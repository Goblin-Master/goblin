package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Goblin-Master/goblin/internal/llm"
)

var ErrUnparsedToolMarkup = errors.New("模型返回了无法解析的工具调用标记")

type Decision struct {
	Thought   string
	ToolCalls []llm.ToolCall
	Reply     string
}

func Parse(resp llm.ChatResponse) (Decision, error) {
	thought := strings.TrimSpace(resp.ReasoningContent)
	if len(resp.ToolCalls) > 0 {
		if thought == "" {
			thought = strings.TrimSpace(resp.Content)
		}
		return Decision{Thought: thought, ToolCalls: resp.ToolCalls}, nil
	}
	reply := strings.TrimSpace(resp.Content)
	if reply == "" {
		if thought != "" {
			return Decision{}, errors.New("模型只返回了思考内容，没有最终回答或工具调用")
		}
		return Decision{}, errors.New("模型返回空内容")
	}
	if looksLikeToolMarkup(reply) {
		return Decision{}, fmt.Errorf("%w；本次输出未作为最终回答", ErrUnparsedToolMarkup)
	}
	return Decision{Thought: thought, Reply: reply}, nil
}

func looksLikeToolMarkup(content string) bool {
	lower := strings.ToLower(content)
	for _, marker := range []string{
		"<｜｜dsml｜｜",
		"<|tool▁calls",
		"<|tool_calls",
		"<|function_calls",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
