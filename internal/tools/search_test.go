package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchTool(t *testing.T) {
	tool := NewSearch()
	args, _ := json.Marshal(map[string]any{"query": "DeepSeek tools", "limit": 2})
	res, err := tool.Execute(context.Background(), Call{Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		OK      bool `json:"ok"`
		Results []struct {
			Title  string `json:"title"`
			Source string `json:"source"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || len(out.Results) == 0 || len(out.Results) > 2 {
		t.Fatalf("results = %+v", out)
	}
	for _, item := range out.Results {
		if item.Source != "mock" {
			t.Fatalf("source = %q", item.Source)
		}
	}
}

func TestSearchNoResults(t *testing.T) {
	tool := NewSearch()
	args, _ := json.Marshal(map[string]string{"query": "zzzzzz-not-found"})
	res, err := tool.Execute(context.Background(), Call{Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, `"results":[]`) {
		t.Fatalf("content = %s", res.Content)
	}
}

func TestSearchRejectsBadInput(t *testing.T) {
	tool := NewSearch()
	for _, args := range []string{`{"query":""}`, `{"query":"x","limit":9}`} {
		if _, err := tool.Execute(context.Background(), Call{Arguments: json.RawMessage(args)}); err == nil {
			t.Errorf("args %s unexpectedly succeeded", args)
		}
	}
}
