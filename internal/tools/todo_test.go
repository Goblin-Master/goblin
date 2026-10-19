package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTodoStoreLifecycle(t *testing.T) {
	store, err := NewTodoStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.Add("session-a", "write tests")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "t1" || items[0].Done {
		t.Fatalf("items = %+v", items)
	}
	items, err = store.Complete("session-a", "t1")
	if err != nil {
		t.Fatal(err)
	}
	if !items[0].Done {
		t.Fatal("todo not completed")
	}
	items, err = store.Remove("session-a", "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %+v", items)
	}
	if _, err := store.Complete("session-a", "missing"); err == nil {
		t.Fatal("expected missing id error")
	}
}

func TestTodoStoreIsolation(t *testing.T) {
	store, _ := NewTodoStore(t.TempDir())
	if _, err := store.Add("session-a", "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add("session-b", "B"); err != nil {
		t.Fatal(err)
	}
	a, _ := store.List("session-a")
	b, _ := store.List("session-b")
	if len(a) != 1 || a[0].Text != "A" || len(b) != 1 || b[0].Text != "B" {
		t.Fatalf("leaked: a=%+v b=%+v", a, b)
	}
}

func TestTodoStorePersists(t *testing.T) {
	dir := t.TempDir()
	first, _ := NewTodoStore(dir)
	if _, err := first.Add("session-a", "persist me"); err != nil {
		t.Fatal(err)
	}
	second, _ := NewTodoStore(dir)
	items, err := second.List("session-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Text != "persist me" {
		t.Fatalf("items = %+v", items)
	}
}

func TestTodoToolActions(t *testing.T) {
	store, _ := NewTodoStore(t.TempDir())
	tool := NewTodo(store)
	call := func(args string) Result {
		t.Helper()
		res, err := tool.Execute(context.Background(), Call{SessionID: "session-a", Arguments: json.RawMessage(args)})
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
	if _, err := tool.Execute(context.Background(), Call{SessionID: "session-a", Arguments: json.RawMessage(`{"action":"add","text":""}`)}); err == nil {
		t.Fatal("expected empty text error")
	}
	if _, err := tool.Execute(context.Background(), Call{SessionID: "session-a", Arguments: json.RawMessage(`{"action":"complete"}`)}); err == nil {
		t.Fatal("expected missing id error")
	}
	if _, err := tool.Execute(context.Background(), Call{SessionID: "bad/id", Arguments: json.RawMessage(`{"action":"list"}`)}); err == nil {
		t.Fatal("expected invalid session error")
	}
}
