package memory

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

// scriptedSummarizer fails or answers per call, so tests can exercise a
// failure after earlier chunks already succeeded.
type scriptedSummarizer struct {
	replies []string
	errs    []error
	reqs    []llm.ChatRequest
	called  int
}

func (s *scriptedSummarizer) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	s.called++
	s.reqs = append(s.reqs, req)
	idx := s.called - 1
	if idx < len(s.errs) && s.errs[idx] != nil {
		return llm.ChatResponse{}, s.errs[idx]
	}
	if idx < len(s.replies) {
		return llm.ChatResponse{Content: s.replies[idx]}, nil
	}
	return llm.ChatResponse{Content: "summary"}, nil
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

func TestReasonString(t *testing.T) {
	cases := map[Reason]string{
		ReasonUnknown:          "unknown",
		ReasonCompacted:        "compacted",
		ReasonUnderBudget:      "under_budget",
		ReasonNothingToCompact: "nothing_to_compact",
		ReasonNoBoundary:       "no_boundary",
		ReasonPartial:          "partial",
		ReasonStillExceeded:    "still_exceeded",
		ReasonSummaryFailed:    "summary_failed",
	}
	for reason, want := range cases {
		if got := reason.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", reason, got, want)
		}
	}
}

func TestAutoCompactUnderBudget(t *testing.T) {
	client := &fakeSummarizer{reply: "summary"}
	m := newManager(t, client, 1000)
	s := &session.Session{Messages: []session.Message{{Role: llm.RoleUser, Content: "hi"}}}
	report, err := m.AutoCompact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != ReasonUnderBudget || report.Summarized != 0 || client.called != 0 {
		t.Fatalf("report=%+v called=%d", report, client.called)
	}
	if report.Before != report.After || report.Degraded {
		t.Fatalf("report = %+v", report)
	}
}

func TestAutoCompactSummarizes(t *testing.T) {
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
	report, err := m.AutoCompact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != ReasonCompacted || report.Summarized != 2 || report.Calls != 1 {
		t.Fatalf("report = %+v", report)
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
	if report.Degraded {
		t.Fatal("degraded should be false when the summarizer succeeds")
	}
}

func TestAutoCompactDegradesOnFailure(t *testing.T) {
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
	report, err := m.AutoCompact(context.Background(), s, "system")
	if err != nil {
		t.Fatalf("fallback should not fail: %v", err)
	}
	if report.Summarized == 0 || !report.Degraded || !strings.Contains(s.Summary, "省略") {
		t.Fatalf("report=%+v summary=%q", report, s.Summary)
	}
	if s.SummaryUpTo != report.Summarized {
		t.Fatalf("up_to=%d summarized=%d", s.SummaryUpTo, report.Summarized)
	}
}

func TestAutoCompactNoBoundary(t *testing.T) {
	client := &fakeSummarizer{reply: "x"}
	m := newManager(t, client, 100)
	s := &session.Session{Messages: []session.Message{
		{Role: llm.RoleAssistant, Content: strings.Repeat("a", 100)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("b", 100)},
	}}
	report, err := m.AutoCompact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != ReasonNoBoundary || report.Summarized != 0 || client.called != 0 {
		t.Fatalf("report=%+v called=%d", report, client.called)
	}
}

func TestAutoCompactStillExceeded(t *testing.T) {
	client := &fakeSummarizer{reply: "compressed"}
	m := newManager(t, client, 220)
	s := &session.Session{Messages: []session.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("a", 300)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("b", 300)},
		{Role: llm.RoleUser, Content: strings.Repeat("c", 300)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("d", 300)},
		{Role: llm.RoleUser, Content: strings.Repeat("e", 300)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("f", 300)},
	}}
	report, err := m.AutoCompact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != ReasonStillExceeded || report.Summarized != 2 || report.Calls != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.After <= m.TargetTokens() {
		t.Fatalf("after=%d target=%d", report.After, m.TargetTokens())
	}
}

func TestAutoCompactNothingToCompact(t *testing.T) {
	client := &fakeSummarizer{reply: "x"}
	m := newManager(t, client, 100)
	s := &session.Session{
		Summary:     strings.Repeat("摘", 100),
		SummaryUpTo: 2,
		Messages: []session.Message{
			{Role: llm.RoleUser, Content: "a"},
			{Role: llm.RoleAssistant, Content: "b"},
		},
	}
	report, err := m.AutoCompact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != ReasonNothingToCompact || client.called != 0 {
		t.Fatalf("report=%+v called=%d", report, client.called)
	}
}

func TestManualCompactIgnoresBudget(t *testing.T) {
	client := &fakeSummarizer{reply: "compressed"}
	m := newManager(t, client, 100000)
	s := &session.Session{Messages: []session.Message{
		{Role: llm.RoleUser, Content: "a"},
		{Role: llm.RoleAssistant, Content: "b"},
		{Role: llm.RoleUser, Content: "c"},
		{Role: llm.RoleAssistant, Content: "d"},
	}}
	report, err := m.Compact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != ReasonCompacted || report.Summarized != 4 || report.Calls != 1 {
		t.Fatalf("report = %+v", report)
	}
	if s.SummaryUpTo != 4 || s.Summary != "compressed" {
		t.Fatalf("up_to=%d summary=%q", s.SummaryUpTo, s.Summary)
	}
}

