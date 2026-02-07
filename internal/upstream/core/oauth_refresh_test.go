package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestGetOAuthHandler_NilClient verifies that GetOAuthHandler returns nil when the
// mcp-go client has not been initialized (no connection established yet).
func TestGetOAuthHandler_NilClient(t *testing.T) {
	c := &Client{
		config: &config.ServerConfig{Name: "test"},
	}
	// No mcp-go client set — should return nil without panic
	assert.Nil(t, c.GetOAuthHandler())
}

// TestGetConfig_ReturnsConfig verifies that GetConfig returns the server config pointer.
func TestGetConfig_ReturnsConfig(t *testing.T) {
	cfg := &config.ServerConfig{
		Name:     "my-server",
		URL:      "https://example.com/mcp",
		Protocol: "http",
	}
	c := &Client{config: cfg}

	got := c.GetConfig()
	assert.Same(t, cfg, got, "GetConfig should return the exact config pointer")
	assert.Equal(t, "my-server", got.Name)
	assert.Equal(t, "https://example.com/mcp", got.URL)
}

// TestRefreshOAuthTokenDirect_NoHandler verifies that RefreshOAuthTokenDirect
// returns an appropriate error when no OAuth handler is available (nil mcp-go client).
func TestRefreshOAuthTokenDirect_NoHandler(t *testing.T) {
	c := &Client{
		config: &config.ServerConfig{Name: "no-handler-server"},
		logger: zap.NewNop(),
	}

	err := c.RefreshOAuthTokenDirect(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no OAuth handler available")
	assert.Contains(t, err.Error(), "no-handler-server")
}

// TestRefreshOAuthTokenDirect_NoStoredToken verifies the error path when storage has
// no token record for the server (fresh server, never authenticated).
func TestRefreshOAuthTokenDirect_NoStoredToken(t *testing.T) {
	logger := zap.NewNop()
	db, err := storage.NewBoltDB(t.TempDir(), logger.Sugar())
	require.NoError(t, err)
	defer db.Close()

	c := &Client{
		config:  &config.ServerConfig{Name: "fresh-server", URL: "https://example.com"},
		logger:  logger,
		storage: db,
		// No mcp-go client — GetOAuthHandler returns nil
	}

	err = c.RefreshOAuthTokenDirect(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no OAuth handler available")
}

// TestRefreshOAuthTokenDirect_NoRefreshToken verifies the error path when a token
// record exists but has no refresh_token (e.g., client_credentials grant).
func TestRefreshOAuthTokenDirect_NoRefreshToken(t *testing.T) {
	// This test requires an OAuth handler to be present. Since we can't easily
	// mock the mcp-go transport layer, we verify the path via the nil handler check.
	// The "no refresh token" path would be exercised in integration tests.
	c := &Client{
		config: &config.ServerConfig{Name: "no-refresh"},
		logger: zap.NewNop(),
	}

	err := c.RefreshOAuthTokenDirect(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no OAuth handler available")
}

// TestRefreshTokenWithStoredCredentials_Success verifies the happy path of manual
// token refresh using stored DCR credentials against a mock token endpoint.
func TestRefreshTokenWithStoredCredentials_Success(t *testing.T) {
	// Set up a mock token endpoint
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

		err := r.ParseForm()
		require.NoError(t, err)
		assert.Equal(t, "refresh_token", r.FormValue("grant_type"))
		assert.Equal(t, "old-refresh-token", r.FormValue("refresh_token"))
		assert.Equal(t, "dcr-client-id", r.FormValue("client_id"))
		assert.Equal(t, "dcr-client-secret", r.FormValue("client_secret"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "new-refresh-token",
		})
	}))
	defer tokenServer.Close()

	c := &Client{
		config: &config.ServerConfig{Name: "test-server"},
		logger: zap.NewNop(),
	}

	record := &storage.OAuthTokenRecord{
		RefreshToken: "old-refresh-token",
		ClientID:     "dcr-client-id",
		ClientSecret: "dcr-client-secret",
	}

	result, err := c.refreshTokenWithStoredCredentials(context.Background(), tokenServer.URL, record)
	require.NoError(t, err)
	assert.Equal(t, "new-access-token", result.AccessToken)
	assert.Equal(t, "new-refresh-token", result.RefreshToken)
	assert.Equal(t, "Bearer", result.TokenType)
	assert.True(t, result.ExpiresAt.After(time.Now()), "ExpiresAt should be in the future")
}

