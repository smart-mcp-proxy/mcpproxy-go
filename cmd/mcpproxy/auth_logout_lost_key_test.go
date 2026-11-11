package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 123 review: the documented lost-key recovery is
// `mcpproxy auth logout --server=<server>`. A lost key is usually a key
// reference that no longer resolves (a missing secret mount or key file).
// Standalone logout is a key-only delete, so it must not resolve the key
// first and refuse with exit 4: it discards the unreadable row.
func TestAuthLogoutStandalone_UnresolvableKeyStillDiscardsRow(t *testing.T) {
	t.Setenv(config.EnvOAuthTokenKey, "")
	t.Setenv(config.EnvOAuthTokenKeyFile, "")
	dataDir := t.TempDir()
	srv := &config.ServerConfig{Name: "x", URL: "https://example.invalid/mcp", Protocol: "http", Enabled: true}
	serverKey := oauth.GenerateServerKey(srv.Name, srv.URL)

	db, err := storage.NewBoltDB(dataDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: serverKey, AccessToken: "a"}))
	require.NoError(t, db.Close())
	// Make the row unreadable: a sealed envelope under a key nobody has.
	raw, err := bbolt.Open(filepath.Join(dataDir, "config.db"), 0600, nil)
	require.NoError(t, err)
	require.NoError(t, raw.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(storage.OAuthTokenBucket)).Put([]byte(serverKey),
			[]byte("enc:v1:0123456789abcdef:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"))
	}))
	require.NoError(t, raw.Close())

	cfg := &config.Config{
		DataDir:                 dataDir,
		OAuthTokenEncryptionKey: "${file:" + filepath.Join(t.TempDir(), "missing.key") + "}",
		Servers:                 []*config.ServerConfig{srv},
	}
	_, _, keyErr := config.ResolveOAuthTokenKey(cfg)
	require.Error(t, keyErr, "premise: the key reference does not resolve")

	require.NoError(t, runAuthLogoutStandalone(context.Background(), "x", cfg))

	raw, err = bbolt.Open(filepath.Join(dataDir, "config.db"), 0600, nil)
	require.NoError(t, err)
	defer raw.Close()
	require.NoError(t, raw.View(func(tx *bbolt.Tx) error {
		require.Nil(t, tx.Bucket([]byte(storage.OAuthTokenBucket)).Get([]byte(serverKey)), "row must be discarded")
		return nil
	}))
}

// Only a reference that no longer resolves is a lost key. A key that is
// present but wrong (malformed, or both environment variables set) is an
// operator mistake that the keyless logout would hide, so it still stops
// with the exit-4 startup error and the row is kept (Spec 123 FR-022).
func TestAuthLogoutStandalone_MalformedKeyStillRefuses(t *testing.T) {
	t.Setenv(config.EnvOAuthTokenKey, "")
	t.Setenv(config.EnvOAuthTokenKeyFile, "")
	dataDir := t.TempDir()
	srv := &config.ServerConfig{Name: "x", URL: "https://example.invalid/mcp", Protocol: "http", Enabled: true}
	serverKey := oauth.GenerateServerKey(srv.Name, srv.URL)

	db, err := storage.NewBoltDB(dataDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: serverKey, AccessToken: "a"}))
	require.NoError(t, db.Close())

	cfg := &config.Config{
		DataDir:                 dataDir,
		OAuthTokenEncryptionKey: "not-a-32-byte-key",
		Servers:                 []*config.ServerConfig{srv},
	}

	err = runAuthLogoutStandalone(context.Background(), "x", cfg)
	var startupErr *config.StartupError
	require.ErrorAs(t, err, &startupErr)
	require.Equal(t, config.ExitCodeOAuthTokenKeyError, startupErr.ExitCode)

	raw, err := bbolt.Open(filepath.Join(dataDir, "config.db"), 0600, nil)
	require.NoError(t, err)
	defer raw.Close()
	require.NoError(t, raw.View(func(tx *bbolt.Tx) error {
		require.NotNil(t, tx.Bucket([]byte(storage.OAuthTokenBucket)).Get([]byte(serverKey)), "row must be kept")
		return nil
	}))
}

// A key source that is present but unusable (here an oversized file) is not a
// lost key: standalone logout must refuse and keep the row, like a malformed
// literal (Spec 123 review).
func TestAuthLogoutStandalone_PresentButUnusableSourceRefuses(t *testing.T) {
	t.Setenv(config.EnvOAuthTokenKey, "")
	t.Setenv(config.EnvOAuthTokenKeyFile, "")
	dataDir := t.TempDir()
	srv := &config.ServerConfig{Name: "x", URL: "https://example.invalid/mcp", Protocol: "http", Enabled: true}
	serverKey := oauth.GenerateServerKey(srv.Name, srv.URL)

	db, err := storage.NewBoltDB(dataDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: serverKey, AccessToken: "a"}))
	require.NoError(t, db.Close())

	big := filepath.Join(t.TempDir(), "big.key")
	require.NoError(t, os.WriteFile(big, bytes.Repeat([]byte("A"), 70*1024), 0o600))
	for name, cfg := range map[string]*config.Config{
		"oversized file": {DataDir: dataDir, OAuthTokenEncryptionKey: "${file:" + big + "}", Servers: []*config.ServerConfig{srv}},
		"empty env":      {DataDir: dataDir, OAuthTokenEncryptionKey: "${env:SPEC123_EMPTY_LOGOUT_ENV}", Servers: []*config.ServerConfig{srv}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SPEC123_EMPTY_LOGOUT_ENV", "")
			err := runAuthLogoutStandalone(context.Background(), "x", cfg)
			var startupErr *config.StartupError
			require.ErrorAs(t, err, &startupErr)
			require.Equal(t, config.ExitCodeOAuthTokenKeyError, startupErr.ExitCode)
			require.False(t, config.IsOAuthTokenKeyUnresolvable(err))
		})
	}

	raw, err := bbolt.Open(filepath.Join(dataDir, "config.db"), 0600, nil)
	require.NoError(t, err)
	defer raw.Close()
	require.NoError(t, raw.View(func(tx *bbolt.Tx) error {
		require.NotNil(t, tx.Bucket([]byte(storage.OAuthTokenBucket)).Get([]byte(serverKey)), "row must be kept")
		return nil
	}))
}
