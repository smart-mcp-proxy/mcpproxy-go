package main

import (
	"context"
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
