package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/Goblin-Master/goblin/internal/memory"
	"github.com/Goblin-Master/goblin/internal/session"
	"github.com/Goblin-Master/goblin/internal/tools"
	"github.com/Goblin-Master/goblin/internal/trace"
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

func newTestEngine(t *testing.T, responses []llm.ChatResponse, registryTools ...tools.Tool) (*Engine, *fakeStore, *fakeLLM, *trace.Memory, string) {
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
	mem := &trace.Memory{}
	engine, err := New(Options{
		Client: client, Store: store, Tools: registry, Memory: memoryManager,
		Tracer: mem, Model: "deepseek-chat", MaxSteps: 8, Temperature: 0.2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine, store, client, mem, sid
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
	if len(mem.Events) == 0 {
		t.Fatal("expected trace events")
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
	if len(client.reqs[1].Tools) != 0 {
		t.Fatal("final step must not expose tools")
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
