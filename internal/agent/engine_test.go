package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/Goblin-Master/goblin/internal/memory"
	"github.com/Goblin-Master/goblin/internal/session"
	"github.com/Goblin-Master/goblin/internal/tools"
)

type fakeStore struct {
	sessions map[string]*session.Session
	saves    int
	saveErr  error
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: map[string]*session.Session{}}
}

func (s *fakeStore) Create(id string) (*session.Session, error) {
	if _, ok := s.sessions[id]; ok {
		return nil, os.ErrExist
	}
	sess := &session.Session{ID: id, Messages: []session.Message{}}
	s.sessions[id] = sess
	return sess, nil
}

func (s *fakeStore) Load(id string) (*session.Session, error) {
	sess, ok := s.sessions[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return sess, nil
}

func (s *fakeStore) Save(sess *session.Session) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saves++
	s.sessions[sess.ID] = sess
	return nil
}

func (s *fakeStore) List() ([]session.Meta, error) {
	out := make([]session.Meta, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, session.Meta{ID: sess.ID, Title: sess.Title, Messages: len(sess.Messages)})
	}
	return out, nil
}

func (s *fakeStore) Exists(id string) bool {
	_, ok := s.sessions[id]
	return ok
}

type fakeLLM struct {
	responses []llm.ChatResponse
	reqs      []llm.ChatRequest
}

