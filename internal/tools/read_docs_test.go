package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readDocsFixture(t *testing.T, maxBytes int64) (*ReadDocs, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# readme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "spec.md"), []byte("# spec\nhello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "note.txt"), []byte("not markdown"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.md"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool, err := NewReadDocs(root, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	return tool, root
}

func TestReadDocsList(t *testing.T) {
	tool, _ := readDocsFixture(t, 1024)
	res, err := tool.Execute(context.Background(), Call{Arguments: json.RawMessage(`{"action":"list"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, `"docs/spec.md"`) || strings.Contains(res.Content, "note.txt") {
		t.Fatalf("content = %s", res.Content)
	}
}

func TestReadDocsRead(t *testing.T) {
	tool, _ := readDocsFixture(t, 1024)
	for _, tc := range []struct {
		args    string
		content string
	}{
		{`{"action":"read","path":"README.md"}`, "# readme"},
		{`{"action":"read","path":"docs/spec.md"}`, "# spec"},
		{`{"action":"read","path":"docs\\spec.md"}`, "# spec"},
	} {
		res, err := tool.Execute(context.Background(), Call{Arguments: json.RawMessage(tc.args)})
		if err != nil {
			t.Fatalf("%s: %v", tc.args, err)
		}
		if !strings.Contains(res.Content, tc.content) {
			t.Fatalf("%s: content = %s", tc.args, res.Content)
		}
	}
}

func TestReadDocsRejectsUnsafePaths(t *testing.T) {
	tool, _ := readDocsFixture(t, 1024)
	for _, path := range []string{
		"", ".", "..", "../secret.md", "docs/../secret.md", "secret.md",
		"docs/note.txt", "/etc/passwd", "C:/Windows/win.ini", "docs/missing.md",
	} {
		args, _ := json.Marshal(map[string]string{"action": "read", "path": path})
		if _, err := tool.Execute(context.Background(), Call{Arguments: args}); err == nil {
			t.Errorf("path %q unexpectedly succeeded", path)
		}
	}
}

func TestReadDocsSizeLimit(t *testing.T) {
	tool, _ := readDocsFixture(t, 5)
	if _, err := tool.Execute(context.Background(), Call{Arguments: json.RawMessage(`{"action":"read","path":"docs/spec.md"}`)}); err == nil {
		t.Fatal("expected size limit error")
	}
}

func TestReadDocsSymlinkEscape(t *testing.T) {
	tool, root := readDocsFixture(t, 1024)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "docs", "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := tool.Execute(context.Background(), Call{Arguments: json.RawMessage(`{"action":"read","path":"docs/link.md"}`)}); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}
