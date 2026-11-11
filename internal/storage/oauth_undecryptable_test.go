package storage_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage/purgetest"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 T012 (FR-012, FR-013): against a row the configured key cannot
// open, every writer that starts from the stored record fails with the typed
// error and leaves the bytes unchanged; key-only deletes still delete; reads
// return the typed error, not "not found".

func undecTestKey(t *testing.T, seed byte) *tokenseal.Key {
	t.Helper()
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i) + seed
	}
	k, err := tokenseal.ParseKey(base64.StdEncoding.EncodeToString(b))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newKeyedManager(t *testing.T, k *tokenseal.Key) *storage.Manager {
	t.Helper()
	m, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar(), storage.WithOAuthTokenKey(k))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func rawGet(t *testing.T, m *storage.Manager, key string) []byte {
	t.Helper()
	var out []byte
	if err := m.GetDB().View(func(tx *bbolt.Tx) error {
		if v := tx.Bucket([]byte(storage.OAuthTokenBucket)).Get([]byte(key)); v != nil {
			out = append([]byte(nil), v...)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func rawPut(t *testing.T, m *storage.Manager, key string, v []byte) {
	t.Helper()
	if err := m.GetDB().Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(storage.OAuthTokenBucket)).Put([]byte(key), v)
	}); err != nil {
		t.Fatal(err)
	}
}

func sealFor(t *testing.T, k *tokenseal.Key, bucketKey string, rec *storage.OAuthTokenRecord) []byte {
	t.Helper()
	pt, err := rec.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	v, err := k.Seal(storage.OAuthTokenBucket, bucketKey, pt)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// undecVariant builds a manager and the undecryptable bytes to plant under
// bucketKey, with the reason and key ids the typed error must carry.
type undecVariant struct {
	name   string
	reason tokenseal.Reason
	setup  func(t *testing.T, bucketKey string) (m *storage.Manager, value []byte, rowKID, keyKID string)
}

func undecVariants() []undecVariant {
	rec := func(k string) *storage.OAuthTokenRecord {
		return &storage.OAuthTokenRecord{ServerName: k, DisplayName: "srv", AccessToken: "at", RefreshToken: "rt", ClientID: "cid", ClientSecret: "cs"}
	}
	return []undecVariant{
		{"no key", tokenseal.ReasonNoKey, func(t *testing.T, bk string) (*storage.Manager, []byte, string, string) {
			a := undecTestKey(t, 1)
			return newKeyedManager(t, nil), sealFor(t, a, bk, rec(bk)), a.ID(), ""
		}},
		{"other key", tokenseal.ReasonKeyMismatch, func(t *testing.T, bk string) (*storage.Manager, []byte, string, string) {
			a, b := undecTestKey(t, 1), undecTestKey(t, 50)
			return newKeyedManager(t, b), sealFor(t, a, bk, rec(bk)), a.ID(), b.ID()
		}},
		{"swapped envelope", tokenseal.ReasonAuthFailed, func(t *testing.T, bk string) (*storage.Manager, []byte, string, string) {
			a := undecTestKey(t, 1)
			return newKeyedManager(t, a), sealFor(t, a, "other_fedcba9876543210", rec(bk)), a.ID(), a.ID()
		}},
	}
}

func assertUndecryptable(t *testing.T, err error, bucketKey string, reason tokenseal.Reason, rowKID, keyKID string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected the typed undecryptable error, got nil")
	}
	if !errors.Is(err, storage.ErrOAuthTokenUndecryptableSentinel) {
		t.Fatalf("error %v is not ErrOAuthTokenUndecryptableSentinel", err)
	}
	var ue *storage.ErrOAuthTokenUndecryptable
	if !errors.As(err, &ue) {
		t.Fatalf("error %v is not *ErrOAuthTokenUndecryptable", err)
	}
	if ue.Key != bucketKey || ue.Reason != reason || ue.RowKID != rowKID || ue.KeyKID != keyKID {
		t.Fatalf("typed error %+v, want key %q reason %q row kid %q key kid %q", ue, bucketKey, reason, rowKID, keyKID)
	}
}

const undecSrv = "srv"

var undecKey = purgetest.OAuthKey(undecSrv)

func TestOAuthUndecryptable_WritersRefuse(t *testing.T) {
	zero := commitview.CredentialBinding{}
	type op func(db *storage.BoltDB) error
	ok := func(_ bool, err error) error { return err }
	writers := map[string]op{
		"SaveOAuthToken": func(db *storage.BoltDB) error {
			return db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: undecKey, AccessToken: "new"})
		},
		"UpdateOAuthTokenIfBound": func(db *storage.BoltDB) error {
			return ok(db.UpdateOAuthTokenIfBound(undecKey, undecSrv, zero, func(rec *storage.OAuthTokenRecord) error {
				rec.AccessToken = "new"
				return nil
			}))
		},
		"SaveOAuthClientIfBound/Always": func(db *storage.BoltDB) error {
			return ok(db.SaveOAuthClientIfBound(undecKey, undecSrv, zero, storage.OAuthClientSaveAlways, "c", "s", 1, "u"))
		},
		"SaveOAuthClientIfBound/IfUnsetOrSame": func(db *storage.BoltDB) error {
			return ok(db.SaveOAuthClientIfBound(undecKey, undecSrv, zero, storage.OAuthClientSaveIfUnsetOrSame, "c", "s", 1, "u"))
		},
		"SaveOAuthClientIfBound/IfSameOrEmpty": func(db *storage.BoltDB) error {
			return ok(db.SaveOAuthClientIfBound(undecKey, undecSrv, zero, storage.OAuthClientSaveIfSameOrEmpty, "c", "s", 1, "u"))
		},
		"DeleteOAuthTokenIfBound": func(db *storage.BoltDB) error {
			return ok(db.DeleteOAuthTokenIfBound(undecKey, undecSrv, zero, 1))
		},
		"ClearOAuthClientIfBound": func(db *storage.BoltDB) error {
			return ok(db.ClearOAuthClientIfBound(undecKey, undecSrv, zero, 1))
		},
		"ClearOAuthClientIfBoundAndClientID": func(db *storage.BoltDB) error {
			return ok(db.ClearOAuthClientIfBoundAndClientID(undecKey, undecSrv, zero, "cid"))
		},
		"UpdateOAuthToken": func(db *storage.BoltDB) error {
			return db.UpdateOAuthToken(undecKey, func(rec *storage.OAuthTokenRecord) error {
				rec.AccessToken = "new"
				return nil
			})
		},
		"UpdateOAuthClientCredentials": func(db *storage.BoltDB) error {
			return db.UpdateOAuthClientCredentials(undecKey, "c", "s", 1, "u")
		},
		"SaveOAuthClientCredentialsIfUnsetOrSame": func(db *storage.BoltDB) error {
			return ok(db.SaveOAuthClientCredentialsIfUnsetOrSame(undecKey, "c", "s", 1, "u"))
		},
		"SaveOAuthClientCredentialsIfSameOrEmpty": func(db *storage.BoltDB) error {
			return ok(db.SaveOAuthClientCredentialsIfSameOrEmpty(undecKey, "c", "s", 1, "u"))
		},
		"ClearOAuthClientCredentialsIf": func(db *storage.BoltDB) error {
			return ok(db.ClearOAuthClientCredentialsIf(undecKey, "cid", "rt"))
		},
		"ClearOAuthClientCredentialsIfClientID": func(db *storage.BoltDB) error {
			return ok(db.ClearOAuthClientCredentialsIfClientID(undecKey, "cid"))
		},
		"ClearOAuthClientCredentials": func(db *storage.BoltDB) error {
			return db.ClearOAuthClientCredentials(undecKey)
		},
	}
	for _, v := range undecVariants() {
		for name, w := range writers {
			t.Run(v.name+"/"+name, func(t *testing.T) {
				m, val, rowKID, keyKID := v.setup(t, undecKey)
				seedRows(t, m, 1, undecSrv)
				rawPut(t, m, undecKey, val)
				err := w(m.GetBoltDB())
				assertUndecryptable(t, err, undecKey, v.reason, rowKID, keyKID)
				if got := rawGet(t, m, undecKey); !bytes.Equal(got, val) {
					t.Fatalf("%s changed the undecryptable row", name)
				}
			})
		}
	}
}

