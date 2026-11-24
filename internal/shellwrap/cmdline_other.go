//go:build !windows

package shellwrap

import "os/exec"

// UseVerbatimCmdLine is a no-op outside Windows; see cmdline_windows.go.
func UseVerbatimCmdLine(*exec.Cmd) {}
