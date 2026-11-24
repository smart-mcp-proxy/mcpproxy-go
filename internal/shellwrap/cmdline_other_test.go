//go:build !windows

package shellwrap

import (
	"os/exec"
	"testing"
)

// Outside Windows UseVerbatimCmdLine must leave the command untouched; the
// verbatim-cmdline behavior itself is asserted in cmdline_windows_test.go.
func TestUseVerbatimCmdLine_NoOpOutsideWindows(t *testing.T) {
	cmd := exec.Command("sh", "-c", "true")
	UseVerbatimCmdLine(cmd)
	if cmd.SysProcAttr != nil {
		t.Fatal("UseVerbatimCmdLine must not touch SysProcAttr outside Windows")
	}
}
