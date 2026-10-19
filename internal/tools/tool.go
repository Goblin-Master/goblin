package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Goblin-Master/goblin/internal/llm"
)

type Definition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type Call struct {
	ID        string
	Name      string
	SessionID string
	RunID     string
	Arguments json.RawMessage
}

type Result struct {
	Content string
	IsError bool
}

type Tool interface {
	Definition() Definition
	Execute(ctx context.Context, call Call) (Result, error)
}

type Registry struct {
	tools   map[string]Tool
	order   []string
	timeout time.Duration
}

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

func NewRegistry(tools ...Tool) (*Registry, error) {
	return NewRegistryWithTimeout(10*time.Second, tools...)
}

func NewRegistryWithTimeout(timeout time.Duration, tools ...Tool) (*Registry, error) {
	if timeout <= 0 {
		return nil, errors.New("tool timeout 必须大于 0")
	}
	r := &Registry{tools: make(map[string]Tool), timeout: timeout}
	for _, tool := range tools {
		if tool == nil {
			return nil, errors.New("不能注册 nil 工具")
		}
		def := tool.Definition()
		if !toolNamePattern.MatchString(def.Name) {
			return nil, fmt.Errorf("非法工具名 %q：只允许小写字母、数字和下划线", def.Name)
		}
		if _, exists := r.tools[def.Name]; exists {
			return nil, fmt.Errorf("工具 %q 重复注册", def.Name)
		}
		if strings.TrimSpace(def.Description) == "" {
			return nil, fmt.Errorf("工具 %q 缺少描述", def.Name)
		}
		var schema map[string]any
		if len(def.Parameters) == 0 || json.Unmarshal(def.Parameters, &schema) != nil {
			return nil, fmt.Errorf("工具 %q 的参数必须是合法 JSON Schema", def.Name)
		}
		if schema["type"] != "object" {
			return nil, fmt.Errorf("工具 %q 的参数 Schema 必须是 object", def.Name)
		}
		r.tools[def.Name] = tool
		r.order = append(r.order, def.Name)
	}
	return r, nil
}

func (r *Registry) Definitions() []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		d := r.tools[name].Definition()
		defs = append(defs, llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionDefinition{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Parameters,
			},
		})
	}
	return defs
}

func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

func (r *Registry) Execute(ctx context.Context, call Call) (result Result) {
	tool, ok := r.tools[call.Name]
	if !ok {
		return errorResult(fmt.Sprintf("未知工具 %q", call.Name))
	}
	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil || obj == nil {
		return errorResult("工具参数必须是 JSON object")
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			result = errorResult(fmt.Sprintf("工具 %s 内部错误: %v", call.Name, recovered))
		}
	}()

	res, err := tool.Execute(ctx, call)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errorResult(fmt.Sprintf("工具 %s 执行超时", call.Name))
		}
		return errorResult(err.Error())
	}
	if strings.TrimSpace(res.Content) == "" {
		return errorResult(fmt.Sprintf("工具 %s 返回空结果", call.Name))
	}
	if !json.Valid([]byte(res.Content)) {
		data, _ := json.Marshal(map[string]any{"ok": !res.IsError, "result": res.Content})
		res.Content = string(data)
	}
	return res
}

func errorResult(message string) Result {
	data, _ := json.Marshal(map[string]any{"ok": false, "error": message})
	return Result{Content: string(data), IsError: true}
}

func JSONResult(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return `{"ok":false,"error":"encode result"}`
	}
	return string(data)
}
