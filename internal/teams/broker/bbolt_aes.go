//go:build server

package broker

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// credentialBucket holds AES-256-GCM encrypted UpstreamCredential records.
//
// Key scheme:
//   - upstream credential:   "<userID>:<serverKey>"
//   - idp subject token:     "<userID>"            (no colon)
//
// serverKey follows the existing SHA256(name+url) scheme from
// internal/oauth.GenerateServerKey.
const credentialBucket = "user_upstream_credentials" //nolint:gosec // bucket name, not a credential

// aesKeyLen is the required key length for AES-256.
const aesKeyLen = 32

// BBoltAESStore is the default CredentialStore backed by BBolt with
// AES-256-GCM authenticated encryption and a per-record random nonce.
//
// When no master key is configured the store is constructed in a disabled
// state: every operation returns ErrStoreDisabled and the rest of the gateway
// is unaffected (FR-022).
type BBoltAESStore struct {
	db      *bbolt.DB
	gcm     cipher.AEAD // nil when disabled
	enabled bool
	logger  *zap.Logger
}

// NewBBoltAESStore constructs a credential store over the given BBolt database.
//
// base64Key is the base64-encoded 32-byte AES-256 master key (see
// ResolveMasterKey). Behaviour by key state:
//   - empty key     -> store disabled (no error); the condition is logged so it
//     can be surfaced at startup.
//   - present but invalid (bad base64 / wrong length) -> error (loud
//     misconfiguration, not silent degradation).
//   - valid 32-byte -> store enabled.
func NewBBoltAESStore(db *bbolt.DB, base64Key string, logger *zap.Logger) (*BBoltAESStore, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	logger = logger.Named("credential-store")

	if strings.TrimSpace(base64Key) == "" {
		logger.Warn("upstream credential broker disabled: no encryption key configured " +
			"(set MCPPROXY_CRED_KEY or teams.credential_encryption_key to enable)")
		return &BBoltAESStore{db: db, enabled: false, logger: logger}, nil
	}

	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(base64Key))
	if err != nil {
		return nil, fmt.Errorf("decode credential encryption key (must be base64): %w", err)
	}
	if len(key) != aesKeyLen {
		return nil, fmt.Errorf("credential encryption key must be %d bytes (got %d) for AES-256", aesKeyLen, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	// Ensure the bucket exists up front so reads on a fresh DB don't trip on a
	// missing bucket.
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists([]byte(credentialBucket))
		return e
	}); err != nil {
		return nil, fmt.Errorf("init credential bucket: %w", err)
	}

	logger.Info("upstream credential broker enabled (AES-256-GCM at rest)")
	return &BBoltAESStore{db: db, gcm: gcm, enabled: true, logger: logger}, nil
}

// Enabled reports whether a usable encryption key is configured.
func (s *BBoltAESStore) Enabled() bool { return s.enabled }

// recordKey builds the BBolt key for a (userID, serverKey) pair. An empty
// serverKey yields the bare userID, used for the idp subject token.
func recordKey(userID, serverKey string) string {
	if serverKey == "" {
		return userID
	}
	return userID + ":" + serverKey
}

// Get implements CredentialStore.
func (s *BBoltAESStore) Get(userID, serverKey string) (*UpstreamCredential, error) {
	if !s.enabled {
		return nil, ErrStoreDisabled
	}
	key := recordKey(userID, serverKey)

	var ciphertext []byte
	if err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(credentialBucket))
		if b == nil {
			return nil
		}
		if v := b.Get([]byte(key)); v != nil {
			ciphertext = append(ciphertext, v...)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read credential: %w", err)
	}
	if ciphertext == nil {
		return nil, ErrNotFound
	}

	cred, err := s.decrypt(ciphertext)
	if err != nil {
		// Undecryptable (e.g. rotated key) -> treat as absent, never crash.
		s.logger.Warn("credential record could not be decrypted; treating as absent",
			zap.String("user", userID), zap.String("server_key", serverKey), zap.Error(err))
		return nil, ErrNotFound
	}
	return cred, nil
}

// Put implements CredentialStore.
func (s *BBoltAESStore) Put(userID, serverKey string, cred *UpstreamCredential) error {
	if !s.enabled {
		return ErrStoreDisabled
	}
	if cred == nil {
		return fmt.Errorf("nil credential")
	}
	cred.UpdatedAt = time.Now().UTC()

	ciphertext, err := s.encrypt(cred)
	if err != nil {
		return fmt.Errorf("encrypt credential: %w", err)
	}
	key := recordKey(userID, serverKey)
	if err := s.db.Update(func(tx *bbolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists([]byte(credentialBucket))
		if e != nil {
			return e
		}
		return b.Put([]byte(key), ciphertext)
	}); err != nil {
		return fmt.Errorf("write credential: %w", err)
	}
	return nil
}

// Delete implements CredentialStore.
func (s *BBoltAESStore) Delete(userID, serverKey string) error {
	if !s.enabled {
		return ErrStoreDisabled
	}
	key := recordKey(userID, serverKey)
	if err := s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(credentialBucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	}); err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	return nil
}

// List implements CredentialStore. It returns only upstream credentials
// (prefix "<userID>:"); the idp subject token (bare userID) is excluded.
// Undecryptable records are skipped rather than failing the whole listing.
func (s *BBoltAESStore) List(userID string) ([]CredentialEntry, error) {
	if !s.enabled {
		return nil, ErrStoreDisabled
	}
	prefix := []byte(userID + ":")

	var entries []CredentialEntry
	if err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(credentialBucket))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = c.Next() {
			cred, err := s.decrypt(v)
			if err != nil {
				s.logger.Warn("skipping undecryptable credential in list",
					zap.String("user", userID), zap.ByteString("key", k), zap.Error(err))
				continue
			}
			entries = append(entries, CredentialEntry{
				ServerKey:  string(k[len(prefix):]),
				Credential: cred,
			})
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	return entries, nil
}

// hasPrefix reports whether b begins with prefix.
func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}

// encrypt serializes the credential to JSON and AES-256-GCM encrypts it,
// returning nonce||ciphertext with a fresh random nonce per call (FR-020).
func (s *BBoltAESStore) encrypt(cred *UpstreamCredential) ([]byte, error) {
	plaintext, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	// Seal appends the ciphertext to nonce, yielding nonce||ciphertext.
	return s.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt reverses encrypt. It returns an error on any tampering, truncation,
// or wrong-key condition (callers treat that as ErrNotFound).
func (s *BBoltAESStore) decrypt(data []byte) (*UpstreamCredential, error) {
	ns := s.gcm.NonceSize()
	if len(data) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:ns], data[ns:]
	plaintext, err := s.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	var cred UpstreamCredential
	if err := json.Unmarshal(plaintext, &cred); err != nil {
		return nil, err
	}
	return &cred, nil
}
