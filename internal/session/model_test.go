package session

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewIDIsUUID(t *testing.T) {
	id := NewID()
	if !ValidID(id) {
		t.Fatalf("id = %q", id)
	}
	if id1, id2 := NewID(), NewID(); id1 == id2 {
		t.Fatal("UUIDs should be unique")
	}
}

func TestParseID(t *testing.T) {
	id := uuid.NewString()
	got, err := ParseID("  " + strings.ToUpper(id) + "  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("got %q, want %q", got, id)
	}
	for _, bad := range []string{"", "window-1", "../x", "1234", strings.Repeat("a", 36)} {
		if _, err := ParseID(bad); err == nil {
			t.Errorf("ParseID(%q) unexpectedly succeeded", bad)
		}
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
