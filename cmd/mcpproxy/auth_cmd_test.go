package main

import (
	"runtime"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"

	"github.com/stretchr/testify/assert"
)

func TestShouldUseAuthDaemon(t *testing.T) {
	// Test with non-existent directory
	result := shouldUseAuthDaemon("/tmp/nonexistent-mcpproxy-test-dir-auth-88888")
	assert.False(t, result, "shouldUseAuthDaemon should return false for non-existent directory")

	// Test with existing directory but no socket
	tmpDir := t.TempDir()
	result = shouldUseAuthDaemon(tmpDir)
	assert.False(t, result, "shouldUseAuthDaemon should return false when socket doesn't exist")
}

func TestAuthStatus_RequiresDaemon(t *testing.T) {
	tmpDir := t.TempDir()

	// Test that auth status requires daemon
	result := shouldUseAuthDaemon(tmpDir)
	assert.False(t, result, "Should return false when daemon not running")
}

func TestDetectSocketPath_Auth(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := socket.DetectSocketPath(tmpDir)

	assert.NotEmpty(t, socketPath, "DetectSocketPath should return non-empty path")

	// Platform-specific assertions
	if runtime.GOOS == "windows" {
		// Windows: Named pipes use global namespace with hash
		assert.True(t, strings.HasPrefix(socketPath, "npipe:////./pipe/mcpproxy-"),
			"Windows socket should be a named pipe")
	} else {
		// Unix: Socket file is within data directory
		assert.Contains(t, socketPath, tmpDir, "Socket path should be within data directory")
		assert.True(t, strings.HasPrefix(socketPath, "unix://"),
			"Unix socket should have unix:// prefix")
	}
}

func TestFilterOAuthServers(t *testing.T) {
	tests := []struct {
		name     string
		servers  []map[string]interface{}
		expected int
	}{
		{
			name: "filter servers with OAuth config",
			servers: []map[string]interface{}{
				{
					"name":  "oauth-server",
					"oauth": map[string]interface{}{"client_id": "test"},
				},
				{
					"name": "non-oauth-server",
				},
			},
			expected: 1,
		},
		{
			name: "filter servers with authenticated status",
			servers: []map[string]interface{}{
				{
					"name":          "authenticated-server",
					"authenticated": true,
				},
				{
					"name":          "non-authenticated-server",
					"authenticated": false,
				},
			},
			expected: 1,
		},
		{
			name: "filter servers with OAuth errors",
			servers: []map[string]interface{}{
				{
					"name":       "error-server",
					"last_error": "OAuth authentication required",
				},
				{
					"name":       "other-error-server",
					"last_error": "Connection refused",
				},
			},
			expected: 1,
		},
		{
			name:     "empty server list",
			servers:  []map[string]interface{}{},
			expected: 0,
		},
		{
			name: "all OAuth servers",
			servers: []map[string]interface{}{
				{
					"name":  "oauth1",
					"oauth": map[string]interface{}{"client_id": "test1"},
				},
				{
					"name":          "oauth2",
					"authenticated": true,
				},
				{
					"name":       "oauth3",
					"last_error": "OAuth error",
				},
			},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterOAuthServers(tt.servers)
			assert.Equal(t, tt.expected, len(result), "filterOAuthServers should return correct number of OAuth servers")
		})
	}
}
