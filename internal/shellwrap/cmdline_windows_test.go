//go:build windows

package shellwrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #1960: a cmd.exe-wrapped command whose path and argument contain spaces
// must run, with the argument intact.
func TestUseVerbatimCmdLine_PathWithSpace(t *testing.T) {
	t.Setenv("SHELL", "") // force cmd.exe even under Git Bash

	dir := filepath.Join(t.TempDir(), "HTTP Toolkit")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "echo-arg.cmd")
	if err := os.WriteFile(script, []byte("@echo off\r\necho [%~1]\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	shell, args := WrapWithUserShell(nil, script, []string{"a b"})
	cmd := exec.Command(shell, args...)
	UseVerbatimCmdLine(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "[a b]" {
		t.Fatalf("output = %q, want %q", got, "[a b]")
	}
}
