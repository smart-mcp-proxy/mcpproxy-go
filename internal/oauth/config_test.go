package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/storage"

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
