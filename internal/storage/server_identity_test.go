package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mcpproxy-go/internal/config"
)

// TestGenerateServerIDArgsOrder tests that server ID is stable regardless of Args order
func TestGenerateServerIDArgsOrder(t *testing.T) {
	// Create a server config with specific Args order
	server1 := &config.ServerConfig{
		Name:     "test-server",
		Protocol: "stdio",
		Command:  "npx",
		Args:     []string{"arg1", "arg2", "arg3"},
	}

	// Create the same server config with identical Args order
	server2 := &config.ServerConfig{
		Name:     "test-server",
		Protocol: "stdio",
		Command:  "npx",
		Args:     []string{"arg1", "arg2", "arg3"},
	}

	// Create the same server config but with different Args order
	server3 := &config.ServerConfig{
		Name:     "test-server",
		Protocol: "stdio",
		Command:  "npx",
		Args:     []string{"arg3", "arg1", "arg2"}, // Different order
	}

	id1 := GenerateServerID(server1)
	id2 := GenerateServerID(server2)
	id3 := GenerateServerID(server3)

	// Same Args order should produce same ID
	assert.Equal(t, id1, id2, "Same Args order should produce same server ID")

	// Different Args order should produce different ID (Args order matters!)
	assert.NotEqual(t, id1, id3, "Different Args order should produce different server ID because argument position is semantically significant")
}

// TestGenerateServerIDStability tests that server ID remains stable across multiple calls
func TestGenerateServerIDStability(t *testing.T) {
	server := &config.ServerConfig{
		Name:     "stable-server",
		Protocol: "stdio",
		Command:  "uvx",
		Args:     []string{"mcp-server-sqlite", "--db-path", "/path/to/db"},
		Env: map[string]string{
			"API_KEY": "secret123",
		},
	}

	// Generate ID multiple times
	id1 := GenerateServerID(server)
	id2 := GenerateServerID(server)
	id3 := GenerateServerID(server)

	// All IDs should be identical
	assert.Equal(t, id1, id2, "Server ID should be stable across multiple calls")
	assert.Equal(t, id2, id3, "Server ID should be stable across multiple calls")
	assert.NotEmpty(t, id1, "Server ID should not be empty")
}

// TestNormalizeAttributesPreservesArgsOrder tests that normalization doesn't modify Args order
func TestNormalizeAttributesPreservesArgsOrder(t *testing.T) {
	attrs := ServerAttributes{
		Name:     "test-server",
		Protocol: "stdio",
		Command:  "node",
		Args:     []string{"script.js", "--flag1", "--flag2", "value"},
	}

	normalized := normalizeAttributes(attrs)

	// Args order should be preserved
	require.Equal(t, len(attrs.Args), len(normalized.Args), "Args length should match")
	for i := range attrs.Args {
		assert.Equal(t, attrs.Args[i], normalized.Args[i], "Args[%d] should preserve order", i)
	}
}

// TestNormalizeAttributesOAuthScopesOrder tests that OAuth scopes are sorted
func TestNormalizeAttributesOAuthScopesOrder(t *testing.T) {
	attrs := ServerAttributes{
		Name:     "oauth-server",
		Protocol: "http",
		URL:      "https://api.example.com",
		OAuth: &OAuthAttributes{
			ClientID:    "client123",
			RedirectURI: "http://localhost:8080/callback",
			Scopes:      []string{"write", "read", "admin"}, // Unsorted
			PKCEEnabled: true,
		},
	}

	normalized := normalizeAttributes(attrs)

	// OAuth scopes should be sorted in the normalized version
	require.NotNil(t, normalized.OAuth, "OAuth should not be nil")
	require.Len(t, normalized.OAuth.Scopes, 3, "Should have 3 scopes")
	assert.Equal(t, "admin", normalized.OAuth.Scopes[0], "Scopes should be sorted alphabetically")
	assert.Equal(t, "read", normalized.OAuth.Scopes[1], "Scopes should be sorted alphabetically")
	assert.Equal(t, "write", normalized.OAuth.Scopes[2], "Scopes should be sorted alphabetically")

	// Original should not be modified - verify the deep copy worked
	assert.Len(t, attrs.OAuth.Scopes, 3, "Original should still have 3 scopes")
	assert.Equal(t, "write", attrs.OAuth.Scopes[0], "Original scopes should not be modified")
	assert.Equal(t, "read", attrs.OAuth.Scopes[1], "Original scopes should not be modified")
	assert.Equal(t, "admin", attrs.OAuth.Scopes[2], "Original scopes should not be modified")
}