func TestOAuthUndecryptable_ReadsReturnTypedError(t *testing.T) {
	zero := commitview.CredentialBinding{}
	reads := map[string]func(m *storage.Manager) error{
		"Manager.GetOAuthToken": func(m *storage.Manager) error { _, err := m.GetOAuthToken(undecKey); return err },
		"GetOAuthToken":         func(m *storage.Manager) error { _, err := m.GetBoltDB().GetOAuthToken(undecKey); return err },
		"GetOAuthTokenBound": func(m *storage.Manager) error {
			_, err := m.GetBoltDB().GetOAuthTokenBound(undecKey, zero)
			return err
		},
		"GetOAuthClientCredentials": func(m *storage.Manager) error {
			_, _, _, _, err := m.GetBoltDB().GetOAuthClientCredentials(undecKey)
			return err
		},
		"GetOAuthClientCredentialsBound": func(m *storage.Manager) error {
			_, _, _, _, _, err := m.GetBoltDB().GetOAuthClientCredentialsBound(undecKey, zero)
			return err
		},
	}
	for _, v := range undecVariants() {
		for name, r := range reads {
			t.Run(v.name+"/"+name, func(t *testing.T) {
				m, val, rowKID, keyKID := v.setup(t, undecKey)
				rawPut(t, m, undecKey, val)
				assertUndecryptable(t, r(m), undecKey, v.reason, rowKID, keyKID)
			})
		}
	}
}

