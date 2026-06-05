//go:build server

package broker

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// newTestKey returns a freshly generated, base64-encoded 32-byte AES-256 key.
func newTestKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// openTestDB opens a throwaway BBolt database in the test's temp dir.
func openTestDB(t *testing.T) *bbolt.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "creds.db")
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("open bbolt: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestStore(t *testing.T, db *bbolt.DB, key string) *BBoltAESStore {
	t.Helper()
	store, err := NewBBoltAESStore(db, key, zap.NewNop())
	if err != nil {
		t.Fatalf("NewBBoltAESStore: %v", err)
	}
	return store
}

func sampleCred() *UpstreamCredential {
	return &UpstreamCredential{
		Type:         "oauth2",
		AccessToken:  "at-secret-12345",
		RefreshToken: "rt-secret-67890",
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		Scopes:       []string{"read", "write"},
		TokenType:    "Bearer",
		Audience:     "https://api.example.com",
		ObtainedVia:  "token_exchange",
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}
}

func TestUpstreamCredential_ExpiryHelpers(t *testing.T) {
	// Zero ExpiresAt means never-expiring (matches oauth convention).
	never := &UpstreamCredential{}
	if never.IsExpired() {
		t.Errorf("zero ExpiresAt should not be expired")
	}
	if !never.IsValid() {
		t.Errorf("zero ExpiresAt should be valid")
	}

	past := &UpstreamCredential{ExpiresAt: time.Now().Add(-time.Minute)}
	if !past.IsExpired() {
		t.Errorf("past ExpiresAt should be expired")
	}
	if past.IsValid() {
		t.Errorf("past ExpiresAt should not be valid")
	}

	future := &UpstreamCredential{ExpiresAt: time.Now().Add(time.Hour)}
	if future.IsExpired() {
		t.Errorf("future ExpiresAt should not be expired")
	}

	// ExpiresWithin: true when within grace, false otherwise; never-expiring is always false.
	soon := &UpstreamCredential{ExpiresAt: time.Now().Add(time.Minute)}
	if !soon.ExpiresWithin(5 * time.Minute) {
		t.Errorf("token expiring in 1m should be within 5m grace")
	}
	if future.ExpiresWithin(5 * time.Minute) {
		t.Errorf("token expiring in 1h should not be within 5m grace")
	}
	if never.ExpiresWithin(5 * time.Minute) {
		t.Errorf("never-expiring token should not be within any grace window")
	}
}

func TestBBoltAESStore_EncryptDecryptRoundtrip(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db, newTestKey(t))

	cred := sampleCred()
	if err := store.Put("alice", "github_abc", cred); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := store.Get("alice", "github_abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != cred.AccessToken ||
		got.RefreshToken != cred.RefreshToken ||
		got.TokenType != cred.TokenType ||
		got.Audience != cred.Audience ||
		got.ObtainedVia != cred.ObtainedVia ||
		!got.ExpiresAt.Equal(cred.ExpiresAt) ||
		len(got.Scopes) != len(cred.Scopes) {
		t.Errorf("roundtrip mismatch: got %+v want %+v", got, cred)
	}

	// On-disk bytes must NOT contain the plaintext secret.
	if err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(credentialBucket))
		if b == nil {
			t.Fatalf("bucket missing")
		}
		raw := b.Get([]byte("alice:github_abc"))
		if raw == nil {
			t.Fatalf("record missing on disk")
		}
		if bytes.Contains(raw, []byte(cred.AccessToken)) {
			t.Errorf("access token stored in plaintext on disk")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBBoltAESStore_NonceUniqueness(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db, newTestKey(t))
	cred := sampleCred()

	if err := store.Put("alice", "k1", cred); err != nil {
		t.Fatalf("Put: %v", err)
	}
	var c1 []byte
	_ = db.View(func(tx *bbolt.Tx) error {
		c1 = append(c1, tx.Bucket([]byte(credentialBucket)).Get([]byte("alice:k1"))...)
		return nil
	})

	if err := store.Put("alice", "k2", cred); err != nil {
		t.Fatalf("Put: %v", err)
	}
	var c2 []byte
	_ = db.View(func(tx *bbolt.Tx) error {
		c2 = append(c2, tx.Bucket([]byte(credentialBucket)).Get([]byte("alice:k2"))...)
		return nil
	})

	if bytes.Equal(c1, c2) {
		t.Errorf("identical plaintext encrypted to identical ciphertext — nonce not random")
	}
}

