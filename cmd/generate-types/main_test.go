package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// normalizeEOL strips carriage returns so the comparison is robust on Windows,
// where git may check out contracts.ts with CRLF endings (core.autocrlf=true)
// even though the generator always emits LF. .gitattributes pins this file to
// LF, but normalizing here keeps the test green regardless of git config.
func normalizeEOL(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// TestContractsInSync fails when frontend/src/types/contracts.ts has drifted
// from what cmd/generate-types would produce today. Catches the failure mode
// where a contributor hand-edits contracts.ts (or hand-edits the generator's
// hardcoded string literals) without updating the other side: the next
// `make build` / `go run ./cmd/generate-types` silently reverts their work
// and leaves a dirty working tree.
//
// To fix a failure of this test:
//  1. Decide which side is correct (usually: the generator).
//  2. Run `go run ./cmd/generate-types` from the module root, OR update the
//     string literals in main.go to match contracts.ts.
//  3. Commit both files in the same change.
func TestContractsInSync(t *testing.T) {
	// cmd/generate-types tests run with cwd = the package directory.
	// Walk up two levels to reach the module root.
	contractsPath := filepath.Join("..", "..", contractsRelPath)

	committed, err := os.ReadFile(contractsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", contractsPath, err)
	}

	generated := []byte(generateFileContent())

	if bytes.Equal(normalizeEOL(committed), normalizeEOL(generated)) {
		return
	}

	t.Fatalf(
		"%s is out of sync with cmd/generate-types/main.go.\n"+
			"\nThe TypeScript string literals in main.go must produce a byte-identical\n"+
			"copy of contracts.ts. To fix: either run `go run ./cmd/generate-types`\n"+
			"from the module root (if the generator is the source of truth) or update\n"+
			"the string literals in main.go (if contracts.ts is the source of truth).\n"+
			"\ncommitted size: %d bytes\ngenerated size: %d bytes",
		contractsRelPath, len(committed), len(generated),
	)
}

// TestHealthVocabularyMatchesConstants fails when the hardcoded TypeScript
// string literals in generateTypeDefinitions() drift from
// internal/health/constants.go, the actual source of truth. The comments
// above those literals claim they are "generated from
// internal/health/constants.go", but nothing enforced that: TestContractsInSync
// only proves the generator's own output matches the committed contracts.ts,
// so a status/action/label added (or renamed) in constants.go without a
// matching hand-edit here would leave contracts.ts silently stale — a
// frontend lookup keyed on the new value returns undefined — with the whole
// suite, including TestContractsInSync, still green.
//
// internal/preflight/contracts_drift_test.go already does this cross-check
// for the preflight taxonomy; this is the same pattern applied to the health
// vocabulary this PR adds.
func TestHealthVocabularyMatchesConstants(t *testing.T) {
	generated := generateTypeDefinitions()

	for _, status := range health.StatusOrder {
		literal := fmt.Sprintf("'%s'", status)
		if !strings.Contains(generated, literal) {
			t.Errorf("generated contracts.ts is missing HealthStatusValue %s "+
				"(present in health.StatusOrder) — update cmd/generate-types/main.go", literal)
		}
		label, ok := health.StatusLabels[status]
		if !ok {
			t.Errorf("health.StatusLabels has no entry for status %q", status)
			continue
		}
		labelLiteral := fmt.Sprintf("'%s'", label)
		if !strings.Contains(generated, labelLiteral) {
			t.Errorf("generated contracts.ts is missing status label %s for %q "+
				"(present in health.StatusLabels) — update HEALTH_STATUS_LABELS in cmd/generate-types/main.go",
				labelLiteral, status)
		}
	}

	for _, action := range health.ActionPriority {
		literal := fmt.Sprintf("'%s'", action)
		if !strings.Contains(generated, literal) {
			t.Errorf("generated contracts.ts is missing HealthAction %s "+
				"(present in health.ActionPriority) — update cmd/generate-types/main.go", literal)
		}
		label, ok := health.ActionLabels[action]
		if !ok {
			t.Errorf("health.ActionLabels has no entry for action %q", action)
			continue
		}
		labelLiteral := fmt.Sprintf("'%s'", label)
		if !strings.Contains(generated, labelLiteral) {
			t.Errorf("generated contracts.ts is missing action label %s for %q "+
				"(present in health.ActionLabels) — update HEALTH_ACTION_LABELS in cmd/generate-types/main.go",
				labelLiteral, action)
		}
	}
}
