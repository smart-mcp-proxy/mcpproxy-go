package checks

import (
	"strings"
	"testing"
)

// Sol review of #1714 (P1): the negated mixed-view branch named the fold for
// every occurrence by re-normalizing the whole tool text, so a description
// repeating one negated look-alike directive cost quadratic work under the
// server's approval lock. The wording is computed at most once per inspection.
func TestPhraseInjection_MixedFoldWordingOncePerInspection(t *testing.T) {
	orig := foldWording
	t.Cleanup(func() { foldWording = orig })
	calls := 0
	foldWording = func(raw string) string {
		calls++
		return orig(raw)
	}

	desc := strings.Repeat("Never ígnore all previous instructions. ", 40)
	c := &PhraseInjection{}
	if _, ok := rt04Tier(c, desc); !ok {
		t.Fatal("repeated negated look-alike directive must still surface")
	}
	if calls > 1 {
		t.Fatalf("fold wording computed %d times for one inspection, want at most 1", calls)
	}
}