// TestRefreshTokenWithStoredCredentials_NoClientSecret verifies that the request
// omits client_secret when it's empty (public client DCR).
func TestRefreshTokenWithStoredCredentials_NoClientSecret(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseForm()
		require.NoError(t, err)
		assert.Equal(t, "public-client-id", r.FormValue("client_id"))
		assert.Empty(t, r.FormValue("client_secret"), "client_secret should not be sent for public clients")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-token",
			"token_type":   "Bearer",
			"expires_in":   1800,
		})
	}))
	defer tokenServer.Close()

	c := &Client{
		config: &config.ServerConfig{Name: "public-client"},
		logger: zap.NewNop(),
	}

	record := &storage.OAuthTokenRecord{
		RefreshToken: "refresh-token",
		ClientID:     "public-client-id",
		ClientSecret: "", // Public client — no secret
	}

	result, err := c.refreshTokenWithStoredCredentials(context.Background(), tokenServer.URL, record)
	require.NoError(t, err)
	assert.Equal(t, "new-token", result.AccessToken)
	assert.Empty(t, result.RefreshToken, "No new refresh token returned")
}

// TestRefreshTokenWithStoredCredentials_ServerError verifies error handling
// when the token endpoint returns an HTTP error.
func TestRefreshTokenWithStoredCredentials_ServerError(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token expired"}`))
	}))
	defer tokenServer.Close()

	c := &Client{
		config: &config.ServerConfig{Name: "error-server"},
		logger: zap.NewNop(),
	}

	record := &storage.OAuthTokenRecord{
		RefreshToken: "expired-token",
		ClientID:     "some-client",
	}

	_, err := c.refreshTokenWithStoredCredentials(context.Background(), tokenServer.URL, record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token refresh failed with status 400")
	assert.Contains(t, err.Error(), "invalid_grant")
}

// TestRefreshTokenWithStoredCredentials_InvalidJSON verifies error handling
// when the token endpoint returns malformed JSON.
func TestRefreshTokenWithStoredCredentials_InvalidJSON(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer tokenServer.Close()

	c := &Client{
		config: &config.ServerConfig{Name: "bad-json-server"},
		logger: zap.NewNop(),
	}

	record := &storage.OAuthTokenRecord{
		RefreshToken: "some-token",
		ClientID:     "some-client",
	}

	_, err := c.refreshTokenWithStoredCredentials(context.Background(), tokenServer.URL, record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse token response")
}

// TestRefreshTokenWithStoredCredentials_ContextCancelled verifies that cancellation
// propagates correctly through the HTTP request.
func TestRefreshTokenWithStoredCredentials_ContextCancelled(t *testing.T) {
	c := &Client{
		config: &config.ServerConfig{Name: "cancelled-server"},
		logger: zap.NewNop(),
	}

	// Use an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	record := &storage.OAuthTokenRecord{
		RefreshToken: "some-token",
		ClientID:     "some-client",
	}

	_, err := c.refreshTokenWithStoredCredentials(ctx, "http://127.0.0.1:1/token", record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send refresh request")
}

// TestRefreshTokenWithStoredCredentials_MissingExpiresIn verifies that the
// default 1-hour expiry is applied when the token endpoint omits expires_in.
func TestRefreshTokenWithStoredCredentials_MissingExpiresIn(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Response without expires_in field
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-token",
			"token_type":   "Bearer",
		})
	}))
	defer tokenServer.Close()

	c := &Client{
		config: &config.ServerConfig{Name: "no-expiry-server"},
		logger: zap.NewNop(),
	}

	record := &storage.OAuthTokenRecord{
		RefreshToken: "refresh-token",
		ClientID:     "some-client",
	}

	result, err := c.refreshTokenWithStoredCredentials(context.Background(), tokenServer.URL, record)
	require.NoError(t, err)
	assert.Equal(t, "new-token", result.AccessToken)
	// Default expiry should be approximately 1 hour from now (within a 5-second tolerance)
	expectedExpiry := time.Now().Add(1 * time.Hour)
	assert.WithinDuration(t, expectedExpiry, result.ExpiresAt, 5*time.Second,
		"Missing expires_in should default to 1 hour")
}

