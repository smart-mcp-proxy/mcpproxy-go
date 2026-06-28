package detect

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClampConfidence(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{-0.5, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {1.5, 1}, {42, 1},
	}
	for _, c := range cases {
		if got := ClampConfidence(c.in); got != c.want {
			t.Errorf("ClampConfidence(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCapEvidence(t *testing.T) {
	if got := CapEvidence("hello world"); got != "hello world" {
		t.Errorf("plain ascii changed: %q", got)
	}
	// Control char escaped render-safe to a visible \uXXXX form.
	if got := CapEvidence("a\x00b"); got != `a\u0000b` {
		t.Errorf("CapEvidence control = %q, want %q", got, `a\u0000b`)
	}
	// Zero-width escaped (made visible), not silently dropped — evidence must
	// reveal the smuggling.
	if got := CapEvidence("a\u200bb"); got != `a\u200bb` {
		t.Errorf("CapEvidence zero-width = %q, want %q", got, `a\u200bb`)
	}
	// Truncation: capped to MaxEvidenceLen runes + ellipsis.
	long := strings.Repeat("x", MaxEvidenceLen+50)
	got := CapEvidence(long)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected truncated evidence to end with ellipsis")
	}
	if n := utf8.RuneCountInString(got); n != MaxEvidenceLen+1 {
		t.Errorf("truncated rune count = %d, want %d", n, MaxEvidenceLen+1)
	}
}

func TestTierString(t *testing.T) {
	if TierHard.String() != "hard" {
		t.Errorf("TierHard.String() = %q", TierHard.String())
	}
	if TierSoft.String() != "soft" {
		t.Errorf("TierSoft.String() = %q", TierSoft.String())
	}
}

func TestNewRegistryView(t *testing.T) {
	tools := []ToolView{
		{Server: "a", Name: "search", Description: "IGNORE Previous"},
		{Server: "b", Name: "search", Description: "another"},
		{Server: "a", Name: "delete", Description: "removes things"},
	}
	reg := NewRegistryView(tools)

	if len(reg.Tools) != 3 {
		t.Fatalf("Tools len = %d, want 3", len(reg.Tools))
	}
	// Collision: same name across two servers.
	if got := len(reg.ToolsByName["search"]); got != 2 {
		t.Errorf("ToolsByName[search] len = %d, want 2", got)
	}
	if _, ok := reg.ToolNames["delete"]; !ok {
		t.Errorf("ToolNames missing 'delete'")
	}
	if _, ok := reg.ToolNames["missing"]; ok {
		t.Errorf("ToolNames has phantom 'missing'")
	}
	// NormalizedText precomputed once per tool.
	if reg.Tools[0].NormalizedText != "ignore previous" {
		t.Errorf("NormalizedText = %q, want %q", reg.Tools[0].NormalizedText, "ignore previous")
	}
	// Deterministic collision ordering follows input order.
	if reg.ToolsByName["search"][0].Server != "a" || reg.ToolsByName["search"][1].Server != "b" {
		t.Errorf("collision order not stable: %+v", reg.ToolsByName["search"])
	}
}
