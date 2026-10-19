package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Goblin-Master/goblin/internal/fileutil"
	"github.com/Goblin-Master/goblin/internal/session"
)

type Todo struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

type TodoStore struct {
	path string
	mu   sync.Mutex
}

type todoFile struct {
	Version int    `json:"version"`
	Items   []Todo `json:"items"`
}

func NewTodoStore(baseDir string) (*TodoStore, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("todo storage directory 不能为空")
	}
	dir := filepath.Join(baseDir, "todos")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create todos directory: %w", err)
	}
	return &TodoStore{path: dir}, nil
}

func (s *TodoStore) List(sessionID string) ([]Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(sessionID)
}

func (s *TodoStore) Add(sessionID, text string) ([]Todo, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("待办内容不能为空")
	}
	if len([]rune(text)) > 500 {
		return nil, errors.New("待办内容不能超过 500 个字符")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked(sessionID)
	if err != nil {
		return nil, err
	}
	next := 1
	for _, item := range items {
		if n, err := strconv.Atoi(strings.TrimPrefix(item.ID, "t")); err == nil && n >= next {
			next = n + 1
		}
	}
	items = append(items, Todo{ID: fmt.Sprintf("t%d", next), Text: text, CreatedAt: time.Now()})
	if err := s.saveLocked(sessionID, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *TodoStore) Complete(sessionID, id string) ([]Todo, error) {
	return s.update(sessionID, id, func(item *Todo) error {
		item.Done = true
		return nil
	})
}

func (s *TodoStore) Remove(sessionID, id string) ([]Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked(sessionID)
	if err != nil {
		return nil, err
	}
	out := items[:0]
	found := false
	for _, item := range items {
		if item.ID == id {
			found = true
			continue
		}
		out = append(out, item)
	}
	if !found {
		return nil, fmt.Errorf("待办 %q 不存在", id)
	}
	if err := s.saveLocked(sessionID, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *TodoStore) update(sessionID, id string, fn func(*Todo) error) ([]Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked(sessionID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			if err := fn(&items[i]); err != nil {
				return nil, err
			}
			if err := s.saveLocked(sessionID, items); err != nil {
				return nil, err
			}
			return items, nil
		}
	}
	return nil, fmt.Errorf("待办 %q 不存在", id)
}

func (s *TodoStore) loadLocked(sessionID string) ([]Todo, error) {
	if !session.ValidID(sessionID) {
		return nil, fmt.Errorf("非法会话 ID %q", sessionID)
	}
	data, err := os.ReadFile(filepath.Join(s.path, sessionID+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Todo{}, nil
		}
		return nil, fmt.Errorf("read todos: %w", err)
	}
	var file todoFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("待办文件 %q 损坏: %w", sessionID, err)
	}
	if file.Items == nil {
		return []Todo{}, nil
	}
	return file.Items, nil
}

func (s *TodoStore) saveLocked(sessionID string, items []Todo) error {
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, err := json.MarshalIndent(todoFile{Version: 1, Items: items}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := fileutil.WriteAtomic(filepath.Join(s.path, sessionID+".json"), data, 0o600); err != nil {
		return fmt.Errorf("save todos: %w", err)
	}
	return nil
}

type TodoTool struct {
	store *TodoStore
}

func NewTodo(store *TodoStore) *TodoTool {
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

func (t *TodoTool) Execute(_ context.Context, call Call) (Result, error) {
	var args struct {
		Action string `json:"action"`
		Text   string `json:"text"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return Result{}, fmt.Errorf("参数解析失败: %w", err)
	}
	var items []Todo
	var err error
	switch args.Action {
	case "list":
		items, err = t.store.List(call.SessionID)
	case "add":
		items, err = t.store.Add(call.SessionID, args.Text)
	case "complete":
		if strings.TrimSpace(args.ID) == "" {
			return Result{}, errors.New("complete 需要 id")
		}
		items, err = t.store.Complete(call.SessionID, args.ID)
	case "remove":
		if strings.TrimSpace(args.ID) == "" {
			return Result{}, errors.New("remove 需要 id")
		}
		items, err = t.store.Remove(call.SessionID, args.ID)
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
