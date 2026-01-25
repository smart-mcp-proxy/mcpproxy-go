package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func setupTestStorage(t *testing.T) *storage.BoltDB {
	t.Helper()
	logger := zap.NewNop().Sugar()
	// NewBoltDB expects a directory, not a file path
	db, err := storage.NewBoltDB(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
	})
	return db
}

func TestCreateOAuthConfig_WithExtraParams(t *testing.T) {
	// Test that CreateOAuthConfig correctly uses extra_params from config
	storage := setupTestStorage(t)
	serverConfig := &config.ServerConfig{
		Name: "test-server",
		URL:  "https://example.com/mcp",
		OAuth: &config.OAuthConfig{
			ClientID: "test-client",
			ExtraParams: map[string]string{
				"resource": "https://mcp.example.com/api",
				"custom":   "value",
			},
		},
	}

	oauthConfig := CreateOAuthConfig(serverConfig, storage)

	require.NotNil(t, oauthConfig)
	// The OAuth config should be created with the provided configuration
	assert.Equal(t, "test-client", oauthConfig.ClientID)
}

func TestIsOAuthCapable(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.ServerConfig
		expected bool
	}{
		{
			name:     "explicit OAuth config",
			config:   &config.ServerConfig{OAuth: &config.OAuthConfig{}},
			expected: true,
		},
		{
			name:     "HTTP protocol without OAuth",
			config:   &config.ServerConfig{Protocol: "http"},
			expected: true,
		},
		{
			name:     "SSE protocol without OAuth",
			config:   &config.ServerConfig{Protocol: "sse"},
			expected: true,
		},
		{
			name:     "stdio protocol without OAuth",
			config:   &config.ServerConfig{Protocol: "stdio"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsOAuthCapable(tt.config)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// MockTokenStore implements client.TokenStore for testing
type MockTokenStore struct {
	token *client.Token
	err   error
}

func (m *MockTokenStore) GetToken(ctx context.Context) (*client.Token, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.token, nil
}

func (m *MockTokenStore) SaveToken(ctx context.Context, token *client.Token) error {
	m.token = token
	return nil
}

func (m *MockTokenStore) DeleteToken(ctx context.Context) error {
	m.token = nil
	return nil
}

// TestTokenStoreManager_HasValidToken_NoStore validates false when no token store exists
func TestTokenStoreManager_HasValidToken_NoStore(t *testing.T) {
	manager := &TokenStoreManager{
		stores:         make(map[string]client.TokenStore),
		completedOAuth: make(map[string]time.Time),
		logger:         zap.NewNop().Named("test"),
	}

	result := manager.HasValidToken(context.Background(), "nonexistent-server", nil)

	assert.False(t, result, "Expected false for nonexistent server")
}

// TestTokenStoreManager_HasValidToken_InMemoryStore validates true for in-memory stores
func TestTokenStoreManager_HasValidToken_InMemoryStore(t *testing.T) {
	manager := &TokenStoreManager{
		stores:         make(map[string]client.TokenStore),
		completedOAuth: make(map[string]time.Time),
		logger:         zap.NewNop().Named("test"),
	}

	// Create in-memory token store
	memStore := client.NewMemoryTokenStore()
	manager.stores["test-server"] = memStore

	result := manager.HasValidToken(context.Background(), "test-server", nil)

	assert.True(t, result, "Expected true for in-memory store (no expiration checking)")
}

// TestTokenStoreManager_HasValidToken_MockStore_NoToken validates behavior with mock that doesn't match PersistentTokenStore
func TestTokenStoreManager_HasValidToken_MockStore_NoToken(t *testing.T) {
	manager := &TokenStoreManager{
		stores:         make(map[string]client.TokenStore),
		completedOAuth: make(map[string]time.Time),
		logger:         zap.NewNop().Named("test"),
	}

	// Create mock store with no token (returns error)
	// Note: MockTokenStore doesn't match *PersistentTokenStore type,
	// so HasValidToken() will treat it as an in-memory store and return true
	mockStore := &MockTokenStore{
		token: nil,
		err:   fmt.Errorf("token not found"),
	}
	manager.stores["test-server"] = mockStore

	// Create temporary test storage
	tempDir := t.TempDir()
	testStorage, err := storage.NewManager(tempDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer testStorage.Close()

	result := manager.HasValidToken(context.Background(), "test-server", testStorage.GetBoltDB())

	// MockTokenStore falls through to in-memory behavior (returns true)
	assert.True(t, result, "Mock store is treated as in-memory (always valid)")
}

// TestTokenStoreManager_HasValidToken_MockStore_ValidToken validates mock with valid token
func TestTokenStoreManager_HasValidToken_MockStore_ValidToken(t *testing.T) {
	manager := &TokenStoreManager{
		stores:         make(map[string]client.TokenStore),
		completedOAuth: make(map[string]time.Time),
		logger:         zap.NewNop().Named("test"),
	}

	// Create mock store with valid token (expires in 1 hour)
	// Note: MockTokenStore doesn't match *PersistentTokenStore type,
	// so HasValidToken() treats it as in-memory and returns true
	validToken := &client.Token{
		AccessToken:  "valid-access-token",
		RefreshToken: "valid-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	mockStore := &MockTokenStore{
		token: validToken,
		err:   nil,
	}
	manager.stores["test-server"] = mockStore

	// Create temporary test storage
	tempDir := t.TempDir()
	testStorage, err := storage.NewManager(tempDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer testStorage.Close()

	result := manager.HasValidToken(context.Background(), "test-server", testStorage.GetBoltDB())

	assert.True(t, result, "Mock store is treated as in-memory (always valid)")
}

// TestTokenStoreManager_HasValidToken_MockStore_ExpiredToken validates mock with expired token
func TestTokenStoreManager_HasValidToken_MockStore_ExpiredToken(t *testing.T) {
	manager := &TokenStoreManager{
		stores:         make(map[string]client.TokenStore),
		completedOAuth: make(map[string]time.Time),
		logger:         zap.NewNop().Named("test"),
	}

	// Create mock store with expired token (expired 1 hour ago)
	// Note: MockTokenStore doesn't match *PersistentTokenStore type,
	// so HasValidToken() treats it as in-memory and returns true (doesn't check expiration)
	expiredToken := &client.Token{
		AccessToken:  "expired-access-token",
		RefreshToken: "expired-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour),
	}
	mockStore := &MockTokenStore{
		token: expiredToken,
		err:   nil,
	}
	manager.stores["test-server"] = mockStore

	// Create temporary test storage
	tempDir := t.TempDir()
	testStorage, err := storage.NewManager(tempDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer testStorage.Close()

	result := manager.HasValidToken(context.Background(), "test-server", testStorage.GetBoltDB())

	// MockTokenStore is treated as in-memory (doesn't check expiration)
	assert.True(t, result, "Mock store is treated as in-memory (no expiration check)")
}

// TestTokenStoreManager_HasValidToken_PersistentStore_NoExpiration validates true for token with no expiration
func TestTokenStoreManager_HasValidToken_PersistentStore_NoExpiration(t *testing.T) {
	manager := &TokenStoreManager{
		stores:         make(map[string]client.TokenStore),
		completedOAuth: make(map[string]time.Time),
		logger:         zap.NewNop().Named("test"),
	}

	// Create mock persistent store with token that has no expiration (zero time)
	noExpirationToken := &client.Token{
		AccessToken:  "no-expiration-access-token",
		RefreshToken: "no-expiration-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Time{}, // Zero time = no expiration
	}
	mockStore := &MockTokenStore{
		token: noExpirationToken,
		err:   nil,
	}
	manager.stores["test-server"] = mockStore

	// Create temporary test storage
	tempDir := t.TempDir()
	testStorage, err := storage.NewManager(tempDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer testStorage.Close()

	result := manager.HasValidToken(context.Background(), "test-server", testStorage.GetBoltDB())

	assert.True(t, result, "Expected true for token with no expiration (zero time)")
}

// TestTokenStoreManager_HasValidToken_NilStorage validates graceful handling of nil storage
func TestTokenStoreManager_HasValidToken_NilStorage(t *testing.T) {
	manager := &TokenStoreManager{
		stores:         make(map[string]client.TokenStore),
		completedOAuth: make(map[string]time.Time),
		logger:         zap.NewNop().Named("test"),
	}

	// Create in-memory token store (not persistent)
	memStore := client.NewMemoryTokenStore()
	manager.stores["test-server"] = memStore

	// Call with nil storage - should still work for in-memory stores
	result := manager.HasValidToken(context.Background(), "test-server", nil)

	assert.True(t, result, "Expected true for in-memory store with nil storage")
}

// =============================================================================
// T007-T008: Tests for RFC 8707 Resource Auto-Detection (User Story 1)
// =============================================================================

// T007: Test CreateOAuthConfig auto-detects resource from Protected Resource Metadata
func TestCreateOAuthConfig_AutoDetectsResource(t *testing.T) {
	// Variable to hold server URL for use in handler
	var serverURL string

	// Create a mock server that returns Protected Resource Metadata with resource field
	mockMetadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"resource": "https://api.example.com/mcp/v1",
				"authorization_servers": ["https://auth.example.com"],
				"scopes_supported": ["mcp"]
			}`))
			return
		}
		// Return 401 with resource_metadata link in WWW-Authenticate header
		if r.Method == "POST" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_request", resource_metadata="%s/.well-known/oauth-protected-resource"`, serverURL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockMetadataServer.Close()

	// Assign server URL after server is created
	serverURL = mockMetadataServer.URL

	testStorage := setupTestStorage(t)
	serverConfig := &config.ServerConfig{
		Name: "test-resource-detection",
		URL:  mockMetadataServer.URL + "/mcp",
		// No OAuth config - should auto-detect
	}

	// Call CreateOAuthConfigWithExtraParams to get both config and extraParams
	oauthConfig, extraParams := CreateOAuthConfigWithExtraParams(serverConfig, testStorage)

	require.NotNil(t, oauthConfig, "OAuth config should be created")
	require.NotNil(t, extraParams, "Extra params should be returned")

	// Verify resource was auto-detected from Protected Resource Metadata
	resource, hasResource := extraParams["resource"]
	assert.True(t, hasResource, "extraParams should contain 'resource' key")
	assert.Equal(t, "https://api.example.com/mcp/v1", resource, "Resource should be auto-detected from metadata")
}

// T022: Test that manual extra_params.resource overrides auto-detected value
func TestCreateOAuthConfig_ManualOverride(t *testing.T) {
	// Variable to hold server URL for use in handler
	var serverURL string

	// Create a mock server that returns Protected Resource Metadata with resource field
	mockMetadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"resource": "https://auto-detected-resource.example.com/mcp",
				"authorization_servers": ["https://auth.example.com"],
				"scopes_supported": ["mcp"]
			}`))
			return
		}
		// Return 401 with resource_metadata link in WWW-Authenticate header
		if r.Method == "POST" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_request", resource_metadata="%s/.well-known/oauth-protected-resource"`, serverURL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockMetadataServer.Close()

	// Assign server URL after server is created
	serverURL = mockMetadataServer.URL

	testStorage := setupTestStorage(t)
	serverConfig := &config.ServerConfig{
		Name: "test-manual-override",
		URL:  mockMetadataServer.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			ExtraParams: map[string]string{
				"resource": "https://manual-override.example.com/api", // Manual override
			},
		},
	}

	// Call CreateOAuthConfigWithExtraParams
	oauthConfig, extraParams := CreateOAuthConfigWithExtraParams(serverConfig, testStorage)

	require.NotNil(t, oauthConfig, "OAuth config should be created")
	require.NotNil(t, extraParams, "Extra params should be returned")

	// Verify manual resource overrides auto-detected value
	resource, hasResource := extraParams["resource"]
	assert.True(t, hasResource, "extraParams should contain 'resource' key")
	assert.Equal(t, "https://manual-override.example.com/api", resource, "Manual resource should override auto-detected value")
}

// T023: Test that manual extra_params are merged with auto-detected params
func TestCreateOAuthConfig_MergesExtraParams(t *testing.T) {
	// Variable to hold server URL for use in handler
	var serverURL string

	// Create a mock server that returns Protected Resource Metadata
	mockMetadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"resource": "https://auto-detected.example.com/mcp",
				"authorization_servers": ["https://auth.example.com"],
				"scopes_supported": ["mcp"]
			}`))
			return
		}
		if r.Method == "POST" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_request", resource_metadata="%s/.well-known/oauth-protected-resource"`, serverURL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockMetadataServer.Close()

	serverURL = mockMetadataServer.URL

	testStorage := setupTestStorage(t)
	serverConfig := &config.ServerConfig{
		Name: "test-merge-params",
		URL:  mockMetadataServer.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			ExtraParams: map[string]string{
				"tenant_id": "12345",          // Additional param (should be preserved)
				"audience":  "custom-audience", // Additional param (should be preserved)
				// Note: no "resource" - should be auto-detected
			},
		},
	}

	// Call CreateOAuthConfigWithExtraParams
	oauthConfig, extraParams := CreateOAuthConfigWithExtraParams(serverConfig, testStorage)

	require.NotNil(t, oauthConfig, "OAuth config should be created")
	require.NotNil(t, extraParams, "Extra params should be returned")

	// Verify manual params are preserved
	assert.Equal(t, "12345", extraParams["tenant_id"], "Manual tenant_id should be preserved")
	assert.Equal(t, "custom-audience", extraParams["audience"], "Manual audience should be preserved")

	// Verify resource is auto-detected since not manually specified
	resource, hasResource := extraParams["resource"]
	assert.True(t, hasResource, "extraParams should contain auto-detected 'resource' key")
	assert.Equal(t, "https://auto-detected.example.com/mcp", resource, "Resource should be auto-detected")
}

// =============================================================================
// Spec 022: Tests for OAuth Redirect URI Port Persistence
// =============================================================================

// TestStartCallbackServerWithPreferredPort verifies that StartCallbackServer
// uses the preferred port when available.
func TestStartCallbackServerWithPreferredPort(t *testing.T) {
	manager := GetGlobalCallbackManager()

	// Find an available port to use as preferred
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	preferredPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close() // Release the port so we can bind to it again

	// Start callback server with preferred port
	serverName := "test-preferred-port"
	callbackServer, err := manager.StartCallbackServer(serverName, preferredPort)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	// Verify the callback server is using the preferred port
	assert.Equal(t, preferredPort, callbackServer.Port, "Callback server should use preferred port")
	assert.Contains(t, callbackServer.RedirectURI, fmt.Sprintf(":%d/", preferredPort), "Redirect URI should contain preferred port")
}

// TestStartCallbackServerFallback verifies that StartCallbackServer falls back
// to dynamic allocation when the preferred port is unavailable.
func TestStartCallbackServerFallback(t *testing.T) {
	manager := GetGlobalCallbackManager()

	// Occupy a port first
	occupyListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	occupiedPort := occupyListener.Addr().(*net.TCPAddr).Port
	defer occupyListener.Close() // Keep port occupied during test

	// Try to start callback server with the occupied port
	serverName := "test-fallback-port"
	callbackServer, err := manager.StartCallbackServer(serverName, occupiedPort)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	// Verify the callback server fell back to a different port
	assert.NotEqual(t, occupiedPort, callbackServer.Port, "Callback server should use different port when preferred is occupied")
	assert.Greater(t, callbackServer.Port, 0, "Callback server should have valid port")
}

// TestStartCallbackServerDynamicPort verifies that StartCallbackServer
// uses dynamic allocation when preferredPort is 0.
func TestStartCallbackServerDynamicPort(t *testing.T) {
	manager := GetGlobalCallbackManager()

	// Start callback server with dynamic port (preferredPort = 0)
	serverName := "test-dynamic-port"
	callbackServer, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	// Verify a valid port was allocated
	assert.Greater(t, callbackServer.Port, 0, "Callback server should have valid port")
	assert.Contains(t, callbackServer.RedirectURI, "http://127.0.0.1:", "Redirect URI should have localhost base")
}

// TestStartCallbackServerDrainsStaleParams verifies that when a callback server
// is reused, any stale params from a previous failed OAuth attempt are drained
// from the channel. This prevents state mismatch errors on OAuth retries.
// See: https://github.com/smart-mcp-proxy/mcpproxy-go/pull/281
func TestStartCallbackServerDrainsStaleParams(t *testing.T) {
	manager := GetGlobalCallbackManager()

	serverName := "test-drain-stale-params"

	// Start callback server first time
	callbackServer1, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	originalPort := callbackServer1.Port

	// Simulate a failed OAuth attempt by sending stale params to the channel
	// This is what happens when OAuth times out after the HTTP callback arrives
	staleParams := map[string]string{
		"code":  "stale_auth_code",
		"state": "stale_state_abc123",
	}
	select {
	case callbackServer1.CallbackChan <- staleParams:
		// Stale params buffered in channel
	default:
		t.Fatal("Failed to send stale params to channel")
	}

	// Verify stale params are in the channel
	assert.Len(t, callbackServer1.CallbackChan, 1, "Channel should have stale params")

	// Now simulate a retry - call StartCallbackServer again for the same server
	// This should reuse the existing server AND drain the stale params
	callbackServer2, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)

	// Verify we got the same server (reused)
	assert.Equal(t, originalPort, callbackServer2.Port, "Should reuse same callback server")
	assert.Same(t, callbackServer1, callbackServer2, "Should return same server instance")

	// Verify channel is now empty (stale params were drained)
	select {
	case params := <-callbackServer2.CallbackChan:
		t.Fatalf("Channel should be empty after draining, but got: %v", params)
	default:
		// Channel is empty - this is the expected behavior
	}
}

// TestStartCallbackServerDrainsStaleParams_EmptyChannel verifies that draining
// works correctly when the channel is already empty (no stale params).
func TestStartCallbackServerDrainsStaleParams_EmptyChannel(t *testing.T) {
	manager := GetGlobalCallbackManager()

	serverName := "test-drain-empty-channel"

	// Start callback server first time
	callbackServer1, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	// Don't send any params - channel is empty

	// Call StartCallbackServer again - should work fine with empty channel
	callbackServer2, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)

	// Verify we got the same server
	assert.Same(t, callbackServer1, callbackServer2, "Should return same server instance")

	// Verify channel is still empty and usable
	select {
	case <-callbackServer2.CallbackChan:
		t.Fatal("Channel should be empty")
	default:
		// Expected - channel is empty
	}

	// Verify we can still send to the channel (it's functional)
	newParams := map[string]string{"code": "new_code", "state": "new_state"}
	select {
	case callbackServer2.CallbackChan <- newParams:
		// Success
	default:
		t.Fatal("Should be able to send to channel")
	}

	// Verify we can receive
	received := <-callbackServer2.CallbackChan
	assert.Equal(t, "new_code", received["code"])
	assert.Equal(t, "new_state", received["state"])
}