func TestOAuthUndecryptable_KeyOnlyDeletesStillDelete(t *testing.T) {
	deletes := map[string]func(t *testing.T, m *storage.Manager) error{
		"DeleteOAuthToken": func(_ *testing.T, m *storage.Manager) error { return m.GetBoltDB().DeleteOAuthToken(undecKey) },
		"ClearOAuthTokenForStandaloneLogout": func(_ *testing.T, m *storage.Manager) error {
			return m.GetBoltDB().ClearOAuthTokenForStandaloneLogout(undecKey)
		},
		"ClearOAuthState": func(_ *testing.T, m *storage.Manager) error { return m.ClearOAuthState(undecSrv) },
		"commit OAuthClears": func(_ *testing.T, m *storage.Manager) error {
			return m.ApplyServerCommit(&storage.ServerCommitBatch{
				Generation:  2,
				OAuthClears: []string{undecSrv},
				Saves:       []storage.ServerSave{{Config: purgeEntry(undecSrv), Stamp: commitview.ServerGen{Gen: 2, Incarnation: 1, OAuthEpoch: 2}}},
			})
		},
		"purge (commit Removes)": func(t *testing.T, m *storage.Manager) error {
			err := m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: 2, Removes: []string{undecSrv}})
			if err == nil {
				purgetest.AssertPurged(t, m, undecSrv, "ApplyServerCommit.Removes")
			}
			return err
		},
	}
	for _, v := range undecVariants() {
		for name, del := range deletes {
			t.Run(v.name+"/"+name, func(t *testing.T) {
				m, val, _, _ := v.setup(t, undecKey)
				seedRows(t, m, 1, undecSrv)
				purgetest.Seed(t, m, undecSrv)
				rawPut(t, m, undecKey, val)
				if err := del(t, m); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if got := rawGet(t, m, undecKey); got != nil {
					t.Fatalf("%s left the undecryptable row in place", name)
				}
			})
		}
	}
}
