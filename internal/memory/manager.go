package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/Goblin-Master/goblin/internal/session"
)

const (
	toolResultLimit = 16 * 1024
	// manualCompactMaxCalls caps one manual /compact invocation so a very long
	// history cannot issue an unbounded number of summarizer calls. Remaining
	// messages are reported as partial progress and the next /compact resumes.
	manualCompactMaxCalls = 8
)

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

// TargetTokens is the estimated token count above which auto compaction runs.
func (m *Manager) TargetTokens() int {
	return m.target()
}

// Reason explains why a compaction run did or did not make progress.
type Reason int

const (
	ReasonUnknown Reason = iota
	ReasonCompacted
	ReasonUnderBudget
	ReasonNothingToCompact
	ReasonNoBoundary
	ReasonPartial
	ReasonStillExceeded
	ReasonSummaryFailed
)

func (r Reason) String() string {
	switch r {
	case ReasonCompacted:
		return "compacted"
	case ReasonUnderBudget:
		return "under_budget"
	case ReasonNothingToCompact:
		return "nothing_to_compact"
	case ReasonNoBoundary:
		return "no_boundary"
	case ReasonPartial:
		return "partial"
	case ReasonStillExceeded:
		return "still_exceeded"
	case ReasonSummaryFailed:
		return "summary_failed"
	default:
		return "unknown"
	}
}

// Report describes the outcome of one auto or manual compaction run.
type Report struct {
	Before     int
	After      int
	Summarized int
	Calls      int
	Reason     Reason
	Degraded   bool
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
	for _, msg := range s.Messages[m.cursor(s):] {
		out = append(out, toLLMMessage(msg))
	}
	return out
}

// AutoCompact compacts when the built context exceeds the configured budget.
// It makes at most two attempts and degrades to a fallback summary when the
// summarizer fails, so a Run is never blocked by compaction.
func (m *Manager) AutoCompact(ctx context.Context, s *session.Session, system string) (Report, error) {
	if s == nil {
		return Report{Reason: ReasonUnknown}, errors.New("session 不能为空")
	}
	report := Report{Before: EstimateTokens(m.Build(s, system))}
	for attempt := 0; attempt < 2; attempt++ {
		if EstimateTokens(m.Build(s, system)) <= m.target() {
			break
		}
		cutoff := m.autoCutoff(s)
		if cutoff <= m.cursor(s) || cutoff >= len(s.Messages) {
			if report.Summarized == 0 {
				if m.cursor(s) >= len(s.Messages) {
					report.Reason = ReasonNothingToCompact
				} else {
					report.Reason = ReasonNoBoundary
				}
				report.After = EstimateTokens(m.Build(s, system))
				return report, nil
			}
			break
		}
		start := m.cursor(s)
		degraded, err := m.compactOnce(ctx, s, cutoff, compactLenient)
		if err != nil {
			report.Reason = ReasonSummaryFailed
			report.After = EstimateTokens(m.Build(s, system))
			return report, err
		}
		report.Summarized += cutoff - start
		report.Calls++
		report.Degraded = report.Degraded || degraded
	}
	report.After = EstimateTokens(m.Build(s, system))
	switch {
	case report.Summarized == 0 && report.After <= m.target():
		report.Reason = ReasonUnderBudget
	case report.Summarized == 0:
		report.Reason = ReasonNoBoundary
	case report.After > m.target():
		report.Reason = ReasonStillExceeded
	default:
		report.Reason = ReasonCompacted
	}
	return report, nil
}

// Compact folds messages into the summary regardless of the token budget. It
// stops only when the summary cursor reaches the end of the message list, the
// per-invocation chunk cap is hit, or the summarizer fails. A failed chunk is
// not applied, so the session keeps the last good summary.
func (m *Manager) Compact(ctx context.Context, s *session.Session, system string) (Report, error) {
	if s == nil {
		return Report{Reason: ReasonUnknown}, errors.New("session 不能为空")
	}
	report := Report{Before: EstimateTokens(m.Build(s, system))}
	start := m.cursor(s)
	if start >= len(s.Messages) {
		report.After = report.Before
		report.Reason = ReasonNothingToCompact
		return report, nil
	}
	limit := m.transcriptLimit()
	for report.Calls < manualCompactMaxCalls && start < len(s.Messages) {
		cutoff := m.manualCutoff(s, start, limit)
		degraded, err := m.compactOnce(ctx, s, cutoff, compactStrict)
		report.Calls++
		if err != nil {
			report.Reason = ReasonSummaryFailed
			report.After = EstimateTokens(m.Build(s, system))
			if report.Calls == 1 {
				return report, fmt.Errorf("摘要调用失败: %w", err)
			}
			return report, fmt.Errorf("摘要第 %d 块失败: %w", report.Calls, err)
		}
		if degraded {
			report.Degraded = true
		}
		report.Summarized += cutoff - start
		start = cutoff
	}
	report.After = EstimateTokens(m.Build(s, system))
	if start >= len(s.Messages) {
		report.Reason = ReasonCompacted
	} else {
		report.Reason = ReasonPartial
	}
	return report, nil
}

