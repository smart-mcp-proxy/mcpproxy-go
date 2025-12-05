package tray

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func TestServerSupportsOAuth(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	menuMgr := &MenuManager{
		logger: logger,
	}

	tests := []struct {
		name     string
		server   map[string]interface{}
		expected bool
		reason   string
	}{
		{
			name: "explicit oauth config",
			server: map[string]interface{}{
				"name":  "test-server",
				"url":   "https://api.example.com",
				"oauth": map[string]interface{}{"client_id": "test"},
			},
			expected: true,
			reason:   "server has explicit oauth field",
		},
		{
			name: "oauth error message",
			server: map[string]interface{}{
				"name":       "test-server",
				"url":        "https://api.example.com",
				"last_error": "OAuth authentication required",
			},
			expected: true,
			reason:   "error contains 'OAuth'",
		},
		{
			name: "401 error message",
			server: map[string]interface{}{
				"name":       "test-server",
				"url":        "https://api.example.com",
				"last_error": "failed to connect: 401 Unauthorized",
			},
			expected: true,
			reason:   "error contains '401'",
		},
		{
			name: "deferred for tray UI",
			server: map[string]interface{}{
				"name":       "test-server",
				"url":        "https://api.example.com",
				"last_error": "deferred for tray UI - login available via system tray menu",
			},
			expected: true,
			reason:   "error contains 'deferred for tray UI'",
		},
		{
			name: "oauth domain",
			server: map[string]interface{}{
				"name": "sentry-server",
				"url":  "https://sentry.dev/api/mcp",
			},
			expected: true,
			reason:   "URL contains known OAuth domain",
		},
		{
			name: "http server without oauth indicators",
			server: map[string]interface{}{
				"name": "generic-server",
				"url":  "https://api.example.com",
			},
			expected: true,
			reason:   "HTTP/HTTPS servers can try OAuth",
		},
		{
			name: "stdio server",
			server: map[string]interface{}{
				"name":    "stdio-server",
				"command": "npx",
			},
			expected: false,
			reason:   "stdio servers don't support OAuth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := menuMgr.serverSupportsOAuth(tt.server)
			assert.Equal(t, tt.expected, result, "Expected %v for %s: %s", tt.expected, tt.name, tt.reason)
		})
	}
}

func TestOAuthMenuOrdering(t *testing.T) {
	tests := []struct {
		name            string
		server          map[string]interface{}
		expectOAuthFirst bool
		description     string
	}{
		{
			name: "unauthenticated server needs oauth first",
			server: map[string]interface{}{
				"name":          "test-server",
				"url":           "https://oauth.example.com",
				"enabled":       true,
				"connected":     false,
				"authenticated": false,
				"quarantined":   false,
				"last_error":    "OAuth authentication required",
			},
			expectOAuthFirst: true,
			description:      "OAuth should be first menu item when server needs authentication",
		},
		{
			name: "authenticated server has oauth as secondary",
			server: map[string]interface{}{
				"name":          "test-server",
				"url":           "https://oauth.example.com",
				"enabled":       true,
				"connected":     true,
				"authenticated": true,
				"quarantined":   false,
			},
			expectOAuthFirst: false,
			description:      "OAuth should NOT be first when server is already authenticated",
		},
		{
			name: "disabled server doesn't need oauth first",
			server: map[string]interface{}{
				"name":          "test-server",
				"url":           "https://oauth.example.com",
				"enabled":       false,
				"connected":     false,
				"authenticated": false,
				"quarantined":   false,
			},
			expectOAuthFirst: false,
			description:      "OAuth should NOT be first when server is disabled",
		},
		{
			name: "quarantined server doesn't show oauth",
			server: map[string]interface{}{
				"name":          "test-server",
				"url":           "https://oauth.example.com",
				"enabled":       true,
				"connected":     false,
				"authenticated": false,
				"quarantined":   true,
			},
			expectOAuthFirst: false,
			description:      "OAuth should NOT show when server is quarantined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t).Sugar()
			menuMgr := &MenuManager{
				logger: logger,
			}

			serverName := tt.server["name"].(string)
			enabled := tt.server["enabled"].(bool)
			quarantined := tt.server["quarantined"].(bool)
			authenticated := tt.server["authenticated"].(bool)
			connected := tt.server["connected"].(bool)

			supportsOAuth := menuMgr.serverSupportsOAuth(tt.server)
			needsOAuth := supportsOAuth && !quarantined && !authenticated && enabled && !connected

			assert.Equal(t, tt.expectOAuthFirst, needsOAuth,
				"%s (server: %s, enabled: %v, quarantined: %v, authenticated: %v, connected: %v, supports: %v, needs: %v)",
				tt.description, serverName, enabled, quarantined, authenticated, connected, supportsOAuth, needsOAuth)
		})
	}
}
