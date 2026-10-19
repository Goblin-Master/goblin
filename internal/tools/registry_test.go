package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type fakeTool struct {
	def    Definition
	run    func(context.Context, Call) (Result, error)
	called int
}

func (f *fakeTool) Definition() Definition { return f.def }
func (f *fakeTool) Execute(ctx context.Context, call Call) (Result, error) {
	f.called++
	return f.run(ctx, call)
}

func echoDefinition(name string) Definition {
	return Definition{
		Name:        name,
		Description: "echo",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func TestRegistryDefinitionsAndExecute(t *testing.T) {
	tool := &fakeTool{def: echoDefinition("echo"), run: func(_ context.Context, call Call) (Result, error) {
		return Result{Content: `{"ok":true,"args":` + string(call.Arguments) + `}`}, nil
	}}
	reg, err := NewRegistry(tool)
	if err != nil {
		t.Fatal(err)
	}
	defs := reg.Definitions()
	if len(defs) != 1 || defs[0].Function.Name != "echo" || defs[0].Type != "function" {
		t.Fatalf("definitions = %+v", defs)
	}
	res := reg.Execute(context.Background(), Call{Name: "echo", Arguments: json.RawMessage(`{"x":1}`)})
	if res.IsError || !strings.Contains(res.Content, `"x":1`) {
		t.Fatalf("result = %+v", res)
	}
}

func TestRegistryRejectsBadTools(t *testing.T) {
	cases := []struct {
		name string
		tool Tool
	}{
		{"duplicate", &fakeTool{def: echoDefinition("same")}},
		{"bad name", &fakeTool{def: Definition{Name: "Bad Name", Description: "x", Parameters: json.RawMessage(`{"type":"object"}`)}}},
		{"no description", &fakeTool{def: Definition{Name: "ok", Description: "", Parameters: json.RawMessage(`{"type":"object"}`)}}},
		{"bad schema", &fakeTool{def: Definition{Name: "ok", Description: "x", Parameters: json.RawMessage(`[]`)}}},
		{"non object schema", &fakeTool{def: Definition{Name: "ok", Description: "x", Parameters: json.RawMessage(`{"type":"string"}`)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.name == "duplicate" {
				_, err = NewRegistry(tc.tool, &fakeTool{def: echoDefinition("same")})
			} else {
				_, err = NewRegistry(tc.tool)
			}
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRegistryErrorPaths(t *testing.T) {
	reg, err := NewRegistry(&fakeTool{
		def: echoDefinition("panic"),
		run: func(context.Context, Call) (Result, error) { panic("boom") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res := reg.Execute(context.Background(), Call{Name: "panic"}); !res.IsError || !strings.Contains(res.Content, "内部错误") {
		t.Fatalf("panic result = %+v", res)
	}
	if res := reg.Execute(context.Background(), Call{Name: "missing"}); !res.IsError {
		t.Fatalf("missing result = %+v", res)
	}
	if res := reg.Execute(context.Background(), Call{Name: "panic", Arguments: json.RawMessage(`not-json`)}); !res.IsError {
		t.Fatalf("invalid args result = %+v", res)
	}
}

func TestRegistryTimeout(t *testing.T) {
	reg, err := NewRegistryWithTimeout(20*time.Millisecond, &fakeTool{
		def: echoDefinition("slow"),
		run: func(ctx context.Context, _ Call) (Result, error) {
			<-ctx.Done()
			return Result{}, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := reg.Execute(context.Background(), Call{Name: "slow"})
	if !res.IsError || !strings.Contains(res.Content, "超时") {
		t.Fatalf("result = %+v", res)
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout took too long")
	}
}

func TestRegistryWrapsPlainResult(t *testing.T) {
	reg, _ := NewRegistry(&fakeTool{
		def: echoDefinition("plain"),
		run: func(context.Context, Call) (Result, error) { return Result{Content: "hello"}, nil },
	})
	res := reg.Execute(context.Background(), Call{Name: "plain"})
	if res.IsError || !strings.Contains(res.Content, `"result":"hello"`) {
		t.Fatalf("result = %+v", res)
	}
}

func TestErrorResultIsJSON(t *testing.T) {
	res := errorResult("bad")
	if !res.IsError || !json.Valid([]byte(res.Content)) {
		t.Fatalf("result = %+v", res)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(res.Content), &v); err != nil || v["ok"] != false {
		t.Fatalf("json = %v", v)
	}
}

func TestRegistryPassesSessionID(t *testing.T) {
	var got string
	reg, _ := NewRegistry(&fakeTool{
		def: echoDefinition("session"),
		run: func(_ context.Context, call Call) (Result, error) {
			got = call.SessionID
			return Result{Content: `{"ok":true}`}, nil
		},
	})
	reg.Execute(context.Background(), Call{Name: "session", SessionID: "abc"})
	if got != "abc" {
		t.Fatalf("session id = %q", got)
	}
}

func TestRegistryExecuteHonorsCanceledContext(t *testing.T) {
	reg, _ := NewRegistryWithTimeout(time.Second, &fakeTool{
		def: echoDefinition("cancel"),
		run: func(ctx context.Context, _ Call) (Result, error) {
			<-ctx.Done()
			return Result{}, ctx.Err()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := reg.Execute(ctx, Call{Name: "cancel"})
	if !res.IsError || !strings.Contains(res.Content, "context canceled") {
		t.Fatalf("result = %+v", res)
	}
}
