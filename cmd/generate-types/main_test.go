package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
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
// in generateTypeDefinitions() drifts from internal/health/constants.go, the
// actual source of truth. The comments above those literals claim they are
// "generated from internal/health/constants.go", but nothing enforced that:
// TestContractsInSync only proves the generator's own output matches the
// committed contracts.ts, so a status/action/label added (or renamed) in
// constants.go without a matching hand-edit here would leave contracts.ts
// silently stale — a frontend lookup keyed on the new value returns
// undefined — with the whole suite, including TestContractsInSync, still green.
//
// The check is structural, not a whole-file substring search (short literals
// such as 'ready', 'error' or 'login' also appear in unrelated enums, so a
// substring match passes even after the health-specific entry is deleted or
// two labels are swapped). It parses the specific declarations instead:
//   - each `export const <Name> = '<value>' as const;` binds a TS constant
//     to its literal value;
//   - the HealthStatusValue / HealthAction union blocks must list exactly the
//     constants for health.StatusOrder / health.ActionPriority (plus the
//     empty "none" action);
//   - the HEALTH_STATUS_LABELS / HEALTH_ACTION_LABELS object literals must
//     bind each value's own constant to its own label, with no extra keys.
//
// internal/preflight/contracts_drift_test.go does the same cross-check for the
// preflight taxonomy.
func TestHealthVocabularyMatchesConstants(t *testing.T) {
	generated := generateTypeDefinitions()

	constValue := map[string]string{} // TS const name -> literal value
	for _, m := range regexp.MustCompile(`(?m)^export const (\w+) = '([^']*)' as const;$`).FindAllStringSubmatch(generated, -1) {
		constValue[m[1]] = m[2]
	}

	// block returns the text between `start` and the first `end` after it.
	block := func(start, end string) string {
		t.Helper()
		i := strings.Index(generated, start)
		if i < 0 {
			t.Fatalf("generated contracts.ts has no %q declaration", start)
		}
		rest := generated[i+len(start):]
		j := strings.Index(rest, end)
		if j < 0 {
			t.Fatalf("generated contracts.ts: %q declaration is not terminated by %q", start, end)
		}
		return rest[:j]
	}

	// unionValues resolves `| typeof Const` members to their literal values.
	unionValues := func(body string) []string {
		var vals []string
		for _, m := range regexp.MustCompile(`typeof (\w+)`).FindAllStringSubmatch(body, -1) {
			v, ok := constValue[m[1]]
			if !ok {
				t.Errorf("union member typeof %s has no `export const %s = '...' as const` declaration", m[1], m[1])
				continue
			}
			vals = append(vals, v)
		}
		return vals
	}

	// labelEntries resolves `[Const]: 'label',` entries to value -> label.
	labelEntries := func(body string) map[string]string {
		entries := map[string]string{}
		for _, m := range regexp.MustCompile(`\[(\w+)\]: '([^']*)'`).FindAllStringSubmatch(body, -1) {
			v, ok := constValue[m[1]]
			if !ok {
				t.Errorf("label key [%s] has no `export const %s = '...' as const` declaration", m[1], m[1])
				continue
			}
			if _, dup := entries[v]; dup {
				t.Errorf("label table has more than one entry for value %q", v)
			}
			entries[v] = m[2]
		}
		return entries
	}

	check := func(what string, wantValues []string, gotValues []string, wantLabels map[string]string, gotLabels map[string]string) {
		t.Helper()
		if !reflect.DeepEqual(sortedCopy(gotValues), sortedCopy(wantValues)) {
			t.Errorf("%s union in generated contracts.ts = %v, want %v (from internal/health/constants.go) — update cmd/generate-types/main.go",
				what, sortedCopy(gotValues), sortedCopy(wantValues))
		}
		for value, wantLabel := range wantLabels {
			gotLabel, ok := gotLabels[value]
			if !ok {
				t.Errorf("%s labels have no entry for %q (want %q)", what, value, wantLabel)
				continue
			}
			if gotLabel != wantLabel {
				t.Errorf("%s label for %q = %q, want %q (from internal/health/constants.go)", what, value, gotLabel, wantLabel)
			}
		}
		for value := range gotLabels {
			if _, ok := wantLabels[value]; !ok {
				t.Errorf("%s labels have an extra entry for %q that is not in internal/health/constants.go", what, value)
			}
		}
	}

	// Every status/action value must also be a key of the Go label tables.
	for _, status := range health.StatusOrder {
		if _, ok := health.StatusLabels[status]; !ok {
			t.Errorf("health.StatusLabels has no entry for status %q", status)
		}
	}
	for _, action := range health.ActionPriority {
		if _, ok := health.ActionLabels[action]; !ok {
			t.Errorf("health.ActionLabels has no entry for action %q", action)
		}
	}

	check("HealthStatusValue",
		health.StatusOrder,
		unionValues(block("export type HealthStatusValue =", ";")),
		health.StatusLabels,
		labelEntries(block("export const HEALTH_STATUS_LABELS: Record<HealthStatusValue, string> = {", "};")))

	// HealthAction additionally carries the empty "none" action, which has no label.
	check("HealthAction",
		append([]string{health.ActionNone}, health.ActionPriority...),
		unionValues(block("export type HealthAction =", ";")),
		health.ActionLabels,
		labelEntries(block("export const HEALTH_ACTION_LABELS: Record<string, string> = {", "};")))
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
