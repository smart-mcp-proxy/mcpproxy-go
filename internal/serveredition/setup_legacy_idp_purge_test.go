//go:build server

package serveredition

import (
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// legacyCredentialBucket mirrors broker.credentialBucket (unexported there);
// the test seeds the bucket by name so it proves the production sweep found
// the real bucket, not one the test handed it.
const legacyCredentialBucket = "user_upstream_credentials"

func seedCredentialRows(t *testing.T, db *bbolt.DB, keys ...string) {
	t.Helper()
	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(legacyCredentialBucket))
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := b.Put([]byte(k), []byte("opaque")); err != nil {
				return err
			}
		}
		return nil
	}))
}

func credentialRowKeys(t *testing.T, db *bbolt.DB) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	require.NoError(t, db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(legacyCredentialBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			keys[string(k)] = true
			return nil
		})
	}))
	return keys
}

// TestSetupMultiUserOAuth_SweepsLegacyIDPSubjectTokensAtBoot: the production
// setup deletes the bare-userID rows the retired store_idp_tokens writer left
// behind (Spec 107 FR-033 residual) and leaves upstream credentials alone —
// with no encryption key configured, which is the state of a deployment that
// only ever enabled store_idp_tokens.
//
// BITES: drop the PurgeLegacyIDPSubjectTokens call from setup.go.
func TestSetupMultiUserOAuth_SweepsLegacyIDPSubjectTokensAtBoot(t *testing.T) {
	t.Setenv("MCPPROXY_CRED_KEY", "")

	tmpDir := t.TempDir()
	db, err := bbolt.Open(tmpDir+"/test.db", 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const legacyUser = "01HTEST0000000000000USERA"
	seedCredentialRows(t, db, legacyUser, legacyUser+":github_0123456789abcdef")
	require.Len(t, credentialRowKeys(t, db), 2, "fixture")

	require.NoError(t, setupMultiUserOAuth(Dependencies{
		Router:  chi.NewRouter(),
		DB:      db,
		Logger:  zap.NewNop().Sugar(),
		DataDir: tmpDir,
		Config: &config.Config{
			ServerEdition: &config.ServerEditionConfig{
				Enabled:     true,
				AdminEmails: []string{"admin@example.com"},
				OAuth: &config.ServerEditionOAuthConfig{
					Provider:     "google",
					ClientID:     "test-client-id",
					ClientSecret: "test-client-secret",
				},
			},
		},
	}))

	keys := credentialRowKeys(t, db)
	assert.False(t, keys[legacyUser], "the legacy IdP subject-token row must be gone after boot: %v", keys)
	assert.True(t, keys[legacyUser+":github_0123456789abcdef"], "upstream credentials must survive the sweep: %v", keys)
}

// TestSetupMultiUserOAuth_SweepsLegacyRowsEvenWhenTheKeyIsInvalid: the
// release notice promises the sweep on the first start after upgrading
// "whether or not MCPPROXY_CRED_KEY is still set". A key that is SET but
// malformed makes NewBBoltAESStore fail and setupMultiUserOAuth return, and
// SetupAll only logs that error — the server comes up without SSO but WITH
// the legacy IdP tokens still at rest. The sweep matches rows by key and
// needs no cipher, so it must run before the store is constructed (codex
// round 2 on PR-A).
//
// BITES: move the purge back behind NewBBoltAESStore.
func TestSetupMultiUserOAuth_SweepsLegacyRowsEvenWhenTheKeyIsInvalid(t *testing.T) {
	t.Setenv("MCPPROXY_CRED_KEY", "not-base64-and-not-32-bytes!!")

	tmpDir := t.TempDir()
	db, err := bbolt.Open(tmpDir+"/test.db", 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const legacyUser = "01HTEST0000000000000USERB"
	seedCredentialRows(t, db, legacyUser, legacyUser+":github_0123456789abcdef")

	err = setupMultiUserOAuth(Dependencies{
		Router:  chi.NewRouter(),
		DB:      db,
		Logger:  zap.NewNop().Sugar(),
		DataDir: tmpDir,
		Config: &config.Config{
			ServerEdition: &config.ServerEditionConfig{
				Enabled:     true,
				AdminEmails: []string{"admin@example.com"},
				OAuth: &config.ServerEditionOAuthConfig{
					Provider:     "google",
					ClientID:     "test-client-id",
					ClientSecret: "test-client-secret",
				},
			},
		},
	})
	require.Error(t, err, "an invalid key is still a loud misconfiguration")
	assert.Contains(t, err.Error(), "credential store")

	keys := credentialRowKeys(t, db)
	assert.False(t, keys[legacyUser], "the legacy row must be swept before the key is validated: %v", keys)
	assert.True(t, keys[legacyUser+":github_0123456789abcdef"], "upstream credentials must survive: %v", keys)
}