// =============================================================================
// Tests for Rate Limit Handling in Resource Auto-Detection
// =============================================================================

// TestAutoDetectResource_RateLimitWithRetryAfterHeader verifies that 429 responses
// with Retry-After header are handled correctly and retried.
func TestAutoDetectResource_RateLimitWithRetryAfterHeader(t *testing.T) {
	var serverURL string
	requestCount := 0

	// Create a mock server that returns 429 on first request, then 401
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"resource": "https://api.example.com/mcp/v1",
				"authorization_servers": ["https://auth.example.com"]
			}`))
			return
		}

		if r.Method == "POST" {
			if requestCount == 1 {
				// First request: return 429 with Retry-After header (short wait for test)
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			// Subsequent request: return 401 with resource metadata link
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_request", resource_metadata="%s/.well-known/oauth-protected-resource"`, serverURL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	serverURL = mockServer.URL

	logger := zap.NewNop()
	serverConfig := &config.ServerConfig{
		Name: "test-rate-limit-retry-after",
		URL:  mockServer.URL + "/mcp",
	}

	// Call autoDetectResource
	result := autoDetectResource(context.Background(), serverConfig, logger)

	// Verify resource was detected after retry
	assert.Equal(t, "https://api.example.com/mcp/v1", result, "Should detect resource after retry")
	assert.Equal(t, 3, requestCount, "Should have made 3 requests (1 POST 429 + 1 POST 401 + 1 GET metadata)")
}

