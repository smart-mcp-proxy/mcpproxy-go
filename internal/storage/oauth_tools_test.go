package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 PR-3 (FR-032, T051): the storage half of the offline `auth tokens`
// commands: a reporting and dry-run sweep, the decrypt rewrite, and the
// compaction-advice accessor.

func readRawOAuthValue(t *testing.T, db *bbolt.DB, key string) []byte {
	t.Helper()
	var out []byte
	if err := db.View(func(tx *bbolt.Tx) error {
		out = append([]byte(nil), tx.Bucket([]byte(OAuthTokenBucket)).Get([]byte(key))...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOAuthKeyServerName(t *testing.T) {
	cases := map[string]string{
		"github_0123456789abcdef": "github",
		"my_srv_0123456789abcdef": "my_srv",
		"nohash":                  "",
		"x_short":                 "",
		"_0123456789abcdef":       "",
	}
	for in, want := range cases {
		if got := OAuthKeyServerName(in); got != want {
			t.Errorf("OAuthKeyServerName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOAuthSealReport_SkippedAndDryRun(t *testing.T) {
	k := sealTestKey(t, 1)
	m := newSweepManager(t, k)
	db := m.GetBoltDB()
	good, _ := (&OAuthTokenRecord{ServerName: "good", AccessToken: "at"}).MarshalBinary()
	putRawOAuthValue(t, m.GetDB(), "good_0123456789abcdef", good)
	putRawOAuthValue(t, m.GetDB(), "bad_0123456789abcdef", []byte("{broken"))
	huge, _ := (&OAuthTokenRecord{ServerName: "huge", AccessToken: strings.Repeat("a", tokenseal.MaxPlaintextLen+10)}).MarshalBinary()
	putRawOAuthValue(t, m.GetDB(), "huge_0123456789abcdef", huge)

	rep, err := db.SealOAuthTokens(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sealed != 1 {
		t.Fatalf("dry run would seal %d, want 1", rep.Sealed)
	}
	reasons := map[string]string{}
	for _, s := range rep.Skipped {
		reasons[s.BucketKey] = s.Reason
	}
	if reasons["huge_0123456789abcdef"] != "too_large" || reasons["bad_0123456789abcdef"] != "malformed" || len(reasons) != 2 {
		t.Fatalf("skipped = %v", reasons)
	}
	if c := classCounts(t, m); c[OAuthRowPlaintext] != 2 || c[OAuthRowSealed] != 0 {
		t.Fatalf("a dry run changed rows: %v", c)
	}
	if v := compactAdvised(t, m.GetDB()); v != "" {
		t.Fatalf("a dry run set the compaction marker: %q", v)
	}

	rep, err = db.SealOAuthTokens(context.Background(), false)
	if err != nil || rep.Sealed != 1 {
		t.Fatalf("real run = %+v, %v", rep, err)
	}
	if c := classCounts(t, m); c[OAuthRowSealed] != 1 || c[OAuthRowPlaintext] != 1 {
		t.Fatalf("after real run: %v", c)
	}
	if compactAdvised(t, m.GetDB()) == "" {
		t.Fatal("sealing a plaintext row must set the marker")
	}
}

func TestOAuthDecrypt_RewritesSealedRowsAsPlaintextInOneTx(t *testing.T) {
	k := sealTestKey(t, 1)
	m := newSweepManager(t, k)
	f := seedSweep(t, m, k)
	if _, err := m.SealPlaintextOAuthTokens(context.Background()); err != nil {
		t.Fatal(err)
	}
	marker := compactAdvised(t, m.GetDB())
	if marker == "" {
		t.Fatal("seed: marker not set")
	}

	rep, err := m.DecryptOAuthTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// every seeded plaintext row plus the one sealed under k
	if want := len(f.plainKeys) + 1; rep.Decrypted != want {
		t.Fatalf("decrypted %d, want %d", rep.Decrypted, want)
	}
	rows, err := m.ListOAuthTokenRows()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		switch r.BucketKey {
		case "otherkey_0123456789abcdef", "badjson_0123456789abcdef":
			if r.Class != OAuthRowUndecryptable {
				t.Errorf("%s class = %s", r.BucketKey, r.Class)
			}
		default:
			if r.Class != OAuthRowPlaintext {
				t.Errorf("%s class = %s, want plaintext", r.BucketKey, r.Class)
			}
		}
	}
	// the downgrade simulation: every converted row decodes with plain json
	for _, key := range f.plainKeys {
		raw := readRawOAuthValue(t, m.GetDB(), key)
		var rec OAuthTokenRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatalf("%s does not decode with plain json: %v", key, err)
		}
		if !strings.HasPrefix(rec.AccessToken, "at-") {
			t.Fatalf("%s lost its token", key)
		}
	}
	// untouched rows keep their exact bytes
	for key, want := range f.untouched {
		if key == "sealed_0123456789abcdef" {
			continue
		}
		if got := readRawOAuthValue(t, m.GetDB(), key); string(got) != string(want) {
			t.Errorf("%s was modified", key)
		}
	}
	// decrypt does not touch the marker (it frees ciphertext, not plaintext)
	if got := compactAdvised(t, m.GetDB()); got != marker {
		t.Errorf("marker changed from %q to %q", marker, got)
	}
}

func TestOAuthDecrypt_NoKeyIsAnError(t *testing.T) {
	m := newSweepManager(t, nil)
	if _, err := m.DecryptOAuthTokens(context.Background()); err == nil {
		t.Fatal("decrypt without a key must fail")
	}
}

func TestOAuthDecrypt_RollsBackOnWriteFailure(t *testing.T) {
	k := sealTestKey(t, 1)
	m := newSweepManager(t, k)
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("s%d_0123456789abcdef", i)
		putRawOAuthValue(t, m.GetDB(), key, sealedValue(t, k, key, &OAuthTokenRecord{ServerName: key, AccessToken: "a"}))
	}
	calls := 0
	putOAuthRecordHook = func(string) error {
		calls++
		if calls == 2 {
			return fmt.Errorf("boom")
		}
		return nil
	}
	t.Cleanup(func() { putOAuthRecordHook = nil })
	if _, err := m.DecryptOAuthTokens(context.Background()); err == nil {
		t.Fatal("expected the injected failure")
	}
	if c := classCounts(t, m); c[OAuthRowSealed] != 3 {
		t.Fatalf("partial decrypt survived the rollback: %v", c)
	}
}

func TestOAuthCompactAdvised_Accessor(t *testing.T) {
	k := sealTestKey(t, 1)
	m := newSweepManager(t, k)
	if on, err := m.OAuthCompactAdvised(); err != nil || on {
		t.Fatalf("fresh db: %v, %v", on, err)
	}
	plain, _ := (&OAuthTokenRecord{ServerName: "a", AccessToken: "x"}).MarshalBinary()
	putRawOAuthValue(t, m.GetDB(), "a_0123456789abcdef", plain)
	if _, err := m.SealPlaintextOAuthTokens(context.Background()); err != nil {
		t.Fatal(err)
	}
	if on, err := m.OAuthCompactAdvised(); err != nil || !on {
		t.Fatalf("after sweep: %v, %v", on, err)
	}
}
