package tokenseal

import (
	"encoding/base64"
	"testing"
)

// testIKM returns 32 deterministic bytes (seed, seed+1, ...) and their base64 form.
func testIKM(seed byte) ([]byte, string) {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i) + seed
	}
	return b, base64.StdEncoding.EncodeToString(b)
}

func mustKey(t *testing.T, seed byte) *Key {
	t.Helper()
	_, s := testIKM(seed)
	k, err := ParseKey(s)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	return k
}
