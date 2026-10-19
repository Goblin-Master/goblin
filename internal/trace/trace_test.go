package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONL(t *testing.T) {
	dir := t.TempDir()
	tr, err := NewJSONL(dir, "trace.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	tr.Emit("run.start", map[string]any{"run_id": "r1", "session_id": "s1"})
	tr.Emit("tool.call", map[string]any{"tool": "calculator"})
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"event":"run.start"`) || !strings.Contains(text, `"tool":"calculator"`) {
		t.Fatalf("trace = %s", text)
	}
	if strings.Count(text, "\n") != 2 {
		t.Fatalf("lines = %d", strings.Count(text, "\n"))
	}
}

func TestMemoryAndPreview(t *testing.T) {
	m := &Memory{}
	m.Emit("x", map[string]any{"a": 1})
	if len(m.Snapshot()) != 1 || m.Snapshot()[0]["event"] != "x" {
		t.Fatalf("events = %+v", m.Snapshot())
	}
	if got := Preview("你好世界", 2); got != "你好…" {
		t.Fatalf("preview = %q", got)
	}
	if got := Preview("ok", 5); got != "ok" {
		t.Fatalf("preview = %q", got)
	}
}