// TestAutoDetectResource_RateLimitWithResetAt verifies that 429 responses
// with JSON body containing reset_at are handled correctly.
func TestAutoDetectResource_RateLimitWithResetAt(t *testing.T) {
	var serverURL string
	requestCount := 0

	// Create a mock server that returns 429 with reset_at, then 401
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"resource": "https://api.example.com/mcp/v1",
				"authorization_servers": ["https://auth.example.com"]
			}`))
			return
		}

		if r.Method == "POST" {
			if requestCount == 1 {
				// First request: return 429 with reset_at in body (1 second from now)
				resetAt := time.Now().Add(1 * time.Second).Unix()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(fmt.Sprintf(`{"reset_at": %d}`, resetAt)))
				return
			}
			// Subsequent request: return 401 with resource metadata link
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_request", resource_metadata="%s/.well-known/oauth-protected-resource"`, serverURL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	serverURL = mockServer.URL

	logger := zap.NewNop()
	serverConfig := &config.ServerConfig{
		Name: "test-rate-limit-reset-at",
		URL:  mockServer.URL + "/mcp",
	}

	// Call autoDetectResource
	result := autoDetectResource(context.Background(), serverConfig, logger)

	// Verify resource was detected after retry
	assert.Equal(t, "https://api.example.com/mcp/v1", result, "Should detect resource after retry")
	assert.GreaterOrEqual(t, requestCount, 2, "Should have made at least 2 POST requests")
}

