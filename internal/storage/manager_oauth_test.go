package storage_test

import (
	"os"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"

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

// TestBoltDB_UpdateOAuthClientCredentials_WithCallbackPort verifies that UpdateOAuthClientCredentials
// stores and retrieves the callback port alongside client credentials (Spec 022).
func TestBoltDB_UpdateOAuthClientCredentials_WithCallbackPort(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-oauth-port-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	mgr, err := storage.NewManager(tmpDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	serverKey := "test-server_abc123"
	clientID := "dcr-client-id"
	clientSecret := "dcr-client-secret"
	callbackPort := 54321

	// Store credentials with callback port
	err = mgr.GetBoltDB().UpdateOAuthClientCredentials(serverKey, clientID, clientSecret, callbackPort)
	require.NoError(t, err)

	// Retrieve and verify
	gotClientID, gotClientSecret, gotPort, err := mgr.GetBoltDB().GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	require.Equal(t, clientID, gotClientID)
	require.Equal(t, clientSecret, gotClientSecret)
	require.Equal(t, callbackPort, gotPort)
}

// TestBoltDB_GetOAuthClientCredentials_LegacyRecord verifies that GetOAuthClientCredentials
// returns 0 for legacy records that don't have a callback port (backward compatibility).
func TestBoltDB_GetOAuthClientCredentials_LegacyRecord(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-oauth-legacy-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	mgr, err := storage.NewManager(tmpDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	serverKey := "legacy-server_xyz789"

	// Save a legacy-style token record (only ClientID/ClientSecret, no CallbackPort)
	err = mgr.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName:   serverKey,
		AccessToken:  "test-token",
		ClientID:     "legacy-client-id",
		ClientSecret: "legacy-client-secret",
		// CallbackPort not set - should default to 0
	})
	require.NoError(t, err)

	// Retrieve credentials - port should be 0
	gotClientID, gotClientSecret, gotPort, err := mgr.GetBoltDB().GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	require.Equal(t, "legacy-client-id", gotClientID)
	require.Equal(t, "legacy-client-secret", gotClientSecret)
	require.Equal(t, 0, gotPort, "Legacy records should return port 0")
}

// TestBoltDB_ClearOAuthClientCredentials_PreservesToken verifies that ClearOAuthClientCredentials
// clears DCR fields but preserves token data (Spec 022).
func TestBoltDB_ClearOAuthClientCredentials_PreservesToken(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage-oauth-clear-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	mgr, err := storage.NewManager(tmpDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	serverKey := "clear-test-server_def456"

	// First, save a complete OAuth token record with DCR credentials and token
	err = mgr.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName:   serverKey,
		AccessToken:  "access-token-123",
		RefreshToken: "refresh-token-456",
		ClientID:     "dcr-client-id",
		ClientSecret: "dcr-client-secret",
		CallbackPort: 12345,
		RedirectURI:  "http://127.0.0.1:12345/oauth/callback",
	})
	require.NoError(t, err)

	// Clear DCR credentials
	err = mgr.GetBoltDB().ClearOAuthClientCredentials(serverKey)
	require.NoError(t, err)

	// Verify token is still accessible
	record, err := mgr.GetBoltDB().GetOAuthToken(serverKey)
	require.NoError(t, err)
	require.Equal(t, "access-token-123", record.AccessToken, "Access token should be preserved")
	require.Equal(t, "refresh-token-456", record.RefreshToken, "Refresh token should be preserved")

	// Verify DCR credentials are cleared
	require.Empty(t, record.ClientID, "ClientID should be cleared")
	require.Empty(t, record.ClientSecret, "ClientSecret should be cleared")
	require.Equal(t, 0, record.CallbackPort, "CallbackPort should be cleared")
	require.Empty(t, record.RedirectURI, "RedirectURI should be cleared")
}
