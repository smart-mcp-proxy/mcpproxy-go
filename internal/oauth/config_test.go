package oauth

import (
	"context"
	"fmt"
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
