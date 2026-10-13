package cache

import (
	"encoding/json"
	"strings"
	"testing"
)

// Spec 112 FR-017: the forwarded-set fact round-trips through the store and the
// gated read, and is absent (empty) for a call that forwarded nothing.
func TestStoreAsForwarded_RoundTrip(t *testing.T) {
	m := newTestManager(t)
	admin := Authorization{CallerKind: CallerKindAnonymous}
	content := `[{"a":1},{"a":2}]`

	if err := m.StoreAsForwarded("k1", "srv:tool", nil, content, "", 2, admin, "digest-abc", "srv"); err != nil {
		t.Fatal(err)
	}
	if err := m.StoreAs("k2", "srv:tool", nil, content, "", 2, admin); err != nil {
		t.Fatal(err)
	}

	r1, err := m.GetRecordsAs("k1", 0, 10, admin)
	if err != nil {
		t.Fatal(err)
	}
	if r1.ForwardedDigest != "digest-abc" || r1.ForwardedServer != "srv" {
		t.Fatalf("fact lost: %q %q", r1.ForwardedDigest, r1.ForwardedServer)
	}
	r2, err := m.GetRecordsAs("k2", 0, 10, admin)
	if err != nil {
		t.Fatal(err)
	}
	if r2.ForwardedDigest != "" || r2.ForwardedServer != "" {
		t.Fatalf("unforwarded entry carries a fact: %q %q", r2.ForwardedDigest, r2.ForwardedServer)
	}
}

func TestReadCacheResponseNeverSerializesForwardedFact(t *testing.T) {
	r := &ReadCacheResponse{ForwardedDigest: "digest-abc", ForwardedServer: "srv"}
	b, err := jsonMarshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "digest-abc") || strings.Contains(string(b), "forwarded") {
		t.Fatalf("the fact reached the wire: %s", b)
	}
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
