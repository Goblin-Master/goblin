package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Goblin-Master/goblin/internal/llm"
)

func newStore(t *testing.T) *JSONStore {
	t.Helper()
	store, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestCreateSaveLoad(t *testing.T) {
	store := newStore(t)
	sess, err := store.Create("window-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Title = "first"
	sess.Messages = append(sess.Messages, Message{
		Role:      llm.RoleUser,
		Content:   "hello",
		CreatedAt: time.Now(),
	})
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load("window-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Title != "first" || len(loaded.Messages) != 1 || loaded.Messages[0].Content != "hello" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Version != Version {
		t.Fatalf("version = %d", loaded.Version)
	}
}

func TestSessionsAreIsolated(t *testing.T) {
	store := newStore(t)
	a, _ := store.Create("a")
	b, _ := store.Create("b")
	a.Messages = append(a.Messages, Message{Role: llm.RoleUser, Content: "A"})
	b.Messages = append(b.Messages, Message{Role: llm.RoleUser, Content: "B"})
	if err := store.Save(a); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(b); err != nil {
		t.Fatal(err)
	}
	loadedA, _ := store.Load("a")
	loadedB, _ := store.Load("b")
	if loadedA.Messages[0].Content != "A" || loadedB.Messages[0].Content != "B" {
		t.Fatalf("sessions leaked: A=%v B=%v", loadedA.Messages, loadedB.Messages)
	}
}

func TestListSortedByUpdatedAt(t *testing.T) {
	store := newStore(t)
	a, _ := store.Create("a")
	time.Sleep(2 * time.Millisecond)
	b, _ := store.Create("b")
	a.Title = "old"
	b.Title = "new"
	_ = store.Save(a)
	_ = store.Save(b)
	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 || metas[0].ID != "b" {
		t.Fatalf("metas = %+v", metas)
	}
}

func TestInvalidSessionID(t *testing.T) {
	store := newStore(t)
	for _, id := range []string{"", "../x", "a/b", strings.Repeat("a", 65), "中文"} {
		if _, err := store.Create(id); err == nil {
			t.Errorf("Create(%q) unexpectedly succeeded", id)
		}
	}
}

func TestCorruptSessionFile(t *testing.T) {
	store := newStore(t)
	if _, err := store.Create("broken"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.dir, "broken.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := store.Load("broken")
	if err == nil || !strings.Contains(err.Error(), "损坏") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadOrCreate(t *testing.T) {
	store := newStore(t)
	first, err := store.LoadOrCreate("same")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.LoadOrCreate("same")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("expected same session, got %+v then %+v", first, second)
	}
}

func TestLoadMissing(t *testing.T) {
	store := newStore(t)
	_, err := store.Load("missing")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
}

func TestNewIDIsUUID(t *testing.T) {
	id := NewID()
	if !IsUUID(id) || !ValidID(id) {
		t.Fatalf("id = %q", id)
	}
	if NewID() == NewID() {
		t.Fatal("UUIDs should be unique")
	}
}

func TestTitleFromInput(t *testing.T) {
	if got := TitleFromInput("  hello  "); got != "hello" {
		t.Fatalf("title = %q", got)
	}
	long := strings.Repeat("中", 40)
	got := TitleFromInput(long)
	if len([]rune(got)) != 31 || !strings.HasSuffix(got, "…") {
		t.Fatalf("title = %q", got)
	}
}
