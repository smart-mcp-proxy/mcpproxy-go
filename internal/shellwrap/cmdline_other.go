//go:build !windows

package shellwrap

import "os/exec"

// ApplyCommandLine is a no-op outside Windows: Unix shells get the command
// string as an ordinary argv element, which is exactly what they expect.
func ApplyCommandLine(*exec.Cmd) {}
