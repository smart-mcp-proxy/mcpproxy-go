//go:build windows

package shellwrap

import (
	"os/exec"
	"syscall"
)

// ApplyCommandLine hands a shell-wrapped command to cmd.exe through
// SysProcAttr.CmdLine instead of letting os/exec build the command line from
// argv.
//
// Only cmd.exe needs this. A command string produced by WrapWithUserShell is
// already quoted for cmd.exe, and Go escapes an embedded quote as \", which
// cmd.exe reads literally — so a program path containing a space (anything
// under "Program Files", HTTP Toolkit, a user folder with a space) was looked
// up under a name that began with a backslash-quote and never started (#1960).
// CreateProcess uses CmdLine verbatim, so the quoting WrapWithUserShell did
// survives.
//
// Git Bash and PowerShell on Windows keep the argv path: their command string
// uses POSIX quoting, which Go's argv escaping does not damage.
func ApplyCommandLine(cmd *exec.Cmd) {
	if cmd == nil || len(cmd.Args) != 3 || cmd.Args[1] != "/c" {
		return
	}
	if !isCmdExeShell(cmd.Args[0]) {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = cmdExeCommandLine(cmd.Args[0], cmd.Args[2])
}
