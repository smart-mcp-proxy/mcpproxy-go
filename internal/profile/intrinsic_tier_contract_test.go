package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// TestNoSecondAnnotationMapping is the T005a grep guard: internal/profile's
// PRODUCTION code (non-_test.go files) must never read DestructiveHint or
// ReadOnlyHint itself — contracts.AnnotationTier (Spec 109-a) is the ONE
// place that mapping happens; IntrinsicTier only adapts its result
// (data-model.md §2). Test fixtures are exempt (they build raw annotations
// to exercise the adapter) — hence the _test.go skip.
func TestNoSecondAnnotationMapping(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, "_test.go") || !strings.HasSuffix(name, ".go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		require.NoError(t, err)
		content := string(b)
		require.NotContains(t, content, "DestructiveHint", "%s must not read DestructiveHint directly — use contracts.AnnotationTier via IntrinsicTier", name)
		require.NotContains(t, content, "ReadOnlyHint", "%s must not read ReadOnlyHint directly — use contracts.AnnotationTier via IntrinsicTier", name)
	}
}

// TestIntrinsicTier_OneTierMappingAcrossSpecs pins the "one tier mapping
// across Specs 108 and 109" contract (research D30, data-model.md §2):
// IntrinsicTier(a, true).String() == string(contracts.AnnotationTier(a)) for
// every annotation shape — nil, {}, explicit hints, and every tool of the
// enforcement-matrix fixture — and IntrinsicTier(a, false) is always
// TierDestructive regardless of a.
func TestIntrinsicTier_OneTierMappingAcrossSpecs(t *testing.T) {
	trueVal := true
	falseVal := false

	fixtures := []struct {
		name string
		a    *config.ToolAnnotations
	}{
		{"nil annotations", nil},
		{"empty annotations", &config.ToolAnnotations{}},
		{"readOnlyHint true", &config.ToolAnnotations{ReadOnlyHint: &trueVal}},
		{"readOnlyHint explicit false", &config.ToolAnnotations{ReadOnlyHint: &falseVal}},
		{"destructiveHint true", &config.ToolAnnotations{DestructiveHint: &trueVal}},
		{"destructiveHint true + readOnlyHint true", &config.ToolAnnotations{DestructiveHint: &trueVal, ReadOnlyHint: &trueVal}},
		// contracts/enforcement-matrix.md fixture tools.
		{"github:list_issues (readOnly=true)", &config.ToolAnnotations{ReadOnlyHint: &trueVal}},
		{"github:create_issue (readOnly=false)", &config.ToolAnnotations{ReadOnlyHint: &falseVal}},
		{"github:delete_repo (destructive=true)", &config.ToolAnnotations{DestructiveHint: &trueVal}},
		{"github:search_code (none)", nil},
		{"github:get_secret_scanning_alert (readOnly=true)", &config.ToolAnnotations{ReadOnlyHint: &trueVal}},
		{"notion:update_page (readOnly=false)", &config.ToolAnnotations{ReadOnlyHint: &falseVal}},
		{"filesystem:read_text_file (readOnly=true)", &config.ToolAnnotations{ReadOnlyHint: &trueVal}},
	}

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			require.Equal(t, string(contracts.AnnotationTier(f.a)), IntrinsicTier(f.a, true).String(),
				"IntrinsicTier(a, true).String() must equal string(contracts.AnnotationTier(a))")
			require.Equal(t, TierDestructive, IntrinsicTier(f.a, false),
				"IntrinsicTier(a, false) must always be destructive regardless of a")
		})
	}
}

// TestIntrinsicTier_OutOfRangeFailsClosed pins that an out-of-range
// contracts.Tier value (e.g. contracts.TierUnknown, which only the Spec 109
// review composer ever produces, or a hypothetical future value) maps to
// TierDestructive, never propagated unmapped or defaulted to something
// permissive (data-model.md §2 "default: fail closed"). This exercises the
// actual production adapter (tierFromContractsTier, which IntrinsicTier
// itself calls) rather than a hand-copied mirror of its switch, so a
// regression in the real mapping cannot pass silently.
func TestIntrinsicTier_OutOfRangeFailsClosed(t *testing.T) {
	require.Equal(t, TierDestructive, tierFromContractsTier(contracts.TierUnknown))
	require.Equal(t, TierDestructive, tierFromContractsTier(contracts.Tier("bogus")))
}

// TestTierString_MatchesContractsSpelling pins Tier.String() to the
// contracts.Tier spelling for every value (data-model.md §2).
func TestTierString_MatchesContractsSpelling(t *testing.T) {
	require.Equal(t, string(contracts.TierRead), TierRead.String())
	require.Equal(t, string(contracts.TierWrite), TierWrite.String())
	require.Equal(t, string(contracts.TierDestructive), TierDestructive.String())
	require.Equal(t, string(contracts.TierUnannotated), TierUnannotated.String())
}
