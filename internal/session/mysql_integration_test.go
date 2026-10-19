//go:build integration

package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Goblin-Master/goblin/internal/db"
	"github.com/Goblin-Master/goblin/internal/llm"
)

func openTestStore(t *testing.T) (*MySQLStore, *sql.DB) {
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
	store, err := NewMySQLStore(conn)
	if err != nil {
		t.Fatal(err)
	}
	return store, conn
}

func cleanupSession(t *testing.T, conn *sql.DB, id string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	})
}

func TestMySQLStoreRoundTrip(t *testing.T) {
	store, conn := openTestStore(t)
	id := NewID()
	cleanupSession(t, conn, id)

	sess, err := store.Create(id)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !store.Exists(id) {
		t.Fatal("Exists returned false")
	}
	sess.Title = "test title"
	sess.Summary = "old summary"
	sess.SummaryUpTo = 1
	sess.Messages = []Message{
		{Role: llm.RoleUser, Content: "hello", CreatedAt: time.Now()},
		{
			Role:      llm.RoleAssistant,
			Content:   "calling tool",
			Reasoning: "because",
			ToolCalls: []llm.ToolCall{{
				ID: "call-1", Type: "function",
				Function: llm.FunctionCall{Name: "calculator", Arguments: `{"expression":"1+1"}`},
			}},
			CreatedAt: time.Now(),
		},
		{Role: llm.RoleTool, Content: `{"ok":true}`, ToolCallID: "call-1", CreatedAt: time.Now()},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Title != "test title" || loaded.Summary != "old summary" || loaded.SummaryUpTo != 1 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if len(loaded.Messages) != 3 {
		t.Fatalf("messages = %+v", loaded.Messages)
	}
	if len(loaded.Messages[1].ToolCalls) != 1 || loaded.Messages[1].ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("tool calls = %+v", loaded.Messages[1].ToolCalls)
	}
	if loaded.Messages[2].ToolCallID != "call-1" {
		t.Fatalf("tool call id = %q", loaded.Messages[2].ToolCallID)
	}

	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, meta := range metas {
		if meta.ID == id {
			found = true
			if meta.Messages != 3 {
				t.Fatalf("message count = %d", meta.Messages)
			}
		}
	}
	if !found {
		t.Fatal("session missing from List")
	}

	if _, err := store.Create(id); err == nil {
		t.Fatal("duplicate Create unexpectedly succeeded")
	}
}

func TestMySQLStoreIsolation(t *testing.T) {
	store, conn := openTestStore(t)
	a, b := NewID(), NewID()
	cleanupSession(t, conn, a)
	cleanupSession(t, conn, b)

	for _, id := range []string{a, b} {
		if _, err := store.Create(id); err != nil {
			t.Fatal(err)
		}
	}
	sa, _ := store.Load(a)
	sb, _ := store.Load(b)
	sa.Messages = []Message{{Role: llm.RoleUser, Content: "A"}}
	sb.Messages = []Message{{Role: llm.RoleUser, Content: "B"}}
	_ = store.Save(sa)
	_ = store.Save(sb)

	la, _ := store.Load(a)
	lb, _ := store.Load(b)
	if la.Messages[0].Content != "A" || lb.Messages[0].Content != "B" {
		t.Fatalf("isolation broken: %+v / %+v", la.Messages, lb.Messages)
	}
}

func TestMySQLStoreMissingAndInvalid(t *testing.T) {
	store, _ := openTestStore(t)
	if _, err := store.Load(NewID()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
	for _, id := range []string{"", "window-1", "../../x"} {
		if _, err := store.Create(id); err == nil {
			t.Errorf("Create(%q) unexpectedly succeeded", id)
		}
		if store.Exists(id) {
			t.Errorf("Exists(%q) = true", id)
		}
	}
}

func TestMySQLStoreJSONToolCalls(t *testing.T) {
	store, conn := openTestStore(t)
	id := NewID()
	cleanupSession(t, conn, id)
	if _, err := store.Create(id); err != nil {
		t.Fatal(err)
	}
	sess, _ := store.Load(id)
	calls := []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "todo", Arguments: `{"action":"list"}`}}}
	sess.Messages = []Message{{Role: llm.RoleAssistant, ToolCalls: calls}}
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	loaded, _ := store.Load(id)
	raw, _ := json.Marshal(loaded.Messages[0].ToolCalls)
	if len(raw) == 0 || loaded.Messages[0].ToolCalls[0].Function.Name != "todo" {
		t.Fatalf("tool calls = %s", raw)
	}
}