func (m *Manager) target() int {
	return int(float64(m.InputBudget()) * m.ratio)
}

// cursor clamps the summary cursor to the current message list.
func (m *Manager) cursor(s *session.Session) int {
	start := s.SummaryUpTo
	if start < 0 {
		return 0
	}
	if start > len(s.Messages) {
		return len(s.Messages)
	}
	return start
}

// autoCutoff keeps the most recent keepRecent messages and moves the cut to
// the next user message, so an assistant tool_call and its tool result are
// never split across the summary boundary.
func (m *Manager) autoCutoff(s *session.Session) int {
	start := m.cursor(s)
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

// manualCutoff returns the largest safe cutoff whose rendered transcript fits
// maxChars. Safe means the next message is a user message (or the list ends),
// so a tool result never loses its assistant tool_call. If a single turn is
// larger than the limit, it is returned as one chunk and compactOnce truncates
// the transcript instead of skipping the turn.
func (m *Manager) manualCutoff(s *session.Session, start, maxChars int) int {
	used := 0
	cutoff := -1
	for i := start; i < len(s.Messages); i++ {
		used += messageChars(s.Messages[i])
		if used > maxChars {
			break
		}
		if i+1 == len(s.Messages) || s.Messages[i+1].Role == llm.RoleUser {
			cutoff = i + 1
		}
	}
	if cutoff > start {
		return cutoff
	}
	for i := start + 1; i <= len(s.Messages); i++ {
		if i == len(s.Messages) || s.Messages[i].Role == llm.RoleUser {
			return i
		}
	}
	return len(s.Messages)
}

// transcriptLimit bounds the summarizer input. The 3/2 factor is a rough
// chars-per-token compromise between ASCII and CJK text.
func (m *Manager) transcriptLimit() int {
	maxChars := m.InputBudget() * 3 / 2
	if maxChars < 4000 {
		maxChars = 4000
	}
	return maxChars
}

type compactMode int

const (
	compactLenient compactMode = iota
	compactStrict
)

// compactOnce summarizes messages[SummaryUpTo:cutoff] together with the old
// summary. In lenient mode a summarizer failure falls back to the old summary
// and still advances the cursor. In strict mode a failure leaves the session
// untouched so the caller can report it and let the user retry.
func (m *Manager) compactOnce(ctx context.Context, s *session.Session, cutoff int, mode compactMode) (degraded bool, err error) {
	start := m.cursor(s)
	if start > cutoff {
		start = cutoff
	}
	oldSummary := strings.TrimSpace(s.Summary)
	transcript := renderTranscript(s.Messages[start:cutoff])
	transcript = session.TruncateRunes(transcript, m.transcriptLimit())

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
		if mode == compactStrict {
			if err != nil {
				return false, err
			}
			return false, errors.New("摘要返回为空")
		}
		s.Summary = fallbackSummary(oldSummary)
		s.SummaryUpTo = cutoff
		return true, nil
	}
	s.Summary = session.TruncateRunes(strings.TrimSpace(resp.Content), m.summaryMax*3)
	s.SummaryUpTo = cutoff
	return false, nil
}

// messageChars counts the same runes renderTranscript writes for one message.
func messageChars(msg session.Message) int {
	n := 4 + utf8.RuneCountInString(msg.Role) + utf8.RuneCountInString(msg.Content)
	if msg.ToolCallID != "" {
		n += 1 + utf8.RuneCountInString(msg.ToolCallID)
	}
	for _, call := range msg.ToolCalls {
		n += 15 + utf8.RuneCountInString(call.Function.Name) + utf8.RuneCountInString(call.Function.Arguments)
	}
	return n
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
