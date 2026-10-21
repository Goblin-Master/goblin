package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Goblin-Master/goblin/internal/memory"
)

func TestMainHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Main([]string{"help"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "chat") || !strings.Contains(text, "run") || !strings.Contains(text, "sessions") {
		t.Fatalf("usage = %s", text)
	}
}

func TestMainNoArgsShowsHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Main(nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Available Commands") {
		t.Fatalf("usage = %s", out.String())
	}
}

func TestMainUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Main([]string{"nope"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("stderr = %s", errOut.String())
	}
}

func TestChatRejectsConflictingFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Main([]string{"chat", "--session", "00000000-0000-4000-8000-000000000000", "--new"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(errOut.String(), "--session 与 --new") {
		t.Fatalf("stderr = %s", errOut.String())
	}
}

func TestFormatCompactReport(t *testing.T) {
	cases := []struct {
		name   string
		report memory.Report
		total  int
		cursor int
		want   string
	}{
		{
			name:   "compacted",
			report: memory.Report{Before: 8210, After: 1940, Summarized: 24, Calls: 2, Reason: memory.ReasonCompacted},
			total:  24,
			want:   "已压缩：24 条消息（估算 8210 → 1940 token，摘要调用 2 次）。",
		},
		{
			name:   "nothing",
			report: memory.Report{Reason: memory.ReasonNothingToCompact},
			total:  24,
			cursor: 24,
			want:   "没有可压缩的内容：24/24 条消息已在摘要中。",
		},
		{
			name:   "partial",
			report: memory.Report{Summarized: 18, Calls: 8, Reason: memory.ReasonPartial},
			total:  24,
			cursor: 0,
			want:   "已压缩：18 条，还剩 6 条（达到每次 8 块上限）；再次执行 /compact 继续。",
		},
		{
			name:   "no boundary",
			report: memory.Report{Reason: memory.ReasonNoBoundary},
			total:  24,
			cursor: 10,
			want:   "无法压缩：剩余 14 条消息中找不到可安全切分的 user 边界。",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatCompactReport(tc.report, tc.total, tc.cursor)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
