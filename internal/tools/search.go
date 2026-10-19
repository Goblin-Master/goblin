package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Search struct {
	docs []mockDoc
}

type mockDoc struct {
	Title   string
	Snippet string
	URL     string
}

func NewSearch() *Search {
	return &Search{docs: defaultDocs()}
}

func (s *Search) Definition() Definition {
	return Definition{
		Name:        "search",
		Description: "在本地 mock 文档库中搜索资料，返回标题、摘要和链接。数据是演示用的，不代表真实互联网结果。",
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

	terms := strings.Fields(strings.ToLower(args.Query))
	scored := make([]scoredDoc, 0, len(s.docs))
	for _, doc := range s.docs {
		score := 0
		title := strings.ToLower(doc.Title)
		snippet := strings.ToLower(doc.Snippet)
		for _, term := range terms {
			if strings.Contains(title, term) {
				score += 3
			}
			if strings.Contains(snippet, term) {
				score++
			}
		}
		if score > 0 {
			scored = append(scored, scoredDoc{doc: doc, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].doc.Title < scored[j].doc.Title
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > args.Limit {
		scored = scored[:args.Limit]
	}
	results := make([]map[string]string, 0, len(scored))
	for _, item := range scored {
		results = append(results, map[string]string{
			"title":   item.doc.Title,
			"snippet": item.doc.Snippet,
			"url":     item.doc.URL,
			"source":  "mock",
		})
	}
	return Result{Content: JSONResult(map[string]any{
		"ok":      true,
		"query":   args.Query,
		"results": results,
		"source":  "mock",
	})}, nil
}

type scoredDoc struct {
	doc   mockDoc
	score int
}

func defaultDocs() []mockDoc {
	return []mockDoc{
		{"Go context 取消与超时", "context.Context 用于在调用链中传递取消信号、截止时间和请求范围的值。", "mock://go/context"},
		{"Go 错误处理", "使用 errors.Is、errors.As 和 fmt.Errorf 的 %w 包装错误，保留错误链。", "mock://go/errors"},
		{"DeepSeek Chat Completions", "DeepSeek 提供 OpenAI 兼容的 chat/completions 接口，支持 messages、tools 和 tool_calls。", "mock://deepseek/chat"},
		{"DeepSeek 函数调用", "通过 tools 数组传入 JSON Schema，模型返回 tool_calls，客户端执行后以 role=tool 回填。", "mock://deepseek/tools"},
		{"Agent 主循环", "接收输入、模型决策、执行工具、回填结果，再判断继续循环还是返回最终回答。", "mock://agent/loop"},
		{"上下文窗口管理", "保留最近消息，把较早对话压缩为摘要，避免上下文无限增长。", "mock://agent/context"},
		{"会话隔离", "不同会话拥有独立的对话历史、摘要和工具状态，通过 session id 区分。", "mock://agent/session"},
		{"工具注册表", "每个工具提供名称、描述和 JSON Schema；注册表负责校验、暴露定义和执行。", "mock://agent/tools"},
		{"JSON 原子写", "先写同目录临时文件，再重命名覆盖目标文件，避免进程崩溃留下半截 JSON。", "mock://go/atomic-file"},
		{"Go 测试与 httptest", "使用 httptest.Server 模拟外部 HTTP 服务，在不发真实请求的情况下验证协议。", "mock://go/httptest"},
		{"待办事项管理", "待办属于会话状态，应持久化并按 session 隔离，而不是塞进系统提示词。", "mock://product/todo"},
		{"文档读取安全", "只允许读取白名单目录，拒绝绝对路径、目录穿越和符号链接逃逸。", "mock://security/read-docs"},
	}
}