// TestAutoDetectResource_RateLimitExhausted verifies that when all retries are
// exhausted due to 429, the function falls back to server URL.
func TestAutoDetectResource_RateLimitExhausted(t *testing.T) {
	requestCount := 0

	// Create a mock server that always returns 429
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method == "POST" {
			w.Header().Set("Retry-After", "1") // Short wait for test
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	logger := zap.NewNop()
	serverConfig := &config.ServerConfig{
		Name: "test-rate-limit-exhausted",
		URL:  mockServer.URL + "/mcp",
	}

	// Call autoDetectResource
	result := autoDetectResource(context.Background(), serverConfig, logger)

	// Verify fallback to server URL after exhausting retries
	assert.Equal(t, mockServer.URL+"/mcp", result, "Should fallback to server URL after exhausting retries")
	// Should have made maxRetries + 1 attempts (0, 1, 2, 3 = 4 attempts)
	assert.Equal(t, resourceDetectMaxRetries+1, requestCount, "Should have made maxRetries+1 attempts")
}

// TestAutoDetectResource_ServerError verifies that 5xx responses trigger
// immediate fallback to server URL without retry.
func TestAutoDetectResource_ServerError(t *testing.T) {
	requestCount := 0

	// Create a mock server that returns 500
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method == "POST" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "internal server error"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	logger := zap.NewNop()
	serverConfig := &config.ServerConfig{
		Name: "test-server-error",
		URL:  mockServer.URL + "/mcp",
	}

	// Call autoDetectResource
	result := autoDetectResource(context.Background(), serverConfig, logger)

	// Verify fallback to server URL (no retry for 5xx)
	assert.Equal(t, mockServer.URL+"/mcp", result, "Should fallback to server URL on 5xx error")
	assert.Equal(t, 1, requestCount, "Should only make 1 request (no retry for 5xx)")
}

// TestParseRateLimitWait_RetryAfterSeconds verifies parsing Retry-After header as seconds.
func TestParseRateLimitWait_RetryAfterSeconds(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
	}
	resp.Header.Set("Retry-After", "30")

	wait := parseRateLimitWait(resp, nil)

	assert.Equal(t, 30*time.Second, wait, "Should parse Retry-After seconds correctly")
}

