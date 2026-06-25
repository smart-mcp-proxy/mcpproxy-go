package core

import (
	"os"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/sandbox"
)

// TestMain doubles as the sandbox re-exec child for this package's launcher
// integration tests. When the test binary is invoked with the production
// `__sandbox_exec -- …` argv (exactly how wrapWithSandbox / sandbox.WrapCommand
// build it), we dispatch straight into sandbox.RunChild instead of running the
// test suite — so the end-to-end path (WrapCommand → re-exec → Apply → exec) is
// exercised against the real binary rather than a mock.
func TestMain(m *testing.M) {
	for i, a := range os.Args {
		if a == sandbox.Subcommand {
			rest := os.Args[i+1:]
			if len(rest) > 0 && rest[0] == "--" {
				rest = rest[1:]
			}
			os.Exit(sandbox.RunChild(rest, os.Stderr))
		}
	}
	os.Exit(m.Run())
}
