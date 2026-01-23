package upstream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// TestRefreshOAuthToken_DynamicOAuthDiscovery tests that RefreshOAuthToken works
// for servers that use dynamic OAuth discovery (no OAuth in static config).
//
// Bug: The current implementation checks serverConfig.OAuth which is nil for
// servers that discover OAuth via Protected Resource Metadata at runtime.
// These servers have OAuth tokens stored in the database but not in their config.
//
// Related: spec 023-oauth-state-persistence
func TestRefreshOAuthToken_DynamicOAuthDiscovery(t *testing.T) {
	logger := zap.NewNop()
	sugaredLogger := logger.Sugar()

	// Create a server config WITHOUT OAuth block (simulates dynamic OAuth discovery)
	// This is how servers like atlassian-remote, slack work - they discover OAuth
	// requirements at runtime via Protected Resource Metadata
	serverConfig := &config.ServerConfig{
		Name:     "test-dynamic-oauth",
		URL:      "https://example.com/mcp",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now(),
		// NOTE: No OAuth field set - this is the key part of the test
		// OAuth was discovered at runtime, not configured statically
	}

	// Create an in-memory storage with OAuth tokens for this server
	// This simulates a server that authenticated via dynamic OAuth discovery
	tempDir := t.TempDir()
	db, err := storage.NewBoltDB(tempDir, sugaredLogger)
	require.NoError(t, err)
	defer db.Close()

	// Generate the server key using the same function as PersistentTokenStore
	// This is critical - tokens are stored with key = hash(name|url), not just name
	serverKey := oauth.GenerateServerKey(serverConfig.Name, serverConfig.URL)

	// Store an OAuth token for the server (as if it had authenticated previously)
	// The ServerName field is used as the storage key (must match GenerateServerKey output)
	token := &storage.OAuthTokenRecord{
		ServerName:   serverKey,             // Key used for storage lookup (hash-based)
		DisplayName:  "test-dynamic-oauth",  // Human-readable name for RefreshManager
		AccessToken:  "expired-access-token",
		RefreshToken: "valid-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour), // Expired
		Created:      time.Now().Add(-2 * time.Hour),
		Updated:      time.Now().Add(-1 * time.Hour),
	}
	err = db.SaveOAuthToken(token)
	require.NoError(t, err)

	// Verify token was saved with the correct key
	savedToken, err := db.GetOAuthToken(serverKey)
	require.NoError(t, err)
	require.NotNil(t, savedToken, "Token should be saved in database with server_key")
	assert.Equal(t, "valid-refresh-token", savedToken.RefreshToken)

	// Create the manager with a client for this server
	manager := &Manager{
		clients:        make(map[string]*managed.Client),
		logger:         logger,
		storage:        db,
		secretResolver: secret.NewResolver(),
	}

	// Create a managed client for the server
	client, err := managed.NewClient(
		"test-dynamic-oauth",
		serverConfig,
		logger,
		nil,              // logConfig
		&config.Config{}, // globalConfig
		db,               // bolt storage
		secret.NewResolver(),
	)
	require.NoError(t, err)
	manager.clients["test-dynamic-oauth"] = client

	// Attempt to refresh the OAuth token
	// BUG: This currently fails with "server does not use OAuth: test-dynamic-oauth"
	// because it checks serverConfig.OAuth which is nil
	err = manager.RefreshOAuthToken("test-dynamic-oauth")

	// The refresh should NOT fail with "server does not use OAuth"
	// It should either:
	// 1. Successfully trigger a token refresh, or
	// 2. Fail with a different error (network, invalid token, etc.)
	if err != nil {
		assert.NotContains(t, err.Error(), "server does not use OAuth",
			"RefreshOAuthToken should not fail just because OAuth is not in static config. "+
				"The server has OAuth tokens in the database from dynamic discovery.")
	}
}

// TestRefreshOAuthToken_StaticOAuthConfig tests the happy path where OAuth
// is configured statically in the server config.
func TestRefreshOAuthToken_StaticOAuthConfig(t *testing.T) {
	logger := zap.NewNop()
	sugaredLogger := logger.Sugar()

	// Create a server config WITH OAuth block (traditional static config)
	serverConfig := &config.ServerConfig{
		Name:     "test-static-oauth",
		URL:      "https://example.com/mcp",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now(),
		OAuth: &config.OAuthConfig{
			ClientID: "test-client-id",
			Scopes:   []string{"read", "write"},
		},
	}

	tempDir := t.TempDir()
	db, err := storage.NewBoltDB(tempDir, sugaredLogger)
	require.NoError(t, err)
	defer db.Close()

	manager := &Manager{
		clients:        make(map[string]*managed.Client),
		logger:         logger,
		storage:        db,
		secretResolver: secret.NewResolver(),
	}

	client, err := managed.NewClient(
		"test-static-oauth",
		serverConfig,
		logger,
		nil,
		&config.Config{},
		db,
		secret.NewResolver(),
	)
	require.NoError(t, err)
	manager.clients["test-static-oauth"] = client

	// This should not fail with "server does not use OAuth"
	// It may fail with connection errors, but that's expected in a unit test
	err = manager.RefreshOAuthToken("test-static-oauth")

	// Should not fail with the OAuth detection error
	if err != nil {
		assert.NotContains(t, err.Error(), "server does not use OAuth")
	}
}

// TestRefreshOAuthToken_ServerNotFound tests that non-existent servers return proper error.
func TestRefreshOAuthToken_ServerNotFound(t *testing.T) {
	logger := zap.NewNop()

	manager := &Manager{
		clients: make(map[string]*managed.Client),
		logger:  logger,
	}

	err := manager.RefreshOAuthToken("non-existent-server")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "server not found")
}
