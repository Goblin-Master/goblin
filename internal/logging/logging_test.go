package logging

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONLFields(t *testing.T) {
	dir := t.TempDir()
	file, err := New(dir, "debug")
	if err != nil {
		t.Fatal(err)
	}
	logger := file.Logger.With("session_id", "s-1", "trace_id", "t-1")
	logger.Info("tool.call", "step", 1, "tool", "calculator", "arguments", `{"expression":"1+1"}`)
	logger.Error("run.end", "ok", false, "error", "boom")
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(filepath.Join(dir, "goblin.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	var lines []map[string]any
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid JSONL: %v: %s", err, scanner.Text())
		}
		lines = append(lines, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	first := lines[0]
	if first["event"] != "tool.call" || first["session_id"] != "s-1" || first["trace_id"] != "t-1" {
		t.Fatalf("first = %+v", first)
	}
	if first["level"] != "INFO" {
		t.Fatalf("level = %v", first["level"])
	}
	if _, ok := first["ts"]; !ok {
		t.Fatalf("missing ts: %+v", first)
	}
	if lines[1]["level"] != "ERROR" || lines[1]["error"] != "boom" {
		t.Fatalf("second = %+v", lines[1])
	}
}

func TestPreview(t *testing.T) {
	if got := Preview("你好世界", 2); got != "你好…" {
		t.Fatalf("preview = %q", got)
	}
	if got := Preview("ok", 5); got != "ok" {
		t.Fatalf("preview = %q", got)
	}
	if got := Preview("abc", 0); got != "" {
		t.Fatalf("preview = %q", got)
	}
}

func TestNewRejectsEmptyDir(t *testing.T) {
	if _, err := New(" ", "info"); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("error = %v", err)
	}
}
