package contracts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertGenericServersToTyped_OAuth verifies OAuth config is properly extracted
func TestConvertGenericServersToTyped_OAuth(t *testing.T) {
	// Simulate the map structure returned from the management service
	genericServers := []map[string]interface{}{
		{
			"id":            "sentry",
			"name":          "sentry",
			"url":           "https://mcp.sentry.dev/mcp",
			"protocol":      "http",
			"enabled":       true,
			"quarantined":   false,
			"connected":     false,
			"status":        "connecting",
			"authenticated": false,
			"tool_count":    0,
			"created":       time.Date(2025, 11, 29, 15, 49, 25, 0, time.UTC),
			"updated":       time.Date(2025, 11, 29, 15, 49, 25, 0, time.UTC),
			"oauth": map[string]interface{}{
				"auth_url":  "https://mcp.sentry.dev/oauth/authorize",
				"token_url": "https://mcp.sentry.dev/oauth/token",
				"client_id": "test-client-id",
				"scopes":    []interface{}{"read", "write"},
				"extra_params": map[string]interface{}{
					"resource": "https://mcp.sentry.dev/mcp",
					"audience": "sentry-api",
				},
				"redirect_port": 8080,
			},
			"last_error": "OAuth authorization required",
		},
	}

	servers := ConvertGenericServersToTyped(genericServers)

	require.Len(t, servers, 1, "Should convert exactly one server")

	server := servers[0]
	assert.Equal(t, "sentry", server.Name)
	assert.Equal(t, "https://mcp.sentry.dev/mcp", server.URL)
	assert.Equal(t, false, server.Authenticated)

	// The critical assertions: OAuth config should be extracted
	require.NotNil(t, server.OAuth, "OAuth config should not be nil")
	assert.Equal(t, "https://mcp.sentry.dev/oauth/authorize", server.OAuth.AuthURL)
	assert.Equal(t, "https://mcp.sentry.dev/oauth/token", server.OAuth.TokenURL)
	assert.Equal(t, "test-client-id", server.OAuth.ClientID)
	assert.Equal(t, []string{"read", "write"}, server.OAuth.Scopes)
	assert.Equal(t, 8080, server.OAuth.RedirectPort)

	require.NotNil(t, server.OAuth.ExtraParams, "ExtraParams should not be nil")
	assert.Equal(t, "https://mcp.sentry.dev/mcp", server.OAuth.ExtraParams["resource"])
	assert.Equal(t, "sentry-api", server.OAuth.ExtraParams["audience"])
}

// TestConvertGenericServersToTyped_EmptyOAuth verifies empty OAuth config creates non-nil OAuth struct
func TestConvertGenericServersToTyped_EmptyOAuth(t *testing.T) {
	genericServers := []map[string]interface{}{
		{
			"id":            "test-server",
			"name":          "test-server",
			"enabled":       true,
			"connected":     false,
			"authenticated": false,
			"tool_count":    0,
			"oauth":         map[string]interface{}{}, // Empty OAuth config
		},
	}

	servers := ConvertGenericServersToTyped(genericServers)

	require.Len(t, servers, 1)
	require.NotNil(t, servers[0].OAuth, "Even empty oauth map should create non-nil OAuth config")
	assert.Empty(t, servers[0].OAuth.AuthURL)
	assert.Empty(t, servers[0].OAuth.ClientID)
}

// TestConvertGenericServersToTyped_NoOAuth verifies servers without OAuth have nil OAuth field
func TestConvertGenericServersToTyped_NoOAuth(t *testing.T) {
	genericServers := []map[string]interface{}{
		{
			"id":            "test-server",
			"name":          "test-server",
			"enabled":       true,
			"connected":     true,
			"authenticated": false,
			"tool_count":    5,
			// No oauth field at all
		},
	}

	servers := ConvertGenericServersToTyped(genericServers)

	require.Len(t, servers, 1)
	assert.Nil(t, servers[0].OAuth, "Servers without OAuth config should have nil OAuth field")
}
