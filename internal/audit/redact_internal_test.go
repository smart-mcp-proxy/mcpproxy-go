package audit

import (
	"os"
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

// The bound masks only the credential-shaped run, never the rest of a
// serialized record (a JSON quote ends it).
func TestMaskAllSpans_BoundStopsAtTheRunEnd(t *testing.T) {
	id := strings.Repeat("mcp_agt_", 60) + "0123456789abcdefghij"
	line := `{"request_id":"` + id + `","ts":"2026-09-17T10:00:00.000000001Z"}`
	got, _ := maskAllSpans(line)
	if !strings.Contains(got, `"ts":"2026-09-17T10:00:00.000000001Z"`) {
		t.Fatalf("masking must not run past the credential run: %q", got)
	}
}

// A retry repairs the torn file itself, even after it was renamed.
func TestSink_RetryRepairsTheHeldTornFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/audit.jsonl"
	if err := os.WriteFile(path, []byte("{\"se"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	s := newWriterSink(&scriptedWriter{})
	s.torn = true
	s.openRepair = func() error {
		if err := writeNewline(f); err != nil {
			return err
		}
		return f.Close()
	}
	renamed := dir + "/audit-old.jsonl"
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]byte(`{"seq":2}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(renamed)
	if string(raw) != "{\"se\n" {
		t.Fatalf("the renamed torn file is the one repaired: %q", raw)
	}
}

func TestMaskAllSpans_ScanContinuesAfterTheBound(t *testing.T) {
	s := strings.Repeat("mcp_agt_", 60) + "0123456789abcdefghij,mcp_cli_secondsecretvalue0123"
	got, _ := maskAllSpans(s)
	if strings.Contains(got, "secondsecretvalue") {
		t.Fatalf("a credential after the bounded run is still masked: %q", got)
	}
}
