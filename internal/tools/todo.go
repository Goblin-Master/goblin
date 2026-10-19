package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Goblin-Master/goblin/internal/session"
)

type Todo struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

type TodoStore interface {
	List(ctx context.Context, sessionID string) ([]Todo, error)
	Add(ctx context.Context, sessionID, text string) ([]Todo, error)
	Complete(ctx context.Context, sessionID, id string) ([]Todo, error)
	Remove(ctx context.Context, sessionID, id string) ([]Todo, error)
}

type TodoTool struct {
	store TodoStore
}

func NewTodo(store TodoStore) *TodoTool {
	return &TodoTool{store: store}
}

func (t *TodoTool) Definition() Definition {
	return Definition{
		Name:        "todo",
		Description: "管理当前会话的待办事项。不同会话的待办互相隔离。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["add", "list", "complete", "remove"]},
    "text": {"type": "string", "description": "add 时必填"},
    "id": {"type": "string", "description": "complete/remove 时必填"}
  },
  "required": ["action"],
  "additionalProperties": false
}`),
	}
}

func (t *TodoTool) Execute(ctx context.Context, call Call) (Result, error) {
	var args struct {
		Action string `json:"action"`
		Text   string `json:"text"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return Result{}, fmt.Errorf("参数解析失败: %w", err)
	}
	if _, err := session.ParseID(call.SessionID); err != nil {
		return Result{}, err
	}
	var items []Todo
	var err error
	switch args.Action {
	case "list":
		items, err = t.store.List(ctx, call.SessionID)
	case "add":
		items, err = t.store.Add(ctx, call.SessionID, args.Text)
	case "complete":
		if strings.TrimSpace(args.ID) == "" {
			return Result{}, errors.New("complete 需要 id")
		}
		items, err = t.store.Complete(ctx, call.SessionID, args.ID)
	case "remove":
		if strings.TrimSpace(args.ID) == "" {
			return Result{}, errors.New("remove 需要 id")
		}
		items, err = t.store.Remove(ctx, call.SessionID, args.ID)
	default:
		return Result{}, fmt.Errorf("action 必须是 add/list/complete/remove，收到 %q", args.Action)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Content: JSONResult(map[string]any{
		"ok":     true,
		"action": args.Action,
		"items":  items,
	})}, nil
}
