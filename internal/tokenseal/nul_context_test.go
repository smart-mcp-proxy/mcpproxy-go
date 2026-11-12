package tokenseal

import (
	"errors"
	"testing"
)

// The AAD is NUL-separated, so (bucket, key) pairs containing NUL could
// collide. Seal and Open must refuse them.
func TestNULInContextRejected(t *testing.T) {
	k := mustKey(t, 0)
	if _, err := k.Seal("oauth_tokens", "x\x00y", []byte("{}")); err == nil {
		t.Fatal("Seal accepted a bucket key with NUL")
	}
	if _, err := k.Seal("oauth_tokens\x00x", "y", []byte("{}")); err == nil {
		t.Fatal("Seal accepted a bucket with NUL")
	}
	env, err := k.Seal("oauth_tokens", "x", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"oauth_tokens", "x\x00y"}, {"oauth_tokens\x00x", "y"}} {
		_, err := k.Open(c[0], c[1], env)
		var oe *OpenError
		if !errors.As(err, &oe) || oe.Reason != ReasonMalformed {
			t.Fatalf("Open(%q,%q) = %v, want malformed OpenError", c[0], c[1], err)
		}
	}
}
