package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Search struct{}

func NewSearch() *Search { return &Search{} }

type mockResult struct {
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	URL     string `json:"url"`
	Source  string `json:"source"`
}

var mockResults = []mockResult{
	{"Go context 取消与超时", "context.Context 在调用链中传递取消信号和截止时间。", "mock://go/context", "mock"},
	{"DeepSeek 函数调用", "通过 tools 数组传入 JSON Schema，模型返回 tool_calls。", "mock://deepseek/tools", "mock"},
	{"Agent 主循环", "接收输入、模型决策、执行工具、回填结果，再决定继续或结束。", "mock://agent/loop", "mock"},
	{"上下文与摘要", "保留最近消息，把较早对话压缩为摘要，避免上下文无限增长。", "mock://agent/context", "mock"},
}

func (s *Search) Definition() Definition {
	return Definition{
		Name:        "search",
		Description: "在本地 mock 数据中搜索，返回固定演示结果；不访问互联网。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "搜索关键词"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 5, "description": "返回数量，默认 3"}
  },
  "required": ["query"],
  "additionalProperties": false
}`),
	}
}

func (s *Search) Execute(_ context.Context, call Call) (Result, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return Result{}, fmt.Errorf("参数解析失败: %w", err)
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return Result{}, errors.New("query 不能为空")
	}
	if args.Limit == 0 {
		args.Limit = 3
	}
	if args.Limit < 1 || args.Limit > 5 {
		return Result{}, errors.New("limit 必须在 1-5 之间")
	}

	query := strings.ToLower(args.Query)
	terms := strings.Fields(query)
	results := make([]mockResult, 0, args.Limit)
	for _, item := range mockResults {
		if len(results) >= args.Limit {
			break
		}
		title := strings.ToLower(item.Title)
		snippet := strings.ToLower(item.Snippet)
		for _, term := range terms {
			if strings.Contains(title, term) || strings.Contains(snippet, term) {
				results = append(results, item)
				break
			}
		}
	}
	return Result{Content: JSONResult(map[string]any{
		"ok":      true,
		"query":   args.Query,
		"results": results,
		"source":  "mock",
	})}, nil
}