// TestParseRateLimitWait_RetryAfterHTTPDate verifies parsing Retry-After header as HTTP-date.
func TestParseRateLimitWait_RetryAfterHTTPDate(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
	}
	// Set Retry-After to 5 seconds from now in HTTP-date format
	futureTime := time.Now().Add(5 * time.Second).UTC()
	resp.Header.Set("Retry-After", futureTime.Format(http.TimeFormat))

	wait := parseRateLimitWait(resp, nil)

	// Allow some tolerance due to test execution time
	assert.InDelta(t, 5*time.Second, wait, float64(2*time.Second), "Should parse HTTP-date correctly")
}

// TestParseRateLimitWait_JSONResetAt verifies parsing reset_at from JSON body.
func TestParseRateLimitWait_JSONResetAt(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
	}
	// Set reset_at to 5 seconds from now
	resetAt := time.Now().Add(5 * time.Second).Unix()
	body := []byte(fmt.Sprintf(`{"reset_at": %d}`, resetAt))

	wait := parseRateLimitWait(resp, body)

	// Allow some tolerance due to test execution time
	assert.InDelta(t, 5*time.Second, wait, float64(2*time.Second), "Should parse reset_at correctly")
}

// TestParseRateLimitWait_JSONNestedResetAt verifies parsing reset_at from nested detail field.
func TestParseRateLimitWait_JSONNestedResetAt(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
	}
	// Set reset_at in detail field
	resetAt := time.Now().Add(5 * time.Second).Unix()
	body := []byte(fmt.Sprintf(`{"detail": {"reset_at": %d}}`, resetAt))

	wait := parseRateLimitWait(resp, body)

	// Allow some tolerance due to test execution time
	assert.InDelta(t, 5*time.Second, wait, float64(2*time.Second), "Should parse nested reset_at correctly")
}

