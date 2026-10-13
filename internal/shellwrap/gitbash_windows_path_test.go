package shellwrap

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// stubWindows forces the Windows code paths on any host and pins $SHELL, so
// the test cannot silently fall through to a different shell branch
// (resolveLoginShell reads $SHELL first).
func stubWindows(t *testing.T, shell string) {
	t.Helper()
	prev := goos
	goos = osWindows
	t.Cleanup(func() { goos = prev })
	t.Setenv("SHELL", shell)
}

func TestWrapWithUserShell_GitBashPathConversion(t *testing.T) {
	const gitBash = `C:\Program Files\Git\bin\bash.exe`
	tests := []struct {
		name     string
		shell    string
		command  string
		args     []string
		wantArgs []string
	}{
		{
			name:     "backslash .cmd command is converted",
			shell:    gitBash,
			command:  `C:\ProgramData\foo\foo.cmd`,
			wantArgs: []string{"-l", "-c", `C:/ProgramData/foo/foo.cmd`},
		},
		{
			name:     "command with spaces is converted and quoted",
			shell:    gitBash,
			command:  `C:\Program Files\nodejs\npx.cmd`,
			wantArgs: []string{"-l", "-c", `'C:/Program Files/nodejs/npx.cmd'`},
		},
		{
			name:     "drive path arg is converted",
			shell:    gitBash,
			command:  "node",
			args:     []string{`D:\srv\server.js`},
			wantArgs: []string{"-l", "-c", `node D:/srv/server.js`},
		},
		{
			name:     "UNC path arg is converted",
			shell:    gitBash,
			command:  "node",
			args:     []string{`\\host\share\s.js`},
			wantArgs: []string{"-l", "-c", `node //host/share/s.js`},
		},
		{
			name:     "regex arg is left alone",
			shell:    gitBash,
			command:  "grep",
			args:     []string{`a\d+`},
			wantArgs: []string{"-l", "-c", `grep 'a\d+'`},
		},
		{
			name:     "domain user and instance args are left alone",
			shell:    gitBash,
			command:  "tool",
			args:     []string{`DOMAIN\user`, `.\SQLEXPRESS`},
			wantArgs: []string{"-l", "-c", `tool 'DOMAIN\user' '.\SQLEXPRESS'`},
		},
		{
			name:     "cmd.exe is left alone",
			shell:    `C:\Windows\System32\cmd.exe`,
			command:  `C:\ProgramData\foo\foo.cmd`,
			args:     []string{`D:\srv\server.js`},
			wantArgs: []string{"/c", `C:\ProgramData\foo\foo.cmd D:\srv\server.js`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubWindows(t, tt.shell)
			_, got := WrapWithUserShell(nil, tt.command, tt.args)
			if strings.Join(got, "\x00") != strings.Join(tt.wantArgs, "\x00") {
				t.Fatalf("args = %q, want %q", got, tt.wantArgs)
			}
		})
	}
}

func TestWrapWithUserShell_NonWindowsKeepsBackslashes(t *testing.T) {
	prev := goos
	goos = "linux"
	t.Cleanup(func() { goos = prev })
	t.Setenv("SHELL", "/bin/bash")
	_, got := WrapWithUserShell(nil, `C:\x\y.cmd`, nil)
	if want := `'C:\x\y.cmd'`; got[2] != want {
		t.Fatalf("got %q, want %q", got[2], want)
	}
}

// Real Git Bash exec check; only runs on a Windows host with Git Bash.
func TestWrapWithUserShell_GitBashCanExecWindowsPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only: exercises the real Git Bash exec path")
	}
	bashPath := `C:\Program Files\Git\bin\bash.exe`
	if _, err := exec.Command(bashPath, "--version").Output(); err != nil {
		t.Skipf("Git Bash not available at %s: %v", bashPath, err)
	}
	t.Setenv("SHELL", bashPath)

	shell, args := WrapWithUserShell(nil, `C:\Windows\System32\whoami.exe`, nil)
	out, err := exec.Command(shell, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("bash failed to exec a Windows-style path: %v\nargs=%v\noutput=%s", err, args, out)
	}
}
