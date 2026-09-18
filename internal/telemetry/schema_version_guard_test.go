package telemetry

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// schemaVersionGuardFile is this file's own name; the guard skips it because
// the fragments below, once concatenated, are exactly the literals it hunts.
const schemaVersionGuardFile = "schema_version_guard_test.go"

// TestSchemaVersionGuard_NoV12PinSurvives (Spec 107 FR-038) walks every Go
// file in this package and asserts that none of the eleven schema-version pins
// that were bumped from v12 to v13 survived, in any of the spellings the
// package used for them. The regex is assembled from fragments so its own
// source never matches itself, and the v3…v12 change-log comment in
// telemetry.go ("v12 adds heartbeat_id") is deliberately outside every
// pattern: it is history, not a pin.
func TestSchemaVersionGuard_NoV12PinSurvives(t *testing.T) {
	const old = "1" + "2"
	// Spellings the eleven bumped sites used, written with <old> in place of
	// the literal so neither this file nor a raw `grep` over the package
	// self-matches: `"schema_version":<old>`, `schema_version:<old>`,
	// `SchemaVersion != <old>`, `const SchemaVersion = <old>`, `want <old>`,
	// `The current version is <old>`.
	patterns := []string{
		`schema_version\\?"?:` + old + `\b`,
		`SchemaVersion\s*!=\s*` + old + `\b`,
		`SchemaVersion\s*=\s*` + old + `\b`,
		`want\s+` + old + `\b`,
		`version\s+is\s+` + old + `\b`,
	}
	re := regexp.MustCompile("(" + strings.Join(patterns, ")|(") + ")")

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || name == schemaVersionGuardFile {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		scanned++
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				t.Errorf("%s:%d: a v%s schema pin survived the v%d bump: %s", name, i+1, old, SchemaVersion, strings.TrimSpace(line))
			}
		}
	}
	if scanned == 0 {
		t.Fatal("guard scanned no files — it is not running in the package directory")
	}
	if SchemaVersion < 13 {
		t.Errorf("SchemaVersion = %d, want >= 13 (Spec 107 US7)", SchemaVersion)
	}
}
