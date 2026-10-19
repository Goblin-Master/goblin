package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// File owns the JSONL log file. The file is the single source of structured
// run/tool/LLM events; there is no separate trace stream.
type File struct {
	Logger *slog.Logger
	file   *os.File
}

func New(dir, level string) (*File, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("logging directory 不能为空")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	path := filepath.Join(dir, "goblin.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level:       parseLevel(level),
		ReplaceAttr: replaceAttr,
	})
	return &File{Logger: slog.New(handler), file: f}, nil
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func replaceAttr(_ []string, attr slog.Attr) slog.Attr {
	switch attr.Key {
	case slog.TimeKey:
		attr.Key = "ts"
	case slog.MessageKey:
		attr.Key = "event"
	}
	return attr
}

func (f *File) Close() error {
	if f == nil || f.file == nil {
		return nil
	}
	return f.file.Close()
}

func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
