package storage

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every activity type the core can record must be known to the macOS app: in
// the Activity scope filter's knownTypes (State/ScopeFilter.swift) and with an
// explicit label, icon and colour case in ActivityTypePresentation.swift
// (rendered by Views/ActivityView.swift). Spec 125 added skill_quarantine_change
// and skill_access without either, so the app showed the raw type; this test
// fails for the next type added the same way.
func TestValidActivityTypesHaveASwiftLabel(t *testing.T) {
	root := filepath.Join("..", "..", "native", "macos", "MCPProxy", "MCPProxy")
	presentation, err := os.ReadFile(filepath.Join(root, "Views", "ActivityTypePresentation.swift"))
	require.NoError(t, err)
	scope, err := os.ReadFile(filepath.Join(root, "State", "ScopeFilter.swift"))
	require.NoError(t, err)

	known := swiftKnownTypes(t, string(scope))
	for _, fn := range []string{"label", "icon", "color"} {
		cases := swiftSwitchCases(t, string(presentation), fn)
		for _, typ := range ValidActivityTypes {
			require.Containsf(t, cases, typ, "ActivityTypePresentation.%s has no case for activity type %q", fn, typ)
		}
	}
	for _, typ := range ValidActivityTypes {
		require.Containsf(t, known, typ, "ScopeFilter.knownTypes lacks activity type %q", typ)
	}
}

var swiftQuoted = regexp.MustCompile(`"([a-z_]+)"`)

func swiftKnownTypes(t *testing.T, src string) []string {
	t.Helper()
	start := strings.Index(src, "static let knownTypes = [")
	require.GreaterOrEqual(t, start, 0, "knownTypes not found")
	end := strings.Index(src[start:], "]")
	require.Greater(t, end, 0)
	var out []string
	for _, m := range swiftQuoted.FindAllStringSubmatch(src[start:start+end], -1) {
		out = append(out, m[1])
	}
	return out
}

// swiftSwitchCases returns the string literals of the `case` lines inside
// `static func <name>(` up to the next `static func`.
func swiftSwitchCases(t *testing.T, src, name string) []string {
	t.Helper()
	start := strings.Index(src, "static func "+name+"(")
	require.GreaterOrEqualf(t, start, 0, "static func %s not found", name)
	body := src[start+1:]
	if next := strings.Index(body, "static func "); next >= 0 {
		body = body[:next]
	}
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "case ") {
			continue
		}
		head := line[:strings.Index(line, ":")]
		for _, m := range swiftQuoted.FindAllStringSubmatch(head, -1) {
			out = append(out, m[1])
		}
	}
	return out
}
