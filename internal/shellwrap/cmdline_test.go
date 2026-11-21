package shellwrap

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsCmdExeShell pins the shell detection used by ApplyCommandLine: the
// wrapped command string only needs the explicit command line when the shell
// that will parse it is cmd.exe itself.
func TestIsCmdExeShell(t *testing.T) {
	for _, shell := range []string{"cmd", "cmd.exe", "CMD.EXE", "C:\\Windows\\system32\\cmd.exe"} {
		require.True(t, isCmdExeShell(shell), shell)
	}
	for _, shell := range []string{"/bin/bash", "bash", "sh", "powershell.exe", "pwsh", "/bin/zsh"} {
		require.False(t, isCmdExeShell(shell), shell)
	}
}

// TestCmdExeCommandLineRecoversTheQuotedCommand is the cross-platform half of
// the #1960 fix. cmd.exe /S strips exactly one leading and one trailing quote
// from the text after /c; everything the shell escaping put inside has to come
// out of that strip unchanged, including the quotes around a program path with
// spaces.
func TestCmdExeCommandLineRecoversTheQuotedCommand(t *testing.T) {
	cases := []struct{ name, shell, commandString string }{
		{
			name:          "program path with spaces",
			shell:         "C:\\Windows\\system32\\cmd.exe",
			commandString: "\"C:\\Program Files\\HTTP Toolkit\\httptoolkit-mcp.cmd\" --stdio",
		},
		{
			name:          "bare command",
			shell:         "cmd",
			commandString: "npx -y @modelcontextprotocol/server-everything",
		},
		{
			// ComSpec can point anywhere; a path with a space cannot be
			// prefixed at all, so the line has to be valid without it.
			name:          "shell path with spaces",
			shell:         "C:\\My Tools\\cmd.exe",
			commandString: "\"C:\\Program Files\\thing.cmd\"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := cmdExeCommandLine(tc.shell, tc.commandString)
			require.True(t, strings.HasSuffix(line, "\""), "line must end with the closing quote: %q", line)

			// cmd.exe /S: drop the first and last quote after /c, then hand the
			// rest to its parser. That rest must be the command string
			// WrapWithUserShell built, byte for byte.
			at := strings.Index(line, "/s /c ")
			require.GreaterOrEqual(t, at, 0, line)
			after := line[at+len("/s /c "):]
			require.True(t, strings.HasPrefix(after, "\""), after)
			require.Equal(t, tc.commandString, after[1:len(after)-1])
		})
	}
}

// TestApplyCommandLineLeavesOtherShellsAlone guards the no-op cases: Unix
// shells and Git Bash keep the command string as an ordinary argv element,
// and a direct-exec launcher command (docker) has no /c to rewrite.
func TestApplyCommandLineLeavesOtherShellsAlone(t *testing.T) {
	cmd := exec.Command("/bin/bash", "-l", "-c", "echo hi")
	ApplyCommandLine(cmd)
	require.Nil(t, cmd.SysProcAttr, "a POSIX shell keeps the argv form untouched")

	cmd = exec.Command("docker", "run", "--rm", "image")
	ApplyCommandLine(cmd)
	require.Nil(t, cmd.SysProcAttr, "a direct-exec launcher command keeps the argv form untouched")
}

// TestWrapWithUserShellShapeIsWhatApplyCommandLineRewrites guards the coupling
// between the two functions: ApplyCommandLine only rewrites argv shaped
// [cmd.exe, "/c", commandString], so a change to the wrapping shape silently
// restores the broken argv launch on Windows. stubWindows forces the Windows
// branch on any host.
func TestWrapWithUserShellShapeIsWhatApplyCommandLineRewrites(t *testing.T) {
	stubWindows(t, "cmd.exe")

	shell, args := WrapWithUserShell(nil, "C:\\Program Files\\HTTP Toolkit\\httptoolkit-mcp.cmd", []string{"--stdio"})
	require.Equal(t, "cmd.exe", shell)
	require.Len(t, args, 2, "cmd.exe wrap must stay [/c, commandString]")
	require.Equal(t, "/c", args[0])
	require.Equal(t, "\"C:\\Program Files\\HTTP Toolkit\\httptoolkit-mcp.cmd\" --stdio", args[1])
}
