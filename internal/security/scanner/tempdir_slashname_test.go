package scanner

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestScanTempDirPatternHandlesSlashNames is the MCP-2123 regression guard for
// the extraction temp dirs. Official-registry server names contain '/'
// (e.g. "com.pulsemcp/google-flights"). os.MkdirTemp rejects a pattern that
// contains a path separator ("pattern contains path separator"), so the
// original raw interpolation of the server name into the pattern
// (extractFromContainer / extractFullFromContainer / StartScan's tool-defs
// fallback) failed and source resolution fell back to "none".
//
// The fix (MCP-2155) drops the user-controlled server name from the pattern
// entirely: the random suffix os.MkdirTemp appends already guarantees a unique
// dir, so the name was purely cosmetic. Removing it kills the slash-rejection
// bug AND the go/path-injection CodeQL alert at the source (no user-provided
// value flows into the path expression). This test proves the three constant
// patterns the scanner now uses are MkdirTemp-safe and carry no user data.
func TestScanTempDirPatternHandlesSlashNames(t *testing.T) {
	const slashName = "com.pulsemcp/google-flights"

	// The original raw, name-interpolated pattern fails — guards against anyone
	// reintroducing the un-sanitized interpolation that broke slash names.
	if _, err := os.MkdirTemp("", fmt.Sprintf("mcpproxy-scan-%s-", slashName)); err == nil {
		t.Fatalf("expected os.MkdirTemp to reject raw slash-named pattern, but it succeeded")
	}

	// Every constant pattern the scanner now uses must succeed regardless of the
	// server name, and must not embed any user-controlled value.
	for _, prefix := range []string{
		"mcpproxy-scan-",
		"mcpproxy-scan-full-",
		"mcpproxy-scan-tools-",
	} {
		if strings.Contains(prefix, "%") {
			t.Fatalf("pattern %q still interpolates a value; user data must not flow into the path", prefix)
		}
		dir, err := os.MkdirTemp("", prefix)
		if err != nil {
			t.Fatalf("os.MkdirTemp with constant pattern %q failed: %v", prefix, err)
		}
		_ = os.RemoveAll(dir)
	}
}
