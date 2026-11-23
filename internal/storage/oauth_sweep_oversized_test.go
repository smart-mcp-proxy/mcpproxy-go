package storage

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// Spec 123 review: one plaintext row that cannot be sealed for a per-row
// reason (a record over tokenseal.MaxPlaintextLen) is skipped and left as it
// is; it must not fail the sweep for every other row.
func TestOAuthSweep_OversizedRowIsSkippedNotFatal(t *testing.T) {
	k := sealTestKey(t, 1)
	m := newSweepManager(t, k)

	const bigKey = "big_0123456789abcdef"
	big := []byte(`{"server_name":"` + bigKey + `","access_token":"` + strings.Repeat("x", 1100*1024) + `"}`)
	putRawOAuthValue(t, m.GetDB(), bigKey, big)

	const okKey = "ok_0123456789abcdef"
	ok, err := (&OAuthTokenRecord{ServerName: okKey, AccessToken: "a"}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	putRawOAuthValue(t, m.GetDB(), okKey, ok)

	n, err := m.SealPlaintextOAuthTokens(context.Background())
	if err != nil {
		t.Fatalf("an oversized row failed the whole sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("sealed %d rows, want 1", n)
	}
	if got := rawOAuthValue(t, m.GetDB(), bigKey); !bytes.Equal(got, big) {
		t.Fatal("the oversized row was changed")
	}
	if got := rawOAuthValue(t, m.GetDB(), okKey); bytes.Equal(got, ok) || !bytes.HasPrefix(got, []byte("enc:v1:")) {
		t.Fatal("the ordinary row was not sealed")
	}
}
