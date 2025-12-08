package storage_test

import (
	"os"
	"testing"

	"mcpproxy-go/internal/oauth"
	"mcpproxy-go/internal/storage"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Verify ClearOAuthState removes both legacy (server name) and hashed serverKey tokens.
func TestManager_ClearOAuthState_RemovesHashedToken(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-clear-oauth-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	mgr, err := storage.NewManager(tmpDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	serverName := "demo"
	serverURL := "https://example.com"
	serverKey := oauth.GenerateServerKey(serverName, serverURL)

	// Seed tokens under both the legacy name and the hashed serverKey
	err = mgr.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: serverName, AccessToken: "legacy"})
	require.NoError(t, err)

	err = mgr.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: serverKey, AccessToken: "hashed"})
	require.NoError(t, err)

	// Clear and verify both are gone
	require.NoError(t, mgr.ClearOAuthState(serverName))

	_, err = mgr.GetBoltDB().GetOAuthToken(serverName)
	require.Error(t, err)

	_, err = mgr.GetBoltDB().GetOAuthToken(serverKey)
	require.Error(t, err)
}
