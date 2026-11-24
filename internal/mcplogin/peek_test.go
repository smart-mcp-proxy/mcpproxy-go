package mcplogin

import "testing"

// PeekCode lets the token endpoint compare the request's origin with the
// stored issuer BEFORE the single-use code is consumed (Spec 132): a mismatch
// must leave the code usable.
func TestPeekCode(t *testing.T) {
	s, clk := newTestStore()
	r := mustCreate(t, s, "cursor")
	code, err := s.Approve(r.ID, Decision{ClientID: "cursor"})
	if err != nil {
		t.Fatal(err)
	}

	got, ok := s.PeekCode(code)
	if !ok || got.ID != r.ID || got.Issuer != r.Issuer || got.Status != StatusApproved {
		t.Fatalf("peek of a live code: %+v ok=%v", got, ok)
	}
	if _, ok := s.PeekCode("nope"); ok {
		t.Fatal("an unknown code must not peek")
	}
	// Peeking consumes nothing.
	if _, _, err := s.Consume(code, func(Request) (string, error) { return "g1", nil }); err != nil {
		t.Fatalf("consume after peek: %v", err)
	}
	if _, ok := s.PeekCode(code); ok {
		t.Fatal("a consumed code must not peek")
	}

	r2 := mustCreate(t, s, "")
	code2, _ := s.Approve(r2.ID, Decision{ClientID: "c"})
	clk.advance(CodeLifetime)
	if _, ok := s.PeekCode(code2); ok {
		t.Fatal("an expired code must not peek")
	}

	r3 := mustCreate(t, s, "")
	code3, _ := s.Approve(r3.ID, Decision{ClientID: "c"})
	s.CancelAll()
	if _, ok := s.PeekCode(code3); ok {
		t.Fatal("a cancelled request's code must not peek")
	}
}
