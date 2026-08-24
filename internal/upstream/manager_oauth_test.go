package upstream

import (
	"errors"
	"testing"
	"time"

	uptransport "github.com/mark3labs/mcp-go/client/transport"
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
		ServerName:   serverKey,            // Key used for storage lookup (hash-based)
		DisplayName:  "test-dynamic-oauth", // Human-readable name for RefreshManager
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

// TestTokenFingerprint verifies the identity used to decide whether a persisted
// token is NEW: the same untouched token must compare equal (so the scan does
// not redial), any re-issue must not.
func TestTokenFingerprint(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC()
	written := time.Now().UTC()
	base := &uptransport.Token{AccessToken: "at", RefreshToken: "rt", ExpiresAt: expires}
	fp := tokenFingerprint(base, written)

	assert.Equal(t, fp, tokenFingerprint(&uptransport.Token{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: expires,
	}, written), "same token, same write: must fingerprint the same")

	assert.NotEqual(t, fp, tokenFingerprint(&uptransport.Token{
		AccessToken: "at2", RefreshToken: "rt", ExpiresAt: expires,
	}, written), "a new access token must fingerprint differently")

	assert.NotEqual(t, fp, tokenFingerprint(&uptransport.Token{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: expires.Add(time.Minute),
	}, written), "a refreshed expiry must fingerprint differently")

	// A provider may re-issue a byte-identical token; the write stamp is what
	// keeps that from silently suppressing the wake.
	assert.NotEqual(t, fp, tokenFingerprint(base, written.Add(time.Second)),
		"a token rewritten identically must still fingerprint differently")

	assert.NotContains(t, fp, "at", "the fingerprint must not carry the token")
	assert.Empty(t, tokenFingerprint(nil, written))
}

// TestScanForNewTokens_OnlyOnNewToken pins the fix for the 5s redial loop: the
// scan must fire when a login/refresh writes a NEW token, not on every pass just
// because the server still holds the stale token it already failed with (#1013).
func TestScanForNewTokens_OnlyOnNewToken(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()
	db, err := storage.NewBoltDB(tempDir, logger.Sugar())
	require.NoError(t, err)
	defer db.Close()

	serverConfig := &config.ServerConfig{
		Name:     "parked-server",
		URL:      "http://127.0.0.1:1/mcp", // refused immediately; no real upstream needed
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now(),
	}
	serverKey := oauth.GenerateServerKey(serverConfig.Name, serverConfig.URL)
	saveToken := func(access string) {
		require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{
			ServerName:  serverKey,
			DisplayName: serverConfig.Name,
			AccessToken: access,
			TokenType:   "Bearer",
			ExpiresAt:   time.Now().Add(time.Hour),
			Created:     time.Now(),
			Updated:     time.Now(),
		}))
	}
	saveToken("stale-token")

	manager := &Manager{
		clients:           make(map[string]*managed.Client),
		logger:            logger,
		storage:           db,
		secretResolver:    secret.NewResolver(),
		tokenReconnect:    make(map[string]time.Time),
		tokenFingerprints: make(map[string]string),
	}

	client, err := managed.NewClient("parked-server", serverConfig, logger, nil, &config.Config{}, db, secret.NewResolver())
	require.NoError(t, err)
	manager.clients["parked-server"] = client

	// Park the client exactly as a deferred-OAuth connect failure would.
	client.StateManager.SetPendingAuth(errors.New("OAuth authentication required for parked-server: login available via Web UI"))

	// Pretend the per-server rate limit has expired so it cannot mask the result.
	expireRateLimit := func() { manager.tokenReconnect["parked-server"] = time.Now().Add(-time.Minute) }

	expireRateLimit()
	manager.scanForNewTokens()
	require.WithinDuration(t, time.Now(), manager.tokenReconnect["parked-server"], time.Second,
		"first scan must retry with the stored token")
	require.NotEmpty(t, manager.tokenFingerprints["parked-server"])

	// Same token, rate limit expired: must NOT redial again.
	expireRateLimit()
	before := manager.tokenReconnect["parked-server"]
	manager.scanForNewTokens()
	require.Equal(t, before, manager.tokenReconnect["parked-server"],
		"scan redialed a parked server for a token it had already tried")

	// A fresh token (login completed) must wake it.
	saveToken("fresh-token")
	expireRateLimit()
	manager.scanForNewTokens()
	require.WithinDuration(t, time.Now(), manager.tokenReconnect["parked-server"], time.Second,
		"scan did not wake the parked server when a new token appeared")

	// A re-login that happens to persist a byte-identical token is still a new
	// write, and this scan is the fallback wake when the CLI could not record an
	// OAuth completion event — it must not be swallowed.
	time.Sleep(2 * time.Millisecond) // ensure a distinct Updated stamp
	saveToken("fresh-token")
	expireRateLimit()
	manager.scanForNewTokens()
	require.WithinDuration(t, time.Now(), manager.tokenReconnect["parked-server"], time.Second,
		"scan ignored an identically-rewritten token")
}
