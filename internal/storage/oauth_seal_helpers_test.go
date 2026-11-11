package storage

import (
	"encoding/base64"
	"testing"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 Phase 2 test helpers (package-internal tests).

// sealTestKey returns a deterministic tokenseal key: 32 bytes seed, seed+1, ...
func sealTestKey(t *testing.T, seed byte) *tokenseal.Key {
	t.Helper()
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i) + seed
	}
	k, err := tokenseal.ParseKey(base64.StdEncoding.EncodeToString(b))
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	return k
}

// newSealTestDB opens a fresh database with k as its OAuth token key (nil =
// encryption off).
func newSealTestDB(t *testing.T, k *tokenseal.Key) *BoltDB {
	t.Helper()
	return openSealTestDB(t, t.TempDir(), k)
}

func openSealTestDB(t *testing.T, dir string, k *tokenseal.Key) *BoltDB {
	t.Helper()
	db, err := NewBoltDB(dir, zap.NewNop().Sugar(), WithOAuthTokenKey(k))
	if err != nil {
		t.Fatalf("NewBoltDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rawOAuthValue returns a copy of the stored bytes under key (nil if absent).
func rawOAuthValue(t *testing.T, db *bbolt.DB, key string) []byte {
	t.Helper()
	var out []byte
	if err := db.View(func(tx *bbolt.Tx) error {
		if v := tx.Bucket([]byte(OAuthTokenBucket)).Get([]byte(key)); v != nil {
			out = append([]byte(nil), v...)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// putRawOAuthValue stores val under key bypassing the codec (seeding only).
func putRawOAuthValue(t *testing.T, db *bbolt.DB, key string, val []byte) {
	t.Helper()
	if err := db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(OAuthTokenBucket)).Put([]byte(key), val)
	}); err != nil {
		t.Fatal(err)
	}
}

// sealedValue seals rec for bucketKey under k, as the codec would.
func sealedValue(t *testing.T, k *tokenseal.Key, bucketKey string, rec *OAuthTokenRecord) []byte {
	t.Helper()
	pt, err := rec.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	v, err := k.Seal(OAuthTokenBucket, bucketKey, pt)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// compactAdvised returns the meta marker value ("" when unset).
func compactAdvised(t *testing.T, db *bbolt.DB) string {
	t.Helper()
	var out string
	if err := db.View(func(tx *bbolt.Tx) error {
		if m := tx.Bucket([]byte(MetaBucket)); m != nil {
			out = string(m.Get([]byte(OAuthSealCompactAdvisedKey)))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func clearCompactAdvised(t *testing.T, db *bbolt.DB) {
	t.Helper()
	if err := db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(MetaBucket)).Delete([]byte(OAuthSealCompactAdvisedKey))
	}); err != nil {
		t.Fatal(err)
	}
}
