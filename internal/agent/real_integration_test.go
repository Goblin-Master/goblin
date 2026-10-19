//go:build integration

package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Goblin-Master/goblin/internal/config"
	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/Goblin-Master/goblin/internal/memory"
	"github.com/Goblin-Master/goblin/internal/session"
	"github.com/Goblin-Master/goblin/internal/tools"
	"github.com/Goblin-Master/goblin/internal/trace"
)

func TestRealDeepSeekToolLoop(t *testing.T) {
	cfg, err := config.Load(config.LoadOptions{ConfigPath: "../../config.yaml", EnvPath: "../../.env"})
	if err != nil {
		t.Skipf("real API test skipped: %v", err)
	}
	if strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) == "" && strings.TrimSpace(os.Getenv("GOBLIN_LLM_API_KEY")) == "" {
		t.Skip("no API key")
	}
	client, err := llm.NewDeepSeek(llm.Options{
		BaseURL: cfg.LLM.BaseURL, APIKey: cfg.LLM.APIKey, Model: cfg.LLM.Model,
		Timeout: cfg.LLM.Timeout, MaxRetries: cfg.LLM.MaxRetries, Temperature: 0.2,
	})
	if err != nil {
		t.Fatal(err)
	}
	root := "../.."
	readDocs, err := tools.NewReadDocs(root, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := tools.NewRegistryWithTimeout(10*time.Second,
		tools.NewCalculator(), tools.NewSearch(), readDocs)
	if err != nil {
		t.Fatal(err)
	}
	memoryManager, err := memory.New(memory.Options{
		Client: client, Model: cfg.LLM.Model,
		MaxContextTokens: 32000, ReserveOutputTokens: 2048,
		KeepRecent: 12, SummaryMaxTokens: 800, CompactRatio: 0.75,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeStore()
	sid := session.NewID()
	if _, err := store.Create(sid); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{
		Client: client, Store: store, Tools: registry, Memory: memoryManager,
		Tracer: trace.Nop{}, Model: cfg.LLM.Model, MaxSteps: 8, Temperature: 0.2,
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := engine.Run(context.Background(), sid, "请用计算器计算 (128+72)*3，只回答最终数字。")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !strings.Contains(first.Text, "600") {
		t.Fatalf("first answer = %q", first.Text)
	}
	if len(first.ToolCalls) == 0 {
		t.Fatal("calculator was not called")
	}
	if first.ToolCalls[0].Name != "calculator" {
		t.Fatalf("tool = %q", first.ToolCalls[0].Name)
	}

	second, err := engine.Run(context.Background(), sid, "再加 100，只回答最终数字。")
	if err != nil {
		t.Fatalf("follow-up: %v", err)
	}
	if !strings.Contains(second.Text, "700") {
		t.Fatalf("follow-up answer = %q", second.Text)
	}

	third, err := engine.Run(context.Background(), sid, "读一下根目录 README.md，告诉我项目使用什么数据库。只回答数据库名。")
	if err != nil {
		t.Fatalf("read_docs run: %v", err)
	}
	if !strings.Contains(strings.ToLower(third.Text), "mysql") {
		t.Fatalf("read_docs answer = %q", third.Text)
	}
	foundRead := false
	for _, call := range third.ToolCalls {
		if call.Name == "read_docs" {
			foundRead = true
		}
	}
	if !foundRead {
		t.Fatalf("read_docs not called: %+v", third.ToolCalls)
	}
}
