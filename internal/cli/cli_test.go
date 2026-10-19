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
	if !strings.Contains(out.String(), "goblin chat") || !strings.Contains(out.String(), "goblin run") {
		t.Fatalf("usage = %s", out.String())
	}
}

func TestMainUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Main([]string{"nope"}, &out, &errOut)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(errOut.String(), "未知命令") {
		t.Fatalf("stderr = %s", errOut.String())
	}
}
