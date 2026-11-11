package tokenseal

import (
	"errors"
	"testing"
)

// Seal sizes two buffers from the plaintext length. The length is bounded so
// that arithmetic cannot overflow and a corrupt caller cannot ask for an
// unbounded allocation: an OAuth record is a few kilobytes of JSON.
func TestSealRejectsOversizedPlaintext(t *testing.T) {
	k := mustKey(t, 1)
	if _, err := k.Seal("oauth_tokens", "srv", make([]byte, MaxPlaintextLen)); err != nil {
		t.Fatalf("Seal at the limit: %v", err)
	}
	_, err := k.Seal("oauth_tokens", "srv", make([]byte, MaxPlaintextLen+1))
	if !errors.Is(err, ErrPlaintextTooLarge) {
		t.Fatalf("Seal over the limit: got %v, want ErrPlaintextTooLarge", err)
	}
}
