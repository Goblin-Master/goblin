package contextmgr

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode"

	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/Goblin-Master/goblin/internal/session"
)

const toolResultLimit = 16 * 1024

type Options struct {
	Client              llm.ChatClient
	Model               string
	MaxContextTokens    int
	ReserveOutputTokens int
	KeepRecent          int
	SummaryMaxTokens    int
	CompactRatio        float64
}

type Manager struct {
	client        llm.ChatClient
	model         string
	maxContext    int
	reserveOutput int
	keepRecent    int
	summaryMax    int
	ratio         float64
}

func New(opts Options) (*Manager, error) {
	if opts.Client == nil {
		return nil, errors.New("summarizer client 不能为空")
	}
	if opts.Model == "" {
		return nil, errors.New("model 不能为空")
	}
	if opts.MaxContextTokens <= opts.ReserveOutputTokens {
		return nil, errors.New("max context 必须大于 reserve output")
	}
	if opts.KeepRecent < 1 || opts.SummaryMaxTokens < 1 {
		return nil, errors.New("keep_recent / summary_max_tokens 必须大于 0")
	}
	if opts.CompactRatio <= 0 || opts.CompactRatio >= 1 {
		return nil, errors.New("compact ratio 必须在 (0,1)")
	}
	return &Manager{
		client:        opts.Client,
		model:         opts.Model,
		maxContext:    opts.MaxContextTokens,
		reserveOutput: opts.ReserveOutputTokens,
		keepRecent:    opts.KeepRecent,
		summaryMax:    opts.SummaryMaxTokens,
		ratio:         opts.CompactRatio,
	}, nil
}

func (m *Manager) InputBudget() int {
	return m.maxContext - m.reserveOutput
}

func (m *Manager) Build(s *session.Session, system string) []llm.Message {
	if s == nil {
		return nil
	}
	if s.Summary != "" {
		system = strings.TrimRight(system, "\n") + "\n\n## 会话摘要\n" + s.Summary
	}
	out := make([]llm.Message, 0, len(s.Messages)+1)
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: system})
	start := s.SummaryUpTo
	if start < 0 {
		start = 0
	}
	if start > len(s.Messages) {
		start = len(s.Messages)
	}
	for _, msg := range s.Messages[start:] {
		out = append(out, toLLMMessage(msg))
	}
	return out
}

func (m *Manager) MaybeCompact(ctx context.Context, s *session.Session, system string) (bool, error) {
	if s == nil {
		return false, errors.New("session 不能为空")
	}
	compacted := false
	for attempt := 0; attempt < 2; attempt++ {
		if EstimateTokens(m.Build(s, system)) <= m.target() {
			return compacted, nil
		}
		cutoff := m.cutoff(s)
		if cutoff <= s.SummaryUpTo || cutoff >= len(s.Messages) {
			return compacted, nil
		}
		if err := m.compactOnce(ctx, s, cutoff); err != nil {
			return compacted, err
		}
		compacted = true
	}
	return compacted, nil
}

func (m *Manager) target() int {
	return int(float64(m.InputBudget()) * m.ratio)
}

func (m *Manager) cutoff(s *session.Session) int {
	start := s.SummaryUpTo
	if start < 0 {
		start = 0
	}
	if len(s.Messages)-start <= m.keepRecent {
		return start
	}
	cut := len(s.Messages) - m.keepRecent
	if cut < start {
		cut = start
	}
	for cut < len(s.Messages) && s.Messages[cut].Role != llm.RoleUser {
		cut++
	}
	return cut
}

func (m *Manager) compactOnce(ctx context.Context, s *session.Session, cutoff int) error {
	start := s.SummaryUpTo
	if start < 0 {
		start = 0
	}
	if start > cutoff {
		start = cutoff
	}
	oldSummary := strings.TrimSpace(s.Summary)
	transcript := renderTranscript(s.Messages[start:cutoff])
	maxChars := m.InputBudget() * 3 / 2
	if maxChars < 4000 {
		maxChars = 4000
	}
	transcript = session.TruncateRunes(transcript, maxChars)

	user := "已有摘要：\n" + emptyAs(oldSummary, "（无）") + "\n\n需要压缩的对话：\n" + transcript
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "你是会话摘要器。保留用户目标、关键事实、数字、工具结果、未完成事项和重要约束；不要编造，不要逐字复述。输出一段紧凑中文摘要。"},
		{Role: llm.RoleUser, Content: user},
	}
	resp, err := m.client.Chat(ctx, llm.ChatRequest{
		Model:       m.model,
		Messages:    messages,
		Temperature: 0.2,
		MaxTokens:   m.summaryMax,
	})
	if err != nil || strings.TrimSpace(resp.Content) == "" {
		s.Summary = fallbackSummary(oldSummary)
		s.SummaryUpTo = cutoff
		return nil
	}
	s.Summary = session.TruncateRunes(strings.TrimSpace(resp.Content), m.summaryMax*3)
	s.SummaryUpTo = cutoff
	return nil
}

func toLLMMessage(msg session.Message) llm.Message {
	out := llm.Message{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCalls:  msg.ToolCalls,
		ToolCallID: msg.ToolCallID,
	}
	if out.Role == llm.RoleTool && len(out.Content) > toolResultLimit {
		out.Content = session.TruncateRunes(out.Content, toolResultLimit)
	}
	return out
}

func renderTranscript(messages []session.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString("[")
		b.WriteString(msg.Role)
		if msg.ToolCallID != "" {
			b.WriteString(":")
			b.WriteString(msg.ToolCallID)
		}
		b.WriteString("] ")
		if msg.Content != "" {
			b.WriteString(msg.Content)
		}
		for _, call := range msg.ToolCalls {
			b.WriteString("\n  tool_call ")
			b.WriteString(call.Function.Name)
			b.WriteString("(")
			b.WriteString(call.Function.Arguments)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func fallbackSummary(old string) string {
	old = strings.TrimSpace(old)
	if old == "" {
		return "更早的对话内容已省略。"
	}
	return old + "\n\n更早的对话内容已省略。"
}

func emptyAs(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// EstimateTokens uses a conservative heuristic rather than a tokenizer.
func EstimateTokens(messages []llm.Message) int {
	total := 0.0
	for _, msg := range messages {
		total += 4
		total += estimateText(msg.Content)
		total += estimateText(msg.Role)
		for _, call := range msg.ToolCalls {
			total += estimateText(call.Function.Name)
			total += estimateText(call.Function.Arguments)
		}
	}
	return int(math.Ceil(total))
}

func estimateText(s string) float64 {
	total := 0.0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r),
			unicode.Is(unicode.Hiragana, r),
			unicode.Is(unicode.Katakana, r),
			unicode.Is(unicode.Hangul, r):
			total += 1
		case r < 128:
			total += 0.25
		default:
			total += 0.5
		}
	}
	return total
}
