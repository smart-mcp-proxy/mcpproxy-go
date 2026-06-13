package scanner

import (
	"fmt"
	"os"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/dockernaming"
)

// TestScanTempDirPatternHandlesSlashNames is the MCP-2123 regression guard for
// the extraction temp dirs. Official-registry server names contain '/'
// (e.g. "com.pulsemcp/google-flights"). os.MkdirTemp rejects a pattern that
// contains a path separator ("pattern contains path separator"), so the raw
// interpolation used by extractFromContainer / extractFullFromContainer /
// StartScan's tool-defs fallback failed and source resolution fell back to
// "none". The fix sanitizes the name via dockernaming.SanitizeServerName before
// building the pattern. This test proves the sanitized pattern is MkdirTemp-safe
// while the raw one is not.
func TestScanTempDirPatternHandlesSlashNames(t *testing.T) {
	const slashName = "com.pulsemcp/google-flights"

	// Raw name reproduces the original failure — guards against anyone
	// reintroducing the un-sanitized interpolation.
	if _, err := os.MkdirTemp("", fmt.Sprintf("mcpproxy-scan-%s-", slashName)); err == nil {
		t.Fatalf("expected os.MkdirTemp to reject raw slash-named pattern, but it succeeded")
	}

	// Every pattern the scanner uses must succeed once sanitized.
	sanitized := dockernaming.SanitizeServerName(slashName)
	for _, prefix := range []string{
		"mcpproxy-scan-%s-",
		"mcpproxy-scan-full-%s-",
		"mcpproxy-scan-tools-%s-",
	} {
		dir, err := os.MkdirTemp("", fmt.Sprintf(prefix, sanitized))
		if err != nil {
			t.Fatalf("os.MkdirTemp with sanitized pattern %q failed: %v", prefix, err)
		}
		_ = os.RemoveAll(dir)
	}
}
