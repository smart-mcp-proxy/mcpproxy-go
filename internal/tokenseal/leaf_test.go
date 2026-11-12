package tokenseal

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTokensealIsStdlibOnly pins FR-006: the package imports only the Go
// standard library, so internal/storage can use it without pulling in
// internal/secret (keyring) or internal/serveredition.
func TestTokensealIsStdlibOnly(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep != "github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal" {
			t.Errorf("tokenseal must import only the standard library, but depends on %s", dep)
		}
	}
}
