package contracts

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestAnnotationTier (Spec 109 FR-028 / T014 / X11): one pure function
// computes a tool's tier from its annotations everywhere — the Web Tools
// page, macOS Tools view and CLI `tools list --tier` all call this instead of
// each computing their own (X11: Web computed "write" as the fallback,
// backend computed "read", CLI used operation_type — three different
// answers for the same unannotated tool).
func TestAnnotationTier(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	cases := []struct {
		name string
		ann  *config.ToolAnnotations
		want Tier
	}{
		{"nil annotations -> unannotated", nil, TierUnannotated},
		{"empty annotations -> unannotated", &config.ToolAnnotations{}, TierUnannotated},
		{
			"destructiveHint=true -> destructive, highest priority",
			&config.ToolAnnotations{DestructiveHint: boolPtr(true), ReadOnlyHint: boolPtr(true)},
			TierDestructive,
		},
		{
			"destructiveHint=false, readOnlyHint=false -> write",
			&config.ToolAnnotations{DestructiveHint: boolPtr(false), ReadOnlyHint: boolPtr(false)},
			TierWrite,
		},
		{
			"readOnlyHint=false (explicit) -> write",
			&config.ToolAnnotations{ReadOnlyHint: boolPtr(false)},
			TierWrite,
		},
		{
			"readOnlyHint=true -> read",
			&config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
			TierRead,
		},
		{
			"neither hint set -> unannotated",
			&config.ToolAnnotations{IdempotentHint: boolPtr(true)},
			TierUnannotated,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AnnotationTier(tc.ann)
			if got != tc.want {
				t.Fatalf("AnnotationTier(%+v) = %q, want %q", tc.ann, got, tc.want)
			}
		})
	}
}

func TestTierValuesMatchTheTerminologyTable(t *testing.T) {
	// specs/109-ux-navigation-consistency/spec.md terminology table: exactly
	// these five string values exist anywhere in the system (the fifth,
	// "unknown", is the review composer's own value — never returned by
	// AnnotationTier itself).
	want := map[Tier]bool{
		TierRead:        true,
		TierWrite:       true,
		TierDestructive: true,
		TierUnannotated: true,
		TierUnknown:     true,
	}
	got := map[Tier]string{
		TierRead:        "read",
		TierWrite:       "write",
		TierDestructive: "destructive",
		TierUnannotated: "unannotated",
		TierUnknown:     "unknown",
	}
	for tier, str := range got {
		if !want[tier] {
			t.Fatalf("unexpected tier constant %q", tier)
		}
		if string(tier) != str {
			t.Fatalf("Tier constant %v must serialize as %q, got %q", tier, str, string(tier))
		}
	}
}
