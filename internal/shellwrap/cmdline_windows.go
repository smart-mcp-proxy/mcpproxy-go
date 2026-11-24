//go:build windows

package shellwrap

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// UseVerbatimCmdLine makes a `cmd.exe /c <command string>` invocation (the
// shape WrapWithUserShell returns for cmd.exe) reach cmd.exe as written. Go
// would otherwise quote the command string as one argument and escape its
// embedded quotes as \", which cmd.exe does not understand, so any quoted
// path or argument (e.g. one under "Program Files") failed to run (#1960).
// Any other command is left unchanged.
func UseVerbatimCmdLine(cmd *exec.Cmd) {
	if len(cmd.Args) != 3 || cmd.Args[1] != "/c" || !strings.EqualFold(filepath.Base(cmd.Path), "cmd.exe") {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// /s: cmd.exe strips only the outer quotes and runs the rest as written.
	cmd.SysProcAttr.CmdLine = syscall.EscapeArg(cmd.Args[0]) + ` /s /c "` + cmd.Args[2] + `"`
}
