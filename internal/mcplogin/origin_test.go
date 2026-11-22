package mcplogin

import (
	"errors"
	"testing"
)

// The issuer rides on the code for its whole life: a replay on another origin
// is refused before replay handling (no grant id comes back to revoke).
func TestConsumeAt_OriginIsCheckedBeforeConsumptionAndReplay(t *testing.T) {
	s, _ := newTestStore()
	r := mustCreate(t, s, "cursor")
	code, err := s.Approve(r.ID, Decision{ClientID: "cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ConsumeAt(code, "http://localhost:8080", func(Request) (string, error) { return "g", nil }); !errors.Is(err, ErrCodeOriginMismatch) {
		t.Fatalf("wrong origin on a live code: %v", err)
	}
	if _, g, err := s.ConsumeAt(code, r.Issuer, func(Request) (string, error) { return "g1", nil }); err != nil || g != "g1" {
		t.Fatalf("the code is still usable on its own origin: %v %q", err, g)
	}
	// Replay: the right origin reports the grant, another origin learns nothing.
	if _, g, err := s.ConsumeAt(code, "http://localhost:8080", nil); !errors.Is(err, ErrCodeOriginMismatch) || g != "" {
		t.Fatalf("replay on another origin: %v %q", err, g)
	}
	if _, g, err := s.ConsumeAt(code, r.Issuer, nil); !errors.Is(err, ErrCodeReused) || g != "g1" {
		t.Fatalf("replay on the same origin: %v %q", err, g)
	}
}

func TestCancelAllReport(t *testing.T) {
	s, _ := newTestStore()
	a := mustCreate(t, s, "")
	b := mustCreate(t, s, "")
	if _, err := s.Approve(b.ID, Decision{ClientID: "c"}); err != nil {
		t.Fatal(err)
	}
	ids, n := s.CancelAllReport()
	if n != 2 || len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("ids=%v n=%d", ids, n)
	}
	if ids, n = s.CancelAllReport(); n != 0 || len(ids) != 0 {
		t.Fatal("second call finds nothing")
	}
}
