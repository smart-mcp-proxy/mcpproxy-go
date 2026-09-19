package shellwrap

import (
	"os/exec"
	"runtime"
	"testing"
)

// Regression test for the mcpproxy wrapper-server outage on Windows: Git
// Bash could not exec a backslash-style Windows path even though
// WrapWithUserShell correctly single-quoted it, because MSYS's exec layer
// only resolves POSIX-style paths. A backslash path falls through to
// bash's PATH lookup, which fails with "command not found" and mangles the
// path in its own error rendering (backslashes silently dropped). See
// toBashPath in WrapWithUserShell.
func TestWrapWithUserShell_GitBashCanExecWindowsPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only: exercises the real Git Bash exec path")
	}
	bashPath := `C:\Program Files\Git\bin\bash.exe`
	if _, err := exec.Command(bashPath, "--version").Output(); err != nil {
		t.Skipf("Git Bash not available at %s: %v", bashPath, err)
	}

	shell, args := WrapWithUserShell(nil, `C:\Windows\System32\whoami.exe`, nil)
	cmd := exec.Command(shell, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash failed to exec a Windows-style path: %v\nargs=%v\noutput=%s", err, args, out)
	}
}
