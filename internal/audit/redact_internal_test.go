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

func TestSink_DeletedTornFileStopsBlocking(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/audit.jsonl"
	if err := os.WriteFile(path, []byte("{\"se"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewFileSink(path, 1, 5, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	ws := s.(*writerSink)
	ws.mu.Lock()
	ws.torn = true
	ws.openRepair = func() error { return repairTornPath(path) }
	ws.mu.Unlock()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]byte(`{"seq":2}`)); err != nil {
		t.Fatalf("a removed torn file leaves nothing to repair: %v", err)
	}
	_ = s.Close()
	raw, _ := os.ReadFile(path)
	if string(raw) != "{\"seq\":2}\n" {
		t.Fatalf("the writer creates a clean file: %q", raw)
	}
}
