package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Goblin-Master/goblin/internal/db"
	"github.com/Goblin-Master/goblin/internal/session"
)

type fakeTodoStore struct {
	items map[string][]Todo
}

func newFakeTodoStore() *fakeTodoStore {
	return &fakeTodoStore{items: map[string][]Todo{}}
}

func (f *fakeTodoStore) List(_ context.Context, sessionID string) ([]Todo, error) {
	return append([]Todo(nil), f.items[sessionID]...), nil
}

func (f *fakeTodoStore) Add(_ context.Context, sessionID, text string) ([]Todo, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("待办内容不能为空")
	}
	next := 1
	for _, item := range f.items[sessionID] {
		var n int
		if _, err := fmt.Sscanf(item.ID, "t%d", &n); err == nil && n >= next {
			next = n + 1
		}
	}
	f.items[sessionID] = append(f.items[sessionID], Todo{
		ID: fmt.Sprintf("t%d", next), Text: text, CreatedAt: time.Now(),
	})
	return f.List(context.Background(), sessionID)
}

func (f *fakeTodoStore) Complete(_ context.Context, sessionID, id string) ([]Todo, error) {
	items := f.items[sessionID]
	for i := range items {
		if items[i].ID == id {
			items[i].Done = true
			f.items[sessionID] = items
			return f.List(context.Background(), sessionID)
		}
	}
	return nil, fmt.Errorf("待办 %q 不存在", id)
}

func (f *fakeTodoStore) Remove(_ context.Context, sessionID, id string) ([]Todo, error) {
	items := f.items[sessionID]
	out := make([]Todo, 0, len(items))
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
	f.items[sessionID] = out
	return out, nil
}

func TestTodoToolActions(t *testing.T) {
	store := newFakeTodoStore()
	tool := NewTodo(store)
	sid := session.NewID()
	call := func(args string) Result {
		t.Helper()
		res, err := tool.Execute(context.Background(), Call{SessionID: sid, Arguments: json.RawMessage(args)})
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		return res
	}
	if res := call(`{"action":"add","text":"buy milk"}`); !strings.Contains(res.Content, "buy milk") {
		t.Fatalf("add = %s", res.Content)
	}
	if res := call(`{"action":"list"}`); !strings.Contains(res.Content, `"id":"t1"`) {
		t.Fatalf("list = %s", res.Content)
	}
	if res := call(`{"action":"complete","id":"t1"}`); !strings.Contains(res.Content, `"done":true`) {
		t.Fatalf("complete = %s", res.Content)
	}
	if res := call(`{"action":"remove","id":"t1"}`); !strings.Contains(res.Content, `"items":[]`) {
		t.Fatalf("remove = %s", res.Content)
	}
	if _, err := tool.Execute(context.Background(), Call{SessionID: sid, Arguments: json.RawMessage(`{"action":"add","text":""}`)}); err == nil {
		t.Fatal("expected empty text error")
	}
	if _, err := tool.Execute(context.Background(), Call{SessionID: sid, Arguments: json.RawMessage(`{"action":"complete"}`)}); err == nil {
		t.Fatal("expected missing id error")
	}
	if _, err := tool.Execute(context.Background(), Call{SessionID: "bad/id", Arguments: json.RawMessage(`{"action":"list"}`)}); err == nil {
		t.Fatal("expected invalid session error")
	}
}

func TestTodoToolIsolation(t *testing.T) {
	store := newFakeTodoStore()
	tool := NewTodo(store)
	a, b := session.NewID(), session.NewID()
	for _, tc := range []struct {
		sid  string
		text string
	}{{a, "A"}, {b, "B"}} {
		if _, err := tool.Execute(context.Background(), Call{SessionID: tc.sid, Arguments: json.RawMessage(`{"action":"add","text":"` + tc.text + `"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	resA, _ := tool.Execute(context.Background(), Call{SessionID: a, Arguments: json.RawMessage(`{"action":"list"}`)})
	resB, _ := tool.Execute(context.Background(), Call{SessionID: b, Arguments: json.RawMessage(`{"action":"list"}`)})
	if !strings.Contains(resA.Content, `"A"`) || strings.Contains(resA.Content, `"B"`) {
		t.Fatalf("A = %s", resA.Content)
	}
	if !strings.Contains(resB.Content, `"B"`) || strings.Contains(resB.Content, `"A"`) {
		t.Fatalf("B = %s", resB.Content)
	}
}

func openTodoTestDB(t *testing.T) (*sql.DB, *session.MySQLStore) {
	t.Helper()
	dsn := os.Getenv("GOBLIN_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("GOBLIN_TEST_MYSQL_DSN 未设置")
	}
	conn, err := db.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	store, err := session.NewMySQLStore(conn)
	if err != nil {
		t.Fatal(err)
	}
	return conn, store
}

func TestMySQLTodoStore(t *testing.T) {
	conn, sessions := openTodoTestDB(t)
	sid := session.NewID()
	if _, err := sessions.Create(sid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM sessions WHERE id = ?`, sid) })

	store, err := NewMySQLTodoStore(conn)
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.Add(context.Background(), sid, "write tests")
	if err != nil || len(items) != 1 || items[0].ID != "t1" {
		t.Fatalf("add = %+v, %v", items, err)
	}
	if _, err := store.Complete(context.Background(), sid, "t1"); err != nil {
		t.Fatal(err)
	}
	items, err = store.List(context.Background(), sid)
	if err != nil || !items[0].Done {
		t.Fatalf("list = %+v, %v", items, err)
	}
	if _, err := store.Remove(context.Background(), sid, "missing"); err == nil {
		t.Fatal("expected missing id error")
	}
	if _, err := store.Add(context.Background(), session.NewID(), "orphan"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan add error = %v", err)
	}
}
