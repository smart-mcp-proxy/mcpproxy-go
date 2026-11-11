package storage

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 T010 (FR-010, FR-011, FR-016): with a key configured every write
// path seals, every read path returns what it would for a plaintext row, the
// revision keeps increasing, and a write over a plaintext row sets the
// compaction-advised marker in the same transaction.

const (
	sealSrv = "srv"
	sealKey = "srv_0123456789abcdef"
)

// sealWriteOps is every oauth_tokens write method, each run against a
// record that already exists (seeded by sealSeed).
func sealWriteOps() map[string]func(db *BoltDB, rev uint64) error {
	zero := commitview.CredentialBinding{}
	must := func(ok bool, err error) error {
		if err == nil && !ok {
			return errNotWritten
		}
		return err
	}
	return map[string]func(db *BoltDB, rev uint64) error{
		"SaveOAuthToken": func(db *BoltDB, _ uint64) error {
			return db.SaveOAuthToken(&OAuthTokenRecord{ServerName: sealKey, AccessToken: "at-2", RefreshToken: "rt", ClientID: "cid", ClientSecret: "cs"})
		},
		"UpdateOAuthTokenIfBound": func(db *BoltDB, _ uint64) error {
			return must(db.UpdateOAuthTokenIfBound(sealKey, sealSrv, zero, func(rec *OAuthTokenRecord) error {
				rec.AccessToken = "at-2"
				return nil
			}))
		},
		"SaveOAuthClientIfBound/Always": func(db *BoltDB, _ uint64) error {
			return must(db.SaveOAuthClientIfBound(sealKey, sealSrv, zero, OAuthClientSaveAlways, "cid2", "cs2", 1, "http://127.0.0.1:1/cb"))
		},
		"SaveOAuthClientIfBound/IfUnsetOrSame": func(db *BoltDB, _ uint64) error {
			return must(db.SaveOAuthClientIfBound(sealKey, sealSrv, zero, OAuthClientSaveIfUnsetOrSame, "cid", "cs2", 1, "http://127.0.0.1:1/cb"))
		},
		"SaveOAuthClientIfBound/IfSameOrEmpty": func(db *BoltDB, _ uint64) error {
			return must(db.SaveOAuthClientIfBound(sealKey, sealSrv, zero, OAuthClientSaveIfSameOrEmpty, "cid", "cs2", 1, "http://127.0.0.1:1/cb"))
		},
		"UpdateOAuthToken": func(db *BoltDB, _ uint64) error {
			return db.UpdateOAuthToken(sealKey, func(rec *OAuthTokenRecord) error {
				rec.AccessToken = "at-2"
				return nil
			})
		},
		"UpdateOAuthClientCredentials": func(db *BoltDB, _ uint64) error {
			return db.UpdateOAuthClientCredentials(sealKey, "cid2", "cs2", 1, "http://127.0.0.1:1/cb")
		},
		"SaveOAuthClientCredentialsIfUnsetOrSame": func(db *BoltDB, _ uint64) error {
			return must(db.SaveOAuthClientCredentialsIfUnsetOrSame(sealKey, "cid", "cs2", 1, "http://127.0.0.1:1/cb"))
		},
		"SaveOAuthClientCredentialsIfSameOrEmpty": func(db *BoltDB, _ uint64) error {
			return must(db.SaveOAuthClientCredentialsIfSameOrEmpty(sealKey, "cid", "cs2", 1, "http://127.0.0.1:1/cb"))
		},
		"ClearOAuthClientCredentials": func(db *BoltDB, _ uint64) error {
			return db.ClearOAuthClientCredentials(sealKey)
		},
		"ClearOAuthClientCredentialsIf": func(db *BoltDB, _ uint64) error {
			return must(db.ClearOAuthClientCredentialsIf(sealKey, "cid", "rt"))
		},
		"ClearOAuthClientCredentialsIfClientID": func(db *BoltDB, _ uint64) error {
			return must(db.ClearOAuthClientCredentialsIfClientID(sealKey, "cid"))
		},
		"ClearOAuthClientIfBound": func(db *BoltDB, rev uint64) error {
			return must(db.ClearOAuthClientIfBound(sealKey, sealSrv, zero, rev))
		},
		"ClearOAuthClientIfBoundAndClientID": func(db *BoltDB, _ uint64) error {
			return must(db.ClearOAuthClientIfBoundAndClientID(sealKey, sealSrv, zero, "cid"))
		},
	}
}

type sealTestErr string

func (e sealTestErr) Error() string { return string(e) }

const errNotWritten = sealTestErr("the write reported that it did not write")