func TestManualCompactNothingToCompact(t *testing.T) {
	client := &fakeSummarizer{reply: "x"}
	m := newManager(t, client, 100000)
	s := &session.Session{
		Summary:     "old",
		SummaryUpTo: 4,
		Messages: []session.Message{
			{Role: llm.RoleUser, Content: "a"},
			{Role: llm.RoleAssistant, Content: "b"},
			{Role: llm.RoleUser, Content: "c"},
			{Role: llm.RoleAssistant, Content: "d"},
		},
	}
	report, err := m.Compact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != ReasonNothingToCompact || client.called != 0 {
		t.Fatalf("report=%+v called=%d", report, client.called)
	}
	if s.Summary != "old" || s.SummaryUpTo != 4 {
		t.Fatalf("session mutated: %+v", s)
	}
}

func TestManualCompactChunks(t *testing.T) {
	client := &fakeSummarizer{reply: "compressed"}
	m := newManager(t, client, 220)
	s := &session.Session{}
	for i := 0; i < 6; i++ {
		s.Messages = append(s.Messages,
			session.Message{Role: llm.RoleUser, Content: strings.Repeat("u", 1000)},
			session.Message{Role: llm.RoleAssistant, Content: strings.Repeat("a", 1000)},
		)
	}
	report, err := m.Compact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != ReasonCompacted || report.Summarized != 12 || s.SummaryUpTo != 12 {
		t.Fatalf("report=%+v up_to=%d", report, s.SummaryUpTo)
	}
	if report.Calls < 2 || client.called != report.Calls {
		t.Fatalf("calls=%d client=%d", report.Calls, client.called)
	}
	for _, req := range client.reqs {
		if len(req.Messages) != 2 {
			t.Fatalf("summarizer request shape = %+v", req.Messages)
		}
	}
}

func TestManualCompactPartialAtCap(t *testing.T) {
	client := &fakeSummarizer{reply: "compressed"}
	m := newManager(t, client, 220)
	s := &session.Session{}
	for i := 0; i < 9; i++ {
		s.Messages = append(s.Messages,
			session.Message{Role: llm.RoleUser, Content: strings.Repeat("u", 1000)},
			session.Message{Role: llm.RoleAssistant, Content: strings.Repeat("a", 1000)},
		)
	}
	report, err := m.Compact(context.Background(), s, "system")
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != ReasonPartial || report.Calls != manualCompactMaxCalls {
		t.Fatalf("report = %+v", report)
	}
	if report.Summarized != 16 || s.SummaryUpTo != 16 {
		t.Fatalf("summarized=%d up_to=%d", report.Summarized, s.SummaryUpTo)
	}
}

func TestManualCompactStrictFailure(t *testing.T) {
	client := &fakeSummarizer{err: errors.New("boom")}
	m := newManager(t, client, 100000)
	s := &session.Session{Messages: []session.Message{
		{Role: llm.RoleUser, Content: "a"},
		{Role: llm.RoleAssistant, Content: "b"},
		{Role: llm.RoleUser, Content: "c"},
		{Role: llm.RoleAssistant, Content: "d"},
	}}
	report, err := m.Compact(context.Background(), s, "system")
	if err == nil {
		t.Fatal("expected error")
	}
	if report.Reason != ReasonSummaryFailed || report.Summarized != 0 || report.Degraded {
		t.Fatalf("report = %+v", report)
	}
	if s.Summary != "" || s.SummaryUpTo != 0 {
		t.Fatalf("failed chunk must not be applied: %+v", s)
	}
}

func TestManualCompactKeepsEarlierChunksOnFailure(t *testing.T) {
	client := &scriptedSummarizer{
		replies: []string{"ok"},
		errs:    []error{nil, errors.New("boom")},
	}
	m := newManager(t, client, 220)
	s := &session.Session{}
	for i := 0; i < 6; i++ {
		s.Messages = append(s.Messages,
			session.Message{Role: llm.RoleUser, Content: strings.Repeat("u", 1000)},
			session.Message{Role: llm.RoleAssistant, Content: strings.Repeat("a", 1000)},
		)
	}
	report, err := m.Compact(context.Background(), s, "system")
	if err == nil {
		t.Fatal("expected error")
	}
	if report.Reason != ReasonSummaryFailed || report.Summarized != 2 || report.Calls != 2 {
		t.Fatalf("report = %+v", report)
	}
	if s.Summary != "ok" || s.SummaryUpTo != 2 {
		t.Fatalf("earlier chunk lost: summary=%q up_to=%d", s.Summary, s.SummaryUpTo)
	}
}
