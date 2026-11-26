package audit

import (
	"strings"
	"testing"
)

// FR-004: a string that matches none of the Spec 133 patterns is masked
// byte-identically to the Spec 107 pass.
func TestApplyMasking_LegacyBytesUnchanged(t *testing.T) {
	for _, s := range []string{
		"ghp_0123456789abcdefghijsk-0123456789abcdefghij",
		"Bearer abc.def AKIAABCDEFGHIJ",
		"eyJa.eyJb.ceyJc.eyJd.e",
		"plain-name",
	} {
		got, gotHit := applyMasking(s)
		want, wantHit := applyLegacyMasking(s)
		if got != want || gotHit != wantHit {
			t.Fatalf("%q: got %q, legacy %q", s, got, want)
		}
	}
}

// admin_action labels mask overlapping tokens of one shape too.
func TestMaskAllSpans_BackToBackJWTs(t *testing.T) {
	s := "eyJhbGciOi.eyJzdWIiOiJ4In0.c2lnbmF0dXJleyJhbGciOi.eyJzZWNvbmRQYXlsb2Fk.c2Vjb25kU2ln"
	got, hit := maskAllSpans(s)
	if !hit || strings.Contains(got, "eyJzZWNvbmRQYXlsb2Fk") || strings.Contains(got, "c2Vjb25kU2ln") {
		t.Fatalf("got %q", got)
	}
}

// Past the rescan bound the remainder is masked whole: no recognisable tail.
func TestMaskAllSpans_BoundMasksTheRemainder(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxOverlapRescans+5; i++ {
		b.WriteString("eyJh.eyJp.c")
	}
	b.WriteString("eyJhbGciOi.eyJsYXN0UGF5bG9hZA.bGFzdFNpZw")
	got, hit := maskAllSpans(b.String())
	if !hit || strings.Contains(got, "eyJsYXN0UGF5bG9hZA") || strings.Contains(got, "bGFzdFNpZw") {
		t.Fatalf("got %q", got)
	}
}
