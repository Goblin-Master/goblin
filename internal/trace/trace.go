package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Tracer interface {
	Emit(event string, fields map[string]any)
}

type Nop struct{}

func (Nop) Emit(string, map[string]any) {}

type JSONL struct {
	mu   sync.Mutex
	file *os.File
}

func NewJSONL(dir, name string) (*JSONL, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("trace directory 不能为空")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create trace directory: %w", err)
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open trace file: %w", err)
	}
	return &JSONL{file: f}, nil
}

func (t *JSONL) Emit(event string, fields map[string]any) {
	if t == nil || t.file == nil {
		return
	}
	record := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		record[k] = v
	}
	if _, ok := record["ts"]; !ok {
		record["ts"] = time.Now().Format(time.RFC3339Nano)
	}
	record["event"] = event
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = t.file.Write(append(data, '\n'))
}

func (t *JSONL) Close() error {
	if t == nil || t.file == nil {
		return nil
	}
	return t.file.Close()
}

type Memory struct {
	mu     sync.Mutex
	Events []map[string]any
}

func (m *Memory) Emit(event string, fields map[string]any) {
	record := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		record[k] = v
	}
	record["event"] = event
	m.mu.Lock()
	m.Events = append(m.Events, record)
	m.mu.Unlock()
}

func (m *Memory) Snapshot() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]map[string]any, len(m.Events))
	copy(out, m.Events)
	return out
}

func Preview(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
