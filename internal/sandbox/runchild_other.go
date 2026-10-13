//go:build !unix

package sandbox

import (
	"fmt"
	"io"
)

// RunChild is unsupported on non-unix platforms: there is no execve to replace
// the wrapper image. mcpproxy never builds the sandbox re-exec wrapper on these
// platforms (the launcher resolves sandbox mode to a documented no-op), so this
// only guards a misconfigured invocation.
func RunChild(_ []string, diag io.Writer) int {
	if diag == nil {
		diag = io.Discard
	}
	fmt.Fprintln(diag, "sandbox: re-exec wrapper unsupported on this platform")
	return 2
}
