package cli

import (
	"bytes"
	"strings"
	"testing"
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
