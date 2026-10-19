package agent

import (
	"strings"
	"testing"

	"github.com/Goblin-Master/goblin/internal/llm"
)

func TestParseFinalAnswer(t *testing.T) {
	d, err := Parse(llm.ChatResponse{Content: "  hello  ", ReasoningContent: "thinking"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Reply != "hello" || d.Thought != "thinking" || len(d.ToolCalls) != 0 {
		t.Fatalf("decision = %+v", d)
	}
}

func TestParseToolCallUsesContentAsThought(t *testing.T) {
	d, err := Parse(llm.ChatResponse{
		Content: "我先算一下",
		ToolCalls: []llm.ToolCall{{
			ID: "c1", Type: "function",
			Function: llm.FunctionCall{Name: "calculator", Arguments: `{"expression":"1+1"}`},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Thought != "我先算一下" || len(d.ToolCalls) != 1 {
		t.Fatalf("decision = %+v", d)
	}
}

func TestParseReasoningBeatsContentForThought(t *testing.T) {
	d, err := Parse(llm.ChatResponse{
		Content:          "visible",
		ReasoningContent: "hidden",
		ToolCalls:        []llm.ToolCall{{ID: "c1", Function: llm.FunctionCall{Name: "search"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Thought != "hidden" {
		t.Fatalf("thought = %q", d.Thought)
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := Parse(llm.ChatResponse{}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Parse(llm.ChatResponse{ReasoningContent: "only thought"}); err == nil || !strings.Contains(err.Error(), "思考") {
		t.Fatalf("error = %v", err)
	}
}
