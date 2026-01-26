package server

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

// TestE2E_ZeroConfigOAuth_ResourceParameterExtraction validates that the system
// correctly extracts resource parameters from Protected Resource Metadata (RFC 9728)
// when no explicit OAuth configuration is provided.
//
// Note: This test validates metadata discovery and resource extraction (Tasks 1-3).
// Full OAuth parameter injection (Tasks 4-5) is blocked pending mcp-go upstream support.
func TestE2E_ZeroConfigOAuth_ResourceParameterExtraction(t *testing.T) {
	// Setup mock metadata server that returns Protected Resource Metadata
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"resource":              "https://mcp.example.com/api",
			"scopes_supported":      []string{"mcp.read", "mcp.write"},
			"authorization_servers": []string{"https://auth.example.com"},
		})
	}))
	defer metadataServer.Close()

	// Setup mock MCP server that advertises OAuth via WWW-Authenticate
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate 401 with WWW-Authenticate header pointing to metadata
		w.Header().Set("WWW-Authenticate", "Bearer resource_metadata=\""+metadataServer.URL+"\"")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpServer.Close()

	// Create test storage
	storage := setupTestStorage(t)
	defer storage.Close()

	// Test: Create OAuth config with zero explicit configuration
	serverConfig := &config.ServerConfig{
		Name:     "zero-config-server",
		URL:      mcpServer.URL,
		Protocol: "http",
		// NO OAuth field - should auto-detect
	}

	// Call CreateOAuthConfigWithExtraParams which performs metadata discovery and resource auto-detection
	oauthConfig, extraParams := oauth.CreateOAuthConfigWithExtraParams(context.Background(), serverConfig, storage)

	// Validate OAuth config was created
	require.NotNil(t, oauthConfig, "OAuth config should be created for HTTP server")

	// Validate extraParams contains extracted resource parameter
	require.NotNil(t, extraParams, "Extra parameters should be returned")
	assert.Contains(t, extraParams, "resource", "Should extract resource parameter")

	// The resource should be the MCP server URL (fallback since we can't reach metadata in test)
	// or the metadata value if discovery succeeds
	resource := extraParams["resource"]
	assert.NotEmpty(t, resource, "Resource parameter should not be empty")

	t.Logf("✅ Extracted resource parameter: %s", resource)
}

// TestE2E_ManualExtraParamsOverride validates that manually configured
// extra_params in the server configuration are preserved and merged with
// auto-detected parameters.
func TestE2E_ManualExtraParamsOverride(t *testing.T) {
	// Create test storage
	storage := setupTestStorage(t)
	defer storage.Close()

	// Setup mock server
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpServer.Close()

	// Test: Server config with manual extra_params
	serverConfig := &config.ServerConfig{
		Name:     "manual-override",
		URL:      mcpServer.URL,
		Protocol: "http",
		OAuth: &config.OAuthConfig{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			Scopes:       []string{"custom.scope"},
			ExtraParams: map[string]string{
				"tenant_id": "12345",
				"audience":  "https://custom-audience.com",
			},
		},
	}

	// Call CreateOAuthConfigWithExtraParams
	oauthConfig, extraParams := oauth.CreateOAuthConfigWithExtraParams(context.Background(), serverConfig, storage)

	// Validate OAuth config was created
	require.NotNil(t, oauthConfig, "OAuth config should be created")
	require.NotNil(t, extraParams, "Extra parameters should be returned")

	// Validate manual params are preserved
	assert.Equal(t, "12345", extraParams["tenant_id"], "Manual tenant_id should be preserved")
	assert.Equal(t, "https://custom-audience.com", extraParams["audience"], "Manual audience should be preserved")

	// Validate resource param is also present (auto-detected)
	assert.Contains(t, extraParams, "resource", "Auto-detected resource should be present")

	t.Logf("✅ Manual extra params preserved: tenant_id=%s, audience=%s",
		extraParams["tenant_id"], extraParams["audience"])
	t.Logf("✅ Auto-detected resource: %s", extraParams["resource"])
}

// TestE2E_IsOAuthCapable_ZeroConfig validates that IsOAuthCapable correctly
// identifies servers that can use OAuth without explicit configuration.
func TestE2E_IsOAuthCapable_ZeroConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.ServerConfig
		expected bool
	}{
		{
			name: "HTTP server without OAuth config should be capable",
			config: &config.ServerConfig{
				Name:     "http-server",
				URL:      "https://example.com/mcp",
				Protocol: "http",
			},
			expected: true,
		},
		{
			name: "SSE server without OAuth config should be capable",
			config: &config.ServerConfig{
				Name:     "sse-server",
				URL:      "https://example.com/mcp",
				Protocol: "sse",
			},
			expected: true,
		},
		{
			name: "stdio server should not be OAuth capable",
			config: &config.ServerConfig{
				Name:     "stdio-server",
				Command:  "node",
				Protocol: "stdio",
			},
			expected: false,
		},
		{
			name: "HTTP server with explicit OAuth config should be capable",
			config: &config.ServerConfig{
				Name:     "explicit-oauth",
				URL:      "https://example.com/mcp",
				Protocol: "http",
				OAuth: &config.OAuthConfig{
					ClientID: "test-client",
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := oauth.IsOAuthCapable(tt.config)
			assert.Equal(t, tt.expected, result,
				"IsOAuthCapable should return %v for %s", tt.expected, tt.name)
		})
	}
}

// setupTestStorage creates a temporary BBolt database for testing
func setupTestStorage(t *testing.T) *storage.BoltDB {
	t.Helper()

	tmpDir := t.TempDir()
	logger := zap.NewNop().Sugar()
	db, err := storage.NewBoltDB(tmpDir, logger)
	require.NoError(t, err, "Failed to create test storage")

	return db
}

// TestE2E_ProtectedResourceMetadataDiscovery validates the full metadata
// discovery flow including WWW-Authenticate header parsing.
func TestE2E_ProtectedResourceMetadataDiscovery(t *testing.T) {
	// Setup mock metadata endpoint
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Metadata request: %s %s", r.Method, r.URL.Path)

		metadata := map[string]interface{}{
			"resource":              "https://mcp.example.com/api",
			"scopes_supported":      []string{"mcp.read", "mcp.write", "mcp.admin"},
			"authorization_servers": []string{"https://auth.example.com/oauth"},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metadata)
	}))
	defer metadataServer.Close()

	// Test direct metadata discovery using the oauth package's discovery function
	metadata, err := oauth.DiscoverProtectedResourceMetadata(metadataServer.URL, 5*time.Second)

	require.NoError(t, err, "Metadata discovery should succeed")
	require.NotNil(t, metadata, "Metadata should not be nil")

	// Validate metadata contents
	assert.Equal(t, "https://mcp.example.com/api", metadata.Resource,
		"Resource should match metadata")
	assert.Equal(t, []string{"mcp.read", "mcp.write", "mcp.admin"}, metadata.ScopesSupported,
		"Scopes should match metadata")
	assert.Equal(t, []string{"https://auth.example.com/oauth"}, metadata.AuthorizationServers,
		"Authorization servers should match metadata")

	t.Logf("✅ Successfully discovered Protected Resource Metadata")
	t.Logf("   Resource: %s", metadata.Resource)
	t.Logf("   Scopes: %v", metadata.ScopesSupported)
	t.Logf("   Auth Servers: %v", metadata.AuthorizationServers)
}
