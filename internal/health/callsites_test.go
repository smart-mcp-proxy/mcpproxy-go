package health

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCalculateHealthCallSitesSupplyTransportFacts is a drift guard.
//
// HasEndpointURL gates ActionEditURL, and it defaults to false. A call site that
// forgets it does not crash and does not produce a dangerous action — it quietly
// falls back to "Restart", so the REST payload, the MCP `upstream_servers` list
// and the tray would offer *different remedies for the same server*. That is
// exactly the class of cross-surface disagreement this whole change set exists
// to remove, and it is invisible in any single-surface test.
//
// Rather than duplicate every projection's fixture, assert the cheap structural
// invariant: any file that calls health.CalculateHealth must also mention
// HasEndpointURL. Mirrors the spirit of TestRefreshStateSync — catch the
// omission at the seam, in the package that owns the contract.
func TestCalculateHealthCallSitesSupplyTransportFacts(t *testing.T) {
	const callMarker = "health.CalculateHealth("

	// Require an actual assignment, not a mere mention: a struct literal field
	// ("HasEndpointURL:") or a field write (".HasEndpointURL ="). Matching the
	// bare identifier would let a passing comment satisfy the guard, which is
	// exactly the kind of vacuous green this test exists to prevent.
	fieldMarkers := []string{"HasEndpointURL:", ".HasEndpointURL ="}
	assignsField := func(text string) bool {
		for _, marker := range fieldMarkers {
			if strings.Contains(text, marker) {
				return true
			}
		}
		return false
	}

	// Walk the sibling packages under internal/.
	root := filepath.Join("..")

	var missing []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Vendored / generated trees have no business calling this.
			if info.Name() == "testdata" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(contents)
		if !strings.Contains(text, callMarker) {
			return nil
		}
		if !assignsField(text) {
			missing = append(missing, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	if len(missing) > 0 {
		t.Errorf(
			"these files call health.CalculateHealth without assigning HasEndpointURL: %v\n"+
				"Set it from the server's configured URL (empty for stdio). Omitting it\n"+
				"silently downgrades the Edit URL remedy to Restart on that surface only,\n"+
				"so surfaces disagree about how to fix the same server.",
			missing)
	}
}
