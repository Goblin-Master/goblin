package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, server *httptest.Server) *DeepSeekClient {
	t.Helper()
	c, err := NewDeepSeek(Options{
		BaseURL:     server.URL + "/",
		APIKey:      "test-key",
		Model:       "deepseek-chat",
		Timeout:     5 * time.Second,
		MaxRetries:  2,
		Temperature: 0.2,
		RetryDelay:  time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestChatSendsToolSchemaAndParsesToolCalls(t *testing.T) {
	var gotAuth, gotPath, gotModel string
	var gotMessages []Message
	var gotTools []ToolDefinition
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var payload chatPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotModel = payload.Model
		gotMessages = payload.Messages
		gotTools = payload.Tools
		w.Header().Set("x-request-id", "req-123")
		_, _ = io.WriteString(w, `{
			"id":"resp-1",
			"choices":[{
				"message":{
					"content":"我先算一下。",
					"reasoning_content":"需要调用计算器。",
					"tool_calls":[{"id":"call-1","type":"function","function":{"name":"calculator","arguments":"{\"expression\":\"1+2\"}"}}]
				},
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`)
	}))
	defer server.Close()

	c := testClient(t, server)
	resp, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "算一下 1+2"}},
		Tools: []ToolDefinition{{
			Type: "function",
			Function: FunctionDefinition{
				Name:        "calculator",
				Description: "calculate",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			},
		}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotModel != "deepseek-chat" {
		t.Fatalf("model = %q", gotModel)
	}
	if len(gotMessages) != 1 || gotMessages[0].Content != "算一下 1+2" {
		t.Fatalf("messages = %+v", gotMessages)
	}
	if len(gotTools) != 1 || gotTools[0].Function.Name != "calculator" {
		t.Fatalf("tools = %+v", gotTools)
	}
	if resp.RequestID != "req-123" || resp.Content != "我先算一下。" || resp.ReasoningContent != "需要调用计算器。" {
		t.Fatalf("response = %+v", resp)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.Usage.TotalTokens != 15 || resp.FinishReason != "tool_calls" {
		t.Fatalf("usage/finish = %+v %q", resp.Usage, resp.FinishReason)
	}
}

func TestChatDoesNotSendReasoningBack(t *testing.T) {
	var gotMessages []Message
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload chatPayload
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotMessages = payload.Messages
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	c := testClient(t, server)
	_, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{
		Role:             RoleAssistant,
		Content:          "answer",
		ReasoningContent: "hidden chain",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if gotMessages[0].ReasoningContent != "" {
		t.Fatalf("reasoning_content was sent back: %q", gotMessages[0].ReasoningContent)
	}
}

func TestRetryOn429(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	c := testClient(t, server)
	resp, err := c.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q", resp.Content)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestNoRetryOn401(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer server.Close()

	c := testClient(t, server)
	_, err := c.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Fatal("error leaked API key")
	}
}

func TestRetryExhaustedOn500(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	defer server.Close()

	c := testClient(t, server)
	if _, err := c.Chat(context.Background(), ChatRequest{}); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3 (initial + 2 retries)", calls.Load())
	}
}

func TestMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `not-json`)
	}))
	defer server.Close()

	c := testClient(t, server)
	if _, err := c.Chat(context.Background(), ChatRequest{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestCanceledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"late"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	c := testClient(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := c.Chat(ctx, ChatRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
