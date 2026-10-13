package config

import (
	"encoding/hex"
	"regexp"
	"testing"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestGenerateAPIKey_Shape pins the generated key to 32 random bytes rendered
// as 64 lowercase hex characters, and pins out the time-based fallback the
// function used to carry (`mcpproxy_<unixnano>`). That branch was unreachable
// — crypto/rand.Read is documented never to return an error since Go 1.24 —
// but nothing in the code said so, and a reader could not tell a predictable
// key was impossible. The shape assertion keeps it impossible.
func TestGenerateAPIKey_Shape(t *testing.T) {
	key := generateAPIKey()

	if !hex64.MatchString(key) {
		t.Fatalf("generateAPIKey() = %q; want 64 lowercase hex characters", key)
	}
	raw, err := hex.DecodeString(key)
	if err != nil {
		t.Fatalf("generateAPIKey() = %q is not hex: %v", key, err)
	}
	if len(raw) != 32 {
		t.Errorf("generateAPIKey() decodes to %d bytes; want 32 (256 bits)", len(raw))
	}
}

// TestGenerateAPIKey_Unique is the cheap liveness check on the entropy source:
// a deterministic fallback would make successive keys equal (or near-equal).
func TestGenerateAPIKey_Unique(t *testing.T) {
	seen := make(map[string]struct{}, 16)
	for i := 0; i < 16; i++ {
		key := generateAPIKey()
		if _, dup := seen[key]; dup {
			t.Fatalf("generateAPIKey() returned a duplicate key on iteration %d: %q", i, key)
		}
		seen[key] = struct{}{}
	}
}
