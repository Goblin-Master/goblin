package agent

import (
	"errors"
	"strings"

	"github.com/Goblin-Master/goblin/internal/llm"
)

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
	return Decision{Thought: thought, Reply: reply}, nil
}
