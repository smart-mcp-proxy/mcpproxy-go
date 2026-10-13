//go:build server

package broker

import (
	"errors"
	"testing"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// Spec 107 FR-033 residual: the retired IdP subject-token writer persisted a
// user's IdP access + offline refresh token under the BARE userID (no colon).
// Nothing on this release reads, lists or deletes those rows through any door
// — List seeks "<userID>:" and Delete needs a resolved server — so an upgraded
// deployment that had store_idp_tokens:true would keep them at rest forever.
// PurgeLegacyIDPSubjectTokens is the one-shot sweep setup runs at boot.

// seedLegacySubjectRow writes an opaque row under a bare userID straight into
// the bucket: the sweep must not need to decrypt anything, so the row is not
// even ciphertext.
func seedLegacySubjectRow(t *testing.T, db *bbolt.DB, userID string) {
	t.Helper()
	if err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(credentialBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(userID), []byte("legacy-opaque-ciphertext"))
	}); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
}

func bucketKeys(t *testing.T, db *bbolt.DB) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	if err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(credentialBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			keys[string(k)] = true
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return keys
}

// TestPurgeLegacyIDPSubjectTokens_RemovesOnlyBareUserIDRows: every bare-userID
// row goes, every "<userID>:<serverKey>" upstream credential stays readable.
func TestPurgeLegacyIDPSubjectTokens_RemovesOnlyBareUserIDRows(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db, newTestKey(t))

	if err := store.Put("alice", "srv_1234", sampleCred()); err != nil {
		t.Fatalf("Put upstream: %v", err)
	}
	seedLegacySubjectRow(t, db, "alice")
	seedLegacySubjectRow(t, db, "bob")

	// Positive control on the fixture: three rows, two of them legacy.
	if keys := bucketKeys(t, db); len(keys) != 3 || !keys["alice"] || !keys["bob"] || !keys["alice:srv_1234"] {
		t.Fatalf("fixture keys wrong: %v", keys)
	}

	n, err := store.PurgeLegacyIDPSubjectTokens()
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 2 {
		t.Errorf("purged %d rows, want 2", n)
	}

	keys := bucketKeys(t, db)
	if keys["alice"] || keys["bob"] {
		t.Errorf("legacy rows survived the sweep: %v", keys)
	}
	if !keys["alice:srv_1234"] {
		t.Errorf("upstream credential was swept: %v", keys)
	}
	if got, err := store.Get("alice", "srv_1234"); err != nil || got.AccessToken != sampleCred().AccessToken {
		t.Errorf("upstream credential must still decrypt after the sweep: %v %+v", err, got)
	}

	// Idempotent: a second boot finds nothing.
	n, err = store.PurgeLegacyIDPSubjectTokens()
	if err != nil || n != 0 {
		t.Errorf("second sweep: n=%d err=%v, want 0 nil", n, err)
	}
}

// TestPurgeLegacyIDPSubjectTokens_RunsWithoutAnEncryptionKey: the rows are
// deleted by key, never decrypted, so a deployment that dropped its
// MCPPROXY_CRED_KEY (store disabled) is still cleaned.
func TestPurgeLegacyIDPSubjectTokens_RunsWithoutAnEncryptionKey(t *testing.T) {
	db := openTestDB(t)
	seedLegacySubjectRow(t, db, "alice")

	store, err := NewBBoltAESStore(db, "", zap.NewNop())
	if err != nil {
		t.Fatalf("NewBBoltAESStore: %v", err)
	}
	if store.Enabled() {
		t.Fatal("fixture: store must be disabled without a key")
	}
	if _, gerr := store.Get("alice", ""); !errors.Is(gerr, ErrStoreDisabled) {
		t.Fatalf("fixture: disabled store must refuse reads, got %v", gerr)
	}

	n, err := store.PurgeLegacyIDPSubjectTokens()
	if err != nil {
		t.Fatalf("purge on disabled store: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d rows, want 1", n)
	}
	if keys := bucketKeys(t, db); keys["alice"] {
		t.Errorf("legacy row survived: %v", keys)
	}
}

// TestPurgeLegacyIDPSubjectTokens_NoBucketIsNoop: a fresh database (no store
// ever enabled) has no bucket and nothing to sweep.
func TestPurgeLegacyIDPSubjectTokens_NoBucketIsNoop(t *testing.T) {
	db := openTestDB(t)
	store, err := NewBBoltAESStore(db, "", zap.NewNop())
	if err != nil {
		t.Fatalf("NewBBoltAESStore: %v", err)
	}
	n, err := store.PurgeLegacyIDPSubjectTokens()
	if err != nil || n != 0 {
		t.Errorf("n=%d err=%v, want 0 nil", n, err)
	}
}