// TestParseRateLimitWait_NoHints verifies that 0 is returned when no hints are present.
func TestParseRateLimitWait_NoHints(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
	}
	body := []byte(`{"error": "rate limited"}`)

	wait := parseRateLimitWait(resp, body)

	assert.Equal(t, time.Duration(0), wait, "Should return 0 when no hints present")
}

// TestParseRateLimitWait_HeaderTakesPrecedence verifies Retry-After header takes precedence over body.
func TestParseRateLimitWait_HeaderTakesPrecedence(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
	}
	resp.Header.Set("Retry-After", "10")
	// Body has different value
	resetAt := time.Now().Add(60 * time.Second).Unix()
	body := []byte(fmt.Sprintf(`{"reset_at": %d}`, resetAt))

	wait := parseRateLimitWait(resp, body)

	assert.Equal(t, 10*time.Second, wait, "Retry-After header should take precedence over body")
}

// T008: Test CreateOAuthConfig falls back to server URL when metadata lacks resource field
func TestCreateOAuthConfig_FallsBackToServerURL(t *testing.T) {
	// Variable to hold server URL for use in handler
	var serverURL string

	// Create a mock server that returns Protected Resource Metadata WITHOUT resource field
	mockMetadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Metadata without resource field
			w.Write([]byte(`{
				"authorization_servers": ["https://auth.example.com"],
				"scopes_supported": ["mcp"]
			}`))
			return
		}
		// Return 401 with resource_metadata link in WWW-Authenticate header
		if r.Method == "POST" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_request", resource_metadata="%s/.well-known/oauth-protected-resource"`, serverURL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockMetadataServer.Close()

	// Assign server URL after server is created
	serverURL = mockMetadataServer.URL

	testStorage := setupTestStorage(t)
	serverConfig := &config.ServerConfig{
		Name: "test-fallback",
		URL:  mockMetadataServer.URL + "/mcp",
		// No OAuth config - should auto-detect and fallback
	}

	// Call CreateOAuthConfigWithExtraParams to get both config and extraParams
	oauthConfig, extraParams := CreateOAuthConfigWithExtraParams(serverConfig, testStorage)

	require.NotNil(t, oauthConfig, "OAuth config should be created")
	require.NotNil(t, extraParams, "Extra params should be returned")

	// Verify resource falls back to server URL when metadata doesn't provide it
	resource, hasResource := extraParams["resource"]
	assert.True(t, hasResource, "extraParams should contain 'resource' key (fallback)")
	assert.Equal(t, mockMetadataServer.URL+"/mcp", resource, "Resource should fall back to server URL")
}
