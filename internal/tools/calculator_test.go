package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEvalExpression(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{"1+2*3", 7},
		{"(1+2)*3", 9},
		{"10/4", 2.5},
		{"10%4", 2},
		{"2^3^2", 512},
		{"-2^2", -4},
		{"(-2)^2", 4},
		{"2^-3", 0.125},
		{"1.5+2.25", 3.75},
		{"100/5/2", 10},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got, err := EvalExpression(tc.expr)
			if err != nil {
				t.Fatalf("EvalExpression(%q): %v", tc.expr, err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvalExpressionErrors(t *testing.T) {
	for _, expr := range []string{"", "1/0", "1%0", "1+", "(1+2", "1..2", "abc", "2(3)"} {
		if _, err := EvalExpression(expr); err == nil {
			t.Errorf("EvalExpression(%q) unexpectedly succeeded", expr)
		}
	}
}

func TestCalculatorTool(t *testing.T) {
	tool := NewCalculator()
	args, _ := json.Marshal(map[string]string{"expression": "(12+3)*4"})
	res, err := tool.Execute(context.Background(), Call{Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatal(err)
	}
	if out["result"] != float64(60) {
		t.Fatalf("result = %v", out)
	}
	if _, err := tool.Execute(context.Background(), Call{Arguments: json.RawMessage(`{"expression":""}`)}); err == nil {
		t.Fatal("expected empty expression error")
	}
}
