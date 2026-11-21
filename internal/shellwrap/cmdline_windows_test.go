//go:build windows

package shellwrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApplyCommandLineRunsAProgramUnderAPathWithSpaces is #1960 end to end: a
// stdio server installed under "Program Files" has to start. Without the
// explicit command line, os/exec escapes the quotes WrapWithUserShell put
// around the path as \", cmd.exe has no backslash escape, and the child dies
// with '"C:\...\httptoolkit-mcp.cmd" is not recognized'.
func TestApplyCommandLineRunsAProgramUnderAPathWithSpaces(t *testing.T) {
	shell, shellArgs := WrapWithUserShell(nil, "cmd.exe", nil)
	if !isCmdExeShell(shell) || len(shellArgs) == 0 || shellArgs[0] != "/c" {
		t.Skipf("the user shell here is %q, not cmd.exe", shell)
	}

	dir := filepath.Join(t.TempDir(), "Program Files", "HTTP Toolkit")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	script := filepath.Join(dir, "httptoolkit-mcp.cmd")
	require.NoError(t, os.WriteFile(script, []byte("@echo off\r\necho STARTED %1\r\n"), 0o644))

	shell, shellArgs = WrapWithUserShell(nil, script, []string{"--stdio"})
	cmd := exec.Command(shell, shellArgs...)
	ApplyCommandLine(cmd)

	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "cmd.exe could not start %q: %s", script, out)
	require.Contains(t, string(out), "STARTED --stdio")
}