func (f *fakeLLM) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.reqs = append(f.reqs, req)
	if len(f.responses) == 0 {
		return llm.ChatResponse{}, errors.New("no scripted response")
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

type echoTool struct {
	name  string
	err   error
	calls []tools.Call
}

func (e *echoTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        e.name,
		Description: "echo",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func (e *echoTool) Execute(_ context.Context, call tools.Call) (tools.Result, error) {
	e.calls = append(e.calls, call)
	if e.err != nil {
		return tools.Result{}, e.err
	}
	return tools.Result{Content: tools.JSONResult(map[string]any{"ok": true, "tool": e.name, "args": string(call.Arguments)})}, nil
}

func newTestEngine(t *testing.T, responses []llm.ChatResponse, registryTools ...tools.Tool) (*Engine, *fakeStore, *fakeLLM, *bytes.Buffer, string) {
	t.Helper()
	client := &fakeLLM{responses: responses}
	store := newFakeStore()
	sid := session.NewID()
	if _, err := store.Create(sid); err != nil {
		t.Fatal(err)
	}
	registry, err := tools.NewRegistryWithTimeout(10*time.Second, registryTools...)
	if err != nil {
		t.Fatal(err)
	}
	memoryManager, err := memory.New(memory.Options{
		Client: client, Model: "deepseek-chat",
		MaxContextTokens: 100000, ReserveOutputTokens: 1000,
		KeepRecent: 4, SummaryMaxTokens: 100, CompactRatio: 0.75,
	})
	if err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	engine, err := New(Options{
		Client: client, Store: store, Tools: registry, Memory: memoryManager,
		Logger: logger, Model: "deepseek-chat", MaxSteps: 8, Temperature: 0.2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine, store, client, &logBuf, sid
}

func toolResponse(content, name, args string) llm.ChatResponse {
	return llm.ChatResponse{
		Content: content,
		ToolCalls: []llm.ToolCall{{
			ID: "call-1", Type: "function",
			Function: llm.FunctionCall{Name: name, Arguments: args},
		}},
	}
}

func TestRunDirectAnswer(t *testing.T) {
	echo := &echoTool{name: "echo"}
	engine, store, client, mem, sid := newTestEngine(t, []llm.ChatResponse{{Content: "hello"}}, echo)
	reply, err := engine.Run(context.Background(), sid, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "hello" || reply.Steps != 1 || len(reply.ToolCalls) != 0 {
		t.Fatalf("reply = %+v", reply)
	}
	sess, _ := store.Load(sid)
	if len(sess.Messages) != 2 || sess.Messages[0].Content != "hi" || sess.Messages[1].Content != "hello" {
		t.Fatalf("messages = %+v", sess.Messages)
	}
	if sess.Title != "hi" {
		t.Fatalf("title = %q", sess.Title)
	}
	if len(client.reqs[0].Tools) == 0 {
		t.Fatal("tools should be sent on non-final steps")
	}
	if mem.Len() == 0 {
		t.Fatal("expected log events")
	}
	logText := mem.String()
	if !strings.Contains(logText, `"session_id"`) || !strings.Contains(logText, `"trace_id"`) || !strings.Contains(logText, `"run.start"`) {
		t.Fatalf("log = %s", logText)
	}
}

func TestRunToolLoop(t *testing.T) {
	echo := &echoTool{name: "echo"}
	engine, store, client, _, sid := newTestEngine(t, []llm.ChatResponse{
		toolResponse("我先执行工具", "echo", `{"x":1}`),
		{Content: "done"},
	}, echo)
	reply, err := engine.Run(context.Background(), sid, "use tool")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "done" || reply.Steps != 2 || len(reply.ToolCalls) != 1 {
		t.Fatalf("reply = %+v", reply)
	}
	if len(echo.calls) != 1 || echo.calls[0].SessionID != sid {
		t.Fatalf("calls = %+v", echo.calls)
	}
	sess, _ := store.Load(sid)
	if len(sess.Messages) != 4 {
		t.Fatalf("messages = %+v", sess.Messages)
	}
	if sess.Messages[2].Role != llm.RoleTool || sess.Messages[2].ToolCallID != "call-1" {
		t.Fatalf("tool message = %+v", sess.Messages[2])
	}
	if len(client.reqs) != 2 || !strings.Contains(client.reqs[1].Messages[3].Content, "args") {
		t.Fatalf("second request messages = %+v", client.reqs[1].Messages)
	}
	if client.reqs[1].Messages[2].Role != llm.RoleAssistant || len(client.reqs[1].Messages[2].ToolCalls) != 1 {
		t.Fatalf("assistant tool call missing: %+v", client.reqs[1].Messages)
	}
}

func TestRunMultipleToolCalls(t *testing.T) {
	first := &echoTool{name: "echo1"}
	second := &echoTool{name: "echo2"}
	engine, _, _, _, sid := newTestEngine(t, []llm.ChatResponse{
		{Content: "parallel", ToolCalls: []llm.ToolCall{
			{ID: "c1", Function: llm.FunctionCall{Name: "echo1", Arguments: `{}`}},
			{ID: "c2", Function: llm.FunctionCall{Name: "echo2", Arguments: `{}`}},
		}},
		{Content: "final"},
	}, first, second)
	reply, err := engine.Run(context.Background(), sid, "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.ToolCalls) != 2 || len(first.calls) != 1 || len(second.calls) != 1 {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestRunToolErrorIsFedBack(t *testing.T) {
	echo := &echoTool{name: "echo", err: errors.New("tool broke")}
	engine, store, client, _, sid := newTestEngine(t, []llm.ChatResponse{
		toolResponse("try", "echo", `{}`),
		{Content: "recovered"},
	}, echo)
	reply, err := engine.Run(context.Background(), sid, "go")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "recovered" || len(reply.ToolCalls) != 1 || !reply.ToolCalls[0].IsError {
		t.Fatalf("reply = %+v", reply)
	}
	sess, _ := store.Load(sid)
	if !strings.Contains(sess.Messages[2].Content, "tool broke") {
		t.Fatalf("tool result = %s", sess.Messages[2].Content)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("requests = %d", len(client.reqs))
	}
}

func TestRunUnknownTool(t *testing.T) {
	engine, _, _, _, sid := newTestEngine(t, []llm.ChatResponse{
		toolResponse("try", "missing", `{}`),
		{Content: "ok"},
	})
	reply, err := engine.Run(context.Background(), sid, "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.ToolCalls) != 1 || !reply.ToolCalls[0].IsError {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestRunMaxSteps(t *testing.T) {
	echo := &echoTool{name: "echo"}
	engine, _, client, _, sid := newTestEngine(t, []llm.ChatResponse{
		toolResponse("1", "echo", `{}`),
		toolResponse("2", "echo", `{}`),
	}, echo)
	engine.maxSteps = 2
	_, err := engine.Run(context.Background(), sid, "loop")
	if err == nil || !strings.Contains(err.Error(), "最大轮次") {
		t.Fatalf("error = %v", err)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("requests = %d", len(client.reqs))
	}
	if len(client.reqs[1].Tools) == 0 {
		t.Fatal("final step should keep tools schema with tool_choice=none")
	}
	if client.reqs[1].ToolChoice != "none" {
		t.Fatalf("tool_choice = %q, want none", client.reqs[1].ToolChoice)
	}
	if !strings.Contains(client.reqs[1].Messages[0].Content, "最后一步") {
		t.Fatal("final step must ask the model to stop using tools")
	}
}

func TestRunMalformedToolMarkupAtMaxSteps(t *testing.T) {
	dsml := llm.ChatResponse{Content: "我先读文档。\n<｜｜DSML｜｜ calls>\n<｜｜DSML｜｜ invoke name=\"read_docs\">"}
	engine, store, client, _, sid := newTestEngine(t, []llm.ChatResponse{dsml, dsml})
	engine.maxSteps = 2
	_, err := engine.Run(context.Background(), sid, "read docs")
	if err == nil || !strings.Contains(err.Error(), "最大轮次") {
		t.Fatalf("error = %v, want max steps", err)
	}
	if strings.Contains(err.Error(), "DSML") {
		t.Fatalf("error leaked markup: %v", err)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("requests = %d", len(client.reqs))
	}
	sess, _ := store.Load(sid)
	if len(sess.Messages) != 1 || sess.Messages[0].Role != llm.RoleUser {
		t.Fatalf("messages = %+v", sess.Messages)
	}
}

func TestRunMalformedToolMarkupRetries(t *testing.T) {
	dsml := llm.ChatResponse{Content: "我先读文档。\n<｜｜DSML｜｜ calls>"}
	engine, _, _, _, sid := newTestEngine(t, []llm.ChatResponse{dsml, {Content: "done"}})
	reply, err := engine.Run(context.Background(), sid, "read docs")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "done" {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestRunFollowUp(t *testing.T) {
	engine, _, client, _, sid := newTestEngine(t, []llm.ChatResponse{
		{Content: "first"},
		{Content: "second"},
	})
	if _, err := engine.Run(context.Background(), sid, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), sid, "two"); err != nil {
		t.Fatal(err)
	}
	msgs := client.reqs[1].Messages
	if len(msgs) < 4 || msgs[1].Content != "one" || msgs[2].Content != "first" {
		t.Fatalf("follow-up context = %+v", msgs)
	}
}

func TestRunMissingSession(t *testing.T) {
	engine, _, _, _, _ := newTestEngine(t, nil)
	if _, err := engine.Run(context.Background(), session.NewID(), "hi"); err == nil {
		t.Fatal("expected missing session error")
	}
}

func TestRunInvalidSession(t *testing.T) {
	engine, _, _, _, _ := newTestEngine(t, nil)
	if _, err := engine.Run(context.Background(), "window-1", "hi"); err == nil {
		t.Fatal("expected invalid session error")
	}
}

func TestRunEmptyInput(t *testing.T) {
	engine, _, _, _, sid := newTestEngine(t, nil)
	if _, err := engine.Run(context.Background(), sid, "  "); err == nil {
		t.Fatal("expected empty input error")
	}
}

func withMemory(t *testing.T, engine *Engine, maxContext int) {
	t.Helper()
	m, err := memory.New(memory.Options{
		Client: engine.client, Model: "deepseek-chat",
		MaxContextTokens: maxContext, ReserveOutputTokens: 20,
		KeepRecent: 4, SummaryMaxTokens: 100, CompactRatio: 0.75,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.memory = m
}

func TestCompactFoldsHistory(t *testing.T) {
	engine, store, _, _, sid := newTestEngine(t, []llm.ChatResponse{{Content: "summarized"}})
	sess, _ := store.Load(sid)
	sess.Messages = []session.Message{
		{Role: llm.RoleUser, Content: "one"},
		{Role: llm.RoleAssistant, Content: "first"},
		{Role: llm.RoleUser, Content: "two"},
		{Role: llm.RoleAssistant, Content: "second"},
	}
	saves := store.saves
	report, err := engine.Compact(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != memory.ReasonCompacted || report.Summarized != 4 || report.Calls != 1 {
		t.Fatalf("report = %+v", report)
	}
	if sess.SummaryUpTo != 4 || sess.Summary == "" {
		t.Fatalf("summary=%q up_to=%d", sess.Summary, sess.SummaryUpTo)
	}
	if store.saves != saves+1 {
		t.Fatalf("saves = %d, want %d", store.saves, saves+1)
	}
}

func TestCompactNothingToCompact(t *testing.T) {
	engine, store, client, _, sid := newTestEngine(t, nil)
	sess, _ := store.Load(sid)
	sess.Summary = "old"
	sess.SummaryUpTo = 2
	sess.Messages = []session.Message{
		{Role: llm.RoleUser, Content: "one"},
		{Role: llm.RoleAssistant, Content: "first"},
	}
	saves := store.saves
	report, err := engine.Compact(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != memory.ReasonNothingToCompact || len(client.reqs) != 0 {
		t.Fatalf("report=%+v requests=%d", report, len(client.reqs))
	}
	if store.saves != saves {
		t.Fatalf("nothing to compact must not save")
	}
}

func TestCompactSavesProgressWhenChunkFails(t *testing.T) {
	engine, store, _, _, sid := newTestEngine(t, []llm.ChatResponse{{Content: "ok"}})
	withMemory(t, engine, 220)
	sess, _ := store.Load(sid)
	for i := 0; i < 6; i++ {
		sess.Messages = append(sess.Messages,
			session.Message{Role: llm.RoleUser, Content: strings.Repeat("u", 1000)},
			session.Message{Role: llm.RoleAssistant, Content: strings.Repeat("a", 1000)},
		)
	}
	saves := store.saves
	report, err := engine.Compact(context.Background(), sid)
	if err == nil {
		t.Fatal("expected summarizer failure")
	}
	if report.Reason != memory.ReasonSummaryFailed || report.Summarized != 2 {
		t.Fatalf("report = %+v", report)
	}
	if store.saves != saves+1 {
		t.Fatalf("progress must be saved: saves=%d", store.saves)
	}
	if sess.SummaryUpTo != 2 {
		t.Fatalf("up_to = %d", sess.SummaryUpTo)
	}
}

func TestRunAutoCompactLogsReason(t *testing.T) {
	engine, store, _, logBuf, sid := newTestEngine(t, []llm.ChatResponse{
		{Content: "compressed"}, {Content: "done"},
	})
	withMemory(t, engine, 220)
	sess, _ := store.Load(sid)
	for i := 0; i < 3; i++ {
		sess.Messages = append(sess.Messages,
			session.Message{Role: llm.RoleUser, Content: strings.Repeat("u", 300)},
			session.Message{Role: llm.RoleAssistant, Content: strings.Repeat("a", 300)},
		)
	}
	reply, err := engine.Run(context.Background(), sid, "next")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "done" {
		t.Fatalf("reply = %+v", reply)
	}
	logText := logBuf.String()
	if !strings.Contains(logText, `"context.compact"`) || !strings.Contains(logText, `"still_exceeded"`) {
		t.Fatalf("log = %s", logText)
	}
	if !strings.Contains(logText, `"summarized"`) || !strings.Contains(logText, `"degraded"`) {
		t.Fatalf("log = %s", logText)
	}
}

func TestRunAutoCompactSkipWarn(t *testing.T) {
	engine, store, _, logBuf, sid := newTestEngine(t, []llm.ChatResponse{{Content: "done"}})
	withMemory(t, engine, 220)
	sess, _ := store.Load(sid)
	sess.Messages = []session.Message{
		{Role: llm.RoleAssistant, Content: strings.Repeat("a", 300)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("b", 300)},
	}
	if _, err := engine.Run(context.Background(), sid, "next"); err != nil {
		t.Fatal(err)
	}
	logText := logBuf.String()
	if !strings.Contains(logText, `"context.compact_skip"`) || !strings.Contains(logText, `"no_boundary"`) {
		t.Fatalf("log = %s", logText)
	}
}