func TestBBoltAESStore_PerUserIsolation(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db, newTestKey(t))

	if err := store.Put("alice", "srv", sampleCred()); err != nil {
		t.Fatalf("Put alice: %v", err)
	}
	bobCred := sampleCred()
	bobCred.AccessToken = "bob-token"
	if err := store.Put("bob", "srv", bobCred); err != nil {
		t.Fatalf("Put bob: %v", err)
	}

	// bob cannot read alice's record via his own userID.
	got, err := store.Get("bob", "srv")
	if err != nil {
		t.Fatalf("Get bob: %v", err)
	}
	if got.AccessToken != "bob-token" {
		t.Errorf("isolation breach: bob got %q", got.AccessToken)
	}

	// List is scoped per user.
	aliceList, err := store.List("alice")
	if err != nil {
		t.Fatalf("List alice: %v", err)
	}
	if len(aliceList) != 1 || aliceList[0].ServerKey != "srv" {
		t.Errorf("alice list = %+v, want 1 entry for srv", aliceList)
	}

	// A userID that is a prefix of another must not bleed.
	if err := store.Put("alice2", "srv", sampleCred()); err != nil {
		t.Fatalf("Put alice2: %v", err)
	}
	aliceList, _ = store.List("alice")
	if len(aliceList) != 1 {
		t.Errorf("alice list leaked alice2 records: %+v", aliceList)
	}
}

func TestBBoltAESStore_SubjectToken(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db, newTestKey(t))

	subj := sampleCred()
	subj.Type = "idp_subject_token"
	// Empty serverKey => idp subject token record keyed by userID.
	if err := store.Put("alice", "", subj); err != nil {
		t.Fatalf("Put subject: %v", err)
	}
	got, err := store.Get("alice", "")
	if err != nil {
		t.Fatalf("Get subject: %v", err)
	}
	if got.Type != "idp_subject_token" {
		t.Errorf("subject token type mismatch: %q", got.Type)
	}

	// Subject token must be keyed by bare userID (no colon).
	if err := db.View(func(tx *bbolt.Tx) error {
		if tx.Bucket([]byte(credentialBucket)).Get([]byte("alice")) == nil {
			t.Errorf("subject token not keyed by bare userID")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// List (upstream creds) must not include the subject token.
	if err := store.Put("alice", "srv", sampleCred()); err != nil {
		t.Fatalf("Put upstream: %v", err)
	}
	list, _ := store.List("alice")
	if len(list) != 1 || list[0].ServerKey != "srv" {
		t.Errorf("List should exclude subject token, got %+v", list)
	}
}

func TestBBoltAESStore_DeleteAndNotFound(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db, newTestKey(t))

	if _, err := store.Get("alice", "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}

	if err := store.Put("alice", "srv", sampleCred()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Delete("alice", "srv"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get("alice", "srv"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestNewBBoltAESStore_MissingKeyDisabled(t *testing.T) {
	db := openTestDB(t)
	store, err := NewBBoltAESStore(db, "", zap.NewNop())
	if err != nil {
		t.Fatalf("missing key should not error, got %v", err)
	}
	if store.Enabled() {
		t.Errorf("store with no key should be disabled")
	}
	if err := store.Put("alice", "srv", sampleCred()); !errors.Is(err, ErrStoreDisabled) {
		t.Errorf("Put on disabled = %v, want ErrStoreDisabled", err)
	}
	if _, err := store.Get("alice", "srv"); !errors.Is(err, ErrStoreDisabled) {
		t.Errorf("Get on disabled = %v, want ErrStoreDisabled", err)
	}
	if _, err := store.List("alice"); !errors.Is(err, ErrStoreDisabled) {
		t.Errorf("List on disabled = %v, want ErrStoreDisabled", err)
	}
	if err := store.Delete("alice", "srv"); !errors.Is(err, ErrStoreDisabled) {
		t.Errorf("Delete on disabled = %v, want ErrStoreDisabled", err)
	}
}

func TestNewBBoltAESStore_InvalidKey(t *testing.T) {
	db := openTestDB(t)

	// Non-base64 garbage.
	if _, err := NewBBoltAESStore(db, "not!!base64!!", zap.NewNop()); err == nil {
		t.Errorf("invalid base64 key should error")
	}
	// Valid base64 but wrong length (16 bytes, not 32).
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := NewBBoltAESStore(db, short, zap.NewNop()); err == nil {
		t.Errorf("wrong-length key should error")
	}
}

func TestBBoltAESStore_KeyChangedNotConnected(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db, newTestKey(t))
	if err := store.Put("alice", "srv", sampleCred()); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Re-open with a different key (simulates key rotation/misconfig).
	rotated := newTestStore(t, db, newTestKey(t))
	if _, err := rotated.Get("alice", "srv"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get with rotated key = %v, want ErrNotFound (record undecryptable => absent)", err)
	}
	// List must also tolerate undecryptable records without crashing.
	list, err := rotated.List("alice")
	if err != nil {
		t.Fatalf("List with rotated key errored: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List with rotated key should skip undecryptable records, got %+v", list)
	}
}

func TestResolveMasterKey(t *testing.T) {
	t.Setenv(MasterKeyEnvVar, "")
	if got := ResolveMasterKey("from-config"); got != "from-config" {
		t.Errorf("empty env should fall back to config, got %q", got)
	}
	t.Setenv(MasterKeyEnvVar, "from-env")
	if got := ResolveMasterKey("from-config"); got != "from-env" {
		t.Errorf("env should override config, got %q", got)
	}
	t.Setenv(MasterKeyEnvVar, "")
	if got := ResolveMasterKey(""); got != "" {
		t.Errorf("both empty should be empty, got %q", got)
	}
}
