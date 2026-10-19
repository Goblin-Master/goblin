//go:build integration

package tools

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/Goblin-Master/goblin/internal/db"
	"github.com/Goblin-Master/goblin/internal/session"
)

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

func TestMySQLTodoStoreLifecycle(t *testing.T) {
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
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(items) != 1 || items[0].ID != "t1" || items[0].Done {
		t.Fatalf("items = %+v", items)
	}
	items, err = store.Add(context.Background(), sid, "run tests")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].ID != "t2" {
		t.Fatalf("items = %+v", items)
	}
	items, err = store.Complete(context.Background(), sid, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if !items[0].Done {
		t.Fatalf("items = %+v", items)
	}
	items, err = store.Remove(context.Background(), sid, "t2")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "t1" {
		t.Fatalf("items = %+v", items)
	}
	if _, err := store.Complete(context.Background(), sid, "missing"); err == nil {
		t.Fatal("expected missing id error")
	}
}

func TestMySQLTodoStoreIsolation(t *testing.T) {
	conn, sessions := openTodoTestDB(t)
	a, b := session.NewID(), session.NewID()
	for _, id := range []string{a, b} {
		if _, err := sessions.Create(id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM sessions WHERE id IN (?, ?)`, a, b)
	})
	store, _ := NewMySQLTodoStore(conn)
	if _, err := store.Add(context.Background(), a, "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(context.Background(), b, "B"); err != nil {
		t.Fatal(err)
	}
	itemsA, _ := store.List(context.Background(), a)
	itemsB, _ := store.List(context.Background(), b)
	if len(itemsA) != 1 || itemsA[0].Text != "A" || len(itemsB) != 1 || itemsB[0].Text != "B" {
		t.Fatalf("leaked: A=%+v B=%+v", itemsA, itemsB)
	}
}

func TestMySQLTodoStoreRequiresSession(t *testing.T) {
	conn, _ := openTodoTestDB(t)
	store, _ := NewMySQLTodoStore(conn)
	if _, err := store.Add(context.Background(), session.NewID(), "orphan"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
	if _, err := store.Add(context.Background(), "bad", "x"); err == nil {
		t.Fatal("expected invalid UUID error")
	}
}