func sealSeed(t *testing.T, db *BoltDB) uint64 {
	t.Helper()
	rec := &OAuthTokenRecord{ServerName: sealKey, DisplayName: sealSrv, AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer", ClientID: "cid", ClientSecret: "cs"}
	if err := db.SaveOAuthToken(rec); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return rec.Revision
}

// openRaw decodes a stored envelope with k independently of the storage codec.
func openRaw(t *testing.T, k *tokenseal.Key, bucketKey string, v []byte) *OAuthTokenRecord {
	t.Helper()
	pt, err := k.Open(OAuthTokenBucket, bucketKey, v)
	if err != nil {
		t.Fatalf("open stored value: %v", err)
	}
	rec := &OAuthTokenRecord{}
	if err := json.Unmarshal(pt, rec); err != nil {
		t.Fatalf("stored plaintext is not a record: %v", err)
	}
	return rec
}

func TestOAuthSeal_EveryWriteSeals(t *testing.T) {
	k := sealTestKey(t, 1)
	for name, op := range sealWriteOps() {
		t.Run(name, func(t *testing.T) {
			db := newSealTestDB(t, k)
			rev := sealSeed(t, db)
			if shape, kid := tokenseal.Classify(rawOAuthValue(t, db.db, sealKey)); shape != tokenseal.ShapeSealedV1 || kid != k.ID() {
				t.Fatalf("seed write: shape %v kid %q, want sealed under %q", shape, kid, k.ID())
			}
			if err := op(db, rev); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			raw := rawOAuthValue(t, db.db, sealKey)
			shape, kid := tokenseal.Classify(raw)
			if shape != tokenseal.ShapeSealedV1 || kid != k.ID() {
				t.Fatalf("after %s: shape %v kid %q, want sealed under %q (value %.40q)", name, shape, kid, k.ID(), raw)
			}
			stored := openRaw(t, k, sealKey, raw)
			if stored.Revision <= rev {
				t.Fatalf("revision %d did not increase past %d", stored.Revision, rev)
			}
			got, err := db.GetOAuthToken(sealKey)
			if err != nil {
				t.Fatalf("GetOAuthToken: %v", err)
			}
			if !reflect.DeepEqual(normTimes(got), normTimes(stored)) {
				t.Fatalf("read %+v != stored %+v", got, stored)
			}
		})
	}
}

// normTimes strips monotonic clock readings so DeepEqual compares instants.
func normTimes(r *OAuthTokenRecord) OAuthTokenRecord {
	c := *r
	c.Created, c.Updated, c.ExpiresAt = c.Created.Round(0), c.Updated.Round(0), c.ExpiresAt.Round(0)
	return c
}

func TestOAuthSeal_ReadsMatchPlaintext(t *testing.T) {
	k := sealTestKey(t, 1)
	ts := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	rec := &OAuthTokenRecord{
		ServerName: sealKey, DisplayName: sealSrv, AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer",
		ExpiresAt: ts.Add(time.Hour), Scopes: []string{"a", "b"}, Created: ts, Updated: ts,
		ClientID: "cid", ClientSecret: "cs", CallbackPort: 4242, RedirectURI: "http://127.0.0.1:4242/cb", Revision: 7,
	}
	plain := newSealTestDB(t, nil)
	plainJSON, _ := rec.MarshalBinary()
	putRawOAuthValue(t, plain.db, sealKey, plainJSON)
	sealed := newSealTestDB(t, k)
	putRawOAuthValue(t, sealed.db, sealKey, sealedValue(t, k, sealKey, rec))

	type creds struct {
		ID, Secret, URI string
		Port            int
		Rev             uint64
	}
	reads := map[string]func(db *BoltDB) (any, error){
		"GetOAuthToken": func(db *BoltDB) (any, error) { return db.GetOAuthToken(sealKey) },
		"GetOAuthTokenBound": func(db *BoltDB) (any, error) {
			return db.GetOAuthTokenBound(sealKey, commitview.CredentialBinding{})
		},
		"GetOAuthClientCredentials": func(db *BoltDB) (any, error) {
			id, s, p, u, err := db.GetOAuthClientCredentials(sealKey)
			return creds{ID: id, Secret: s, Port: p, URI: u}, err
		},
		"GetOAuthClientCredentialsBound": func(db *BoltDB) (any, error) {
			id, s, p, u, r, err := db.GetOAuthClientCredentialsBound(sealKey, commitview.CredentialBinding{})
			return creds{ID: id, Secret: s, Port: p, URI: u, Rev: r}, err
		},
		"ListOAuthTokens": func(db *BoltDB) (any, error) { return db.ListOAuthTokens() },
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			want, err := read(plain)
			if err != nil {
				t.Fatalf("plaintext read: %v", err)
			}
			got, err := read(sealed)
			if err != nil {
				t.Fatalf("sealed read: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("sealed read %+v != plaintext read %+v", got, want)
			}
		})
	}
}

func TestOAuthSeal_CompactionMarker(t *testing.T) {
	k := sealTestKey(t, 1)
	update := func(db *BoltDB) {
		t.Helper()
		if err := db.UpdateOAuthToken(sealKey, func(rec *OAuthTokenRecord) error {
			rec.AccessToken = "at-new"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	plainRow, _ := (&OAuthTokenRecord{ServerName: sealKey, AccessToken: "at-old"}).MarshalBinary()

	t.Run("write over plaintext sets it", func(t *testing.T) {
		db := newSealTestDB(t, k)
		putRawOAuthValue(t, db.db, sealKey, plainRow)
		update(db)
		v := compactAdvised(t, db.db)
		if v == "" {
			t.Fatal("marker not set by a write that replaced a plaintext row")
		}
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			t.Fatalf("marker %q is not an RFC3339 time: %v", v, err)
		}
	})
	t.Run("write over sealed does not", func(t *testing.T) {
		db := newSealTestDB(t, k)
		update(db) // creates the row sealed
		update(db) // rewrites a sealed row
		if v := compactAdvised(t, db.db); v != "" {
			t.Fatalf("marker %q set although no plaintext was replaced", v)
		}
	})
	t.Run("marker write rolls back with the row", func(t *testing.T) {
		db := newSealTestDB(t, k)
		putRawOAuthValue(t, db.db, sealKey, plainRow)
		err := db.UpdateOAuthToken(sealKey, func(rec *OAuthTokenRecord) error { return errNotWritten })
		if err == nil {
			t.Fatal("mutate error swallowed")
		}
		if v := compactAdvised(t, db.db); v != "" {
			t.Fatalf("marker %q set by a write that did not commit", v)
		}
	})
	t.Run("no key never sets it", func(t *testing.T) {
		db := newSealTestDB(t, nil)
		putRawOAuthValue(t, db.db, sealKey, plainRow)
		update(db)
		if v := compactAdvised(t, db.db); v != "" {
			t.Fatalf("marker %q set without a key", v)
		}
	})
}