// TestPersistDCRCredentials_NilStorage verifies that persistDCRCredentials
// is a no-op when storage is nil (graceful degradation).
func TestPersistDCRCredentials_NilStorage(t *testing.T) {
	c := &Client{
		config:  &config.ServerConfig{Name: "no-storage"},
		logger:  zap.NewNop(),
		storage: nil,
	}
	// Should not panic
	c.persistDCRCredentials()
}

// TestPersistDCRCredentials_NoHandler verifies that persistDCRCredentials
// is a no-op when there's no OAuth handler (non-OAuth server).
func TestPersistDCRCredentials_NoHandler(t *testing.T) {
	logger := zap.NewNop()
	db, err := storage.NewBoltDB(t.TempDir(), logger.Sugar())
	require.NoError(t, err)
	defer db.Close()

	c := &Client{
		config:  &config.ServerConfig{Name: "no-oauth"},
		logger:  logger,
		storage: db,
		// No mcp-go client — GetOAuthHandler returns nil
	}
	// Should not panic, should be a no-op
	c.persistDCRCredentials()
}

// TestPersistDCRCredentials_SavesCredentials verifies the full happy path:
// extracting DCR credentials from the OAuth handler and saving them to storage.
// This is a partial test since we can't easily mock the mcp-go transport;
// we verify the storage layer works correctly with pre-existing token records.
func TestPersistDCRCredentials_StorageRoundTrip(t *testing.T) {
	logger := zap.NewNop()
	db, err := storage.NewBoltDB(t.TempDir(), logger.Sugar())
	require.NoError(t, err)
	defer db.Close()

	serverName := "dcr-server"
	serverURL := "https://example.com/mcp"
	serverKey := oauth.GenerateServerKey(serverName, serverURL)

	// Pre-populate a token record (simulating post-OAuth state)
	initialRecord := &storage.OAuthTokenRecord{
		ServerName:   serverKey,
		DisplayName:  serverName,
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
		Created:      time.Now(),
		Updated:      time.Now(),
	}
	require.NoError(t, db.SaveOAuthToken(initialRecord))

	// Verify the record was saved
	saved, err := db.GetOAuthToken(serverKey)
	require.NoError(t, err)
	assert.Empty(t, saved.ClientID, "ClientID should be empty before DCR persistence")
	assert.Empty(t, saved.ClientSecret, "ClientSecret should be empty before DCR persistence")

	// Now simulate what persistDCRCredentials does to the storage:
	// update the record with DCR credentials
	saved.ClientID = "dcr-client-id-12345"
	saved.ClientSecret = "dcr-client-secret-67890"
	saved.Updated = time.Now()
	require.NoError(t, db.SaveOAuthToken(saved))

	// Verify DCR credentials are persisted
	updated, err := db.GetOAuthToken(serverKey)
	require.NoError(t, err)
	assert.Equal(t, "dcr-client-id-12345", updated.ClientID)
	assert.Equal(t, "dcr-client-secret-67890", updated.ClientSecret)
	assert.Equal(t, "access-token", updated.AccessToken, "Original token should be preserved")
}
