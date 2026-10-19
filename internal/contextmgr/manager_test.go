package contextmgr

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/Goblin-Master/goblin/internal/session"
)

type fakeSummarizer struct {
	reply  string
	err    error
	reqs   []llm.ChatRequest
	called int
}

func (f *fakeSummarizer) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.called++
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return llm.ChatResponse{}, f.err
	}
	return llm.ChatResponse{Content: f.reply}, nil
}

func newManager(t *testing.T, client llm.ChatClient, maxContext int) *Manager {
	t.Helper()
	m, err := New(Options{
		Client:              client,
		Model:               "deepseek-chat",
		MaxContextTokens:    maxContext,
		ReserveOutputTokens: 20,
		KeepRecent:          4,
		SummaryMaxTokens:    100,
		CompactRatio:        0.75,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEstimateTokens(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleUser, Content: "你好"},
	}
	got := EstimateTokens(messages)
	if got < 6 || got > 20 {
		t.Fatalf("estimate = %d", got)
	}
}

func TestBuildInjectsSummaryAndStripsReasoning(t *testing.T) {
	m := newManager(t, &fakeSummarizer{}, 1000)
	s := &session.Session{
		Summary:     "earlier facts",
		SummaryUpTo: 1,
		Messages: []session.Message{
			{Role: llm.RoleUser, Content: "old"},
			{Role: llm.RoleAssistant, Content: "visible", Reasoning: "hidden"},
		},
	}
	msgs := m.Build(s, "system")
	if len(msgs) != 2 {
		t.Fatalf("messages = %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "earlier facts") {
		t.Fatalf("system = %q", msgs[0].Content)
	}
	if msgs[1].ReasoningContent != "" {
		t.Fatalf("reasoning leaked: %q", msgs[1].ReasoningContent)
	}
}

func TestBuildTruncatesLargeToolResult(t *testing.T) {
	m := newManager(t, &fakeSummarizer{}, 1000)
	s := &session.Session{Messages: []session.Message{{
		Role:    llm.RoleTool,
		Content: strings.Repeat("x", toolResultLimit+100),
	}}}
	msgs := m.Build(s, "system")
	if len(msgs[1].Content) > toolResultLimit+10 {
		t.Fatalf("tool content length = %d", len(msgs[1].Content))
	}
}

func TestMaybeCompactUnderBudget(t *testing.T) {
	client := &fakeSummarizer{reply: "summary"}
	m := newManager(t, client, 1000)
	s := &session.Session{Messages: []session.Message{{Role: llm.RoleUser, Content: "hi"}}}
	compacted, err := m.MaybeCompact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if compacted || client.called != 0 {
		t.Fatalf("compacted=%v called=%d", compacted, client.called)
	}
}

func TestMaybeCompactSummarizes(t *testing.T) {
	client := &fakeSummarizer{reply: "compressed"}
	m := newManager(t, client, 220)
	s := &session.Session{Messages: []session.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("a", 80)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("b", 80)},
		{Role: llm.RoleUser, Content: strings.Repeat("c", 80)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("d", 80)},
		{Role: llm.RoleUser, Content: strings.Repeat("e", 80)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("f", 80)},
	}}
	compacted, err := m.MaybeCompact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if !compacted || client.called == 0 {
		t.Fatalf("compacted=%v called=%d", compacted, client.called)
	}
	if s.Summary != "compressed" || s.SummaryUpTo == 0 {
		t.Fatalf("summary=%q up_to=%d", s.Summary, s.SummaryUpTo)
	}
	if s.Messages[s.SummaryUpTo].Role != llm.RoleUser {
		t.Fatalf("cutoff split at role %q", s.Messages[s.SummaryUpTo].Role)
	}
	if len(client.reqs[0].Tools) != 0 {
		t.Fatal("summarizer request must not include tools")
	}
}

func TestMaybeCompactFallbackOnFailure(t *testing.T) {
	client := &fakeSummarizer{err: errors.New("boom")}
	m := newManager(t, client, 220)
	s := &session.Session{Messages: []session.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("a", 100)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("b", 100)},
		{Role: llm.RoleUser, Content: strings.Repeat("c", 100)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("d", 100)},
		{Role: llm.RoleUser, Content: strings.Repeat("e", 100)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("f", 100)},
	}}
	compacted, err := m.MaybeCompact(context.Background(), s, "system")
	if err != nil {
		t.Fatalf("fallback should not fail: %v", err)
	}
	if !compacted || !strings.Contains(s.Summary, "省略") {
		t.Fatalf("compacted=%v summary=%q", compacted, s.Summary)
	}
}

func TestMaybeCompactNoUserBoundary(t *testing.T) {
	client := &fakeSummarizer{reply: "x"}
	m := newManager(t, client, 100)
	s := &session.Session{Messages: []session.Message{
		{Role: llm.RoleAssistant, Content: strings.Repeat("a", 100)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("b", 100)},
	}}
	compacted, err := m.MaybeCompact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if compacted || client.called != 0 {
		t.Fatalf("compacted=%v called=%d", compacted, client.called)
	}
}
