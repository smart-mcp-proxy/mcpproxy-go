package auth

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken()
	require.NoError(t, err)

	// Total length: 8 (prefix) + 64 (hex) = 72
	assert.Len(t, token, 72, "token should be 72 characters")
	assert.True(t, strings.HasPrefix(token, TokenPrefixStr), "token should start with mcp_agt_")

	// The hex portion should be valid hex
	hexPart := token[8:]
	assert.Len(t, hexPart, 64, "hex portion should be 64 characters")
	_, err = hex.DecodeString(hexPart)
	assert.NoError(t, err, "hex portion should be valid hex")
}

func TestGenerateToken_Unique(t *testing.T) {
	token1, err := GenerateToken()
	require.NoError(t, err)

	token2, err := GenerateToken()
	require.NoError(t, err)

	assert.NotEqual(t, token1, token2, "two generated tokens should be different")
}

func TestHashToken(t *testing.T) {
	token := "mcp_agt_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	key := []byte("test-key-1234567890123456")

	hash1 := HashToken(token, key)
	hash2 := HashToken(token, key)

	assert.Equal(t, hash1, hash2, "same input and key should produce same hash")
	assert.Len(t, hash1, 64, "HMAC-SHA256 hex should be 64 characters")

	// Should be valid hex
	_, err := hex.DecodeString(hash1)
	assert.NoError(t, err, "hash should be valid hex")
}

func TestHashToken_DifferentKeys(t *testing.T) {
	token := "mcp_agt_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	key1 := []byte("key-one-1234567890123456")
	key2 := []byte("key-two-1234567890123456")

	hash1 := HashToken(token, key1)
	hash2 := HashToken(token, key2)

	assert.NotEqual(t, hash1, hash2, "different keys should produce different hashes")
}

func TestValidateTokenFormat(t *testing.T) {
	tests := []struct {
		name  string
		token string
		valid bool
	}{
		{
			name:  "valid token",
			token: "mcp_agt_" + strings.Repeat("ab", 32),
			valid: true,
		},
		{
			name:  "valid token with mixed hex",
			token: "mcp_agt_" + strings.Repeat("0123456789abcdef", 4),
			valid: true,
		},
		{
			name:  "wrong prefix",
			token: "mcp_xxx_" + strings.Repeat("ab", 32),
			valid: false,
		},
		{
			name:  "too short",
			token: "mcp_agt_abc",
			valid: false,
		},
		{
			name:  "too long",
			token: "mcp_agt_" + strings.Repeat("ab", 33),
			valid: false,
		},
		{
			name:  "non-hex characters",
			token: "mcp_agt_" + strings.Repeat("zz", 32),
			valid: false,
		},
		{
			name:  "empty string",
			token: "",
			valid: false,
		},
		{
			name:  "no prefix",
			token: strings.Repeat("ab", 36),
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, ValidateTokenFormat(tt.token))
		})
	}
}

func TestTokenPrefix(t *testing.T) {
	token := "mcp_agt_abcdef1234567890"
	prefix := TokenPrefix(token)
	assert.Equal(t, "mcp_agt_abcd", prefix, "should return first 12 characters")
}

func TestTokenPrefix_Short(t *testing.T) {
	token := "short"
	prefix := TokenPrefix(token)
	assert.Equal(t, "short", prefix, "short strings should be returned as-is")
}

func TestIsExpired(t *testing.T) {
	t.Run("future expiry is not expired", func(t *testing.T) {
		token := &AgentToken{
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}
		assert.False(t, token.IsExpired())
	})

	t.Run("past expiry is expired", func(t *testing.T) {
		token := &AgentToken{
			ExpiresAt: time.Now().Add(-24 * time.Hour),
		}
		assert.True(t, token.IsExpired())
	})

	t.Run("zero expiry is not expired", func(t *testing.T) {
		token := &AgentToken{}
		assert.False(t, token.IsExpired(), "zero time means no expiry")
	})
}

func TestIsRevoked(t *testing.T) {
	t.Run("revoked token", func(t *testing.T) {
		token := &AgentToken{Revoked: true}
		assert.True(t, token.IsRevoked())
	})

	t.Run("not revoked token", func(t *testing.T) {
		token := &AgentToken{Revoked: false}
		assert.False(t, token.IsRevoked())
	})
}

func TestValidatePermissions(t *testing.T) {
	tests := []struct {
		name    string
		perms   []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "read only",
			perms:   []string{PermRead},
			wantErr: false,
		},
		{
			name:    "read and write",
			perms:   []string{PermRead, PermWrite},
			wantErr: false,
		},
		{
			name:    "all permissions",
			perms:   []string{PermRead, PermWrite, PermDestructive},
			wantErr: false,
		},
		{
			name:    "missing read",
			perms:   []string{PermWrite},
			wantErr: true,
			errMsg:  "must include",
		},
		{
			name:    "invalid value",
			perms:   []string{PermRead, "admin"},
			wantErr: true,
			errMsg:  "invalid permission",
		},
		{
			name:    "empty list",
			perms:   []string{},
			wantErr: true,
			errMsg:  "cannot be empty",
		},
		{
			name:    "nil list",
			perms:   nil,
			wantErr: true,
			errMsg:  "cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePermissions(tt.perms)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// =============================================================================
// T008/T009: HMAC Key Management Tests
// =============================================================================

func TestGetOrCreateHMACKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hmac_key_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// First call: creates the key
	key1, err := GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)
	assert.Len(t, key1, 32, "key should be 32 bytes")

	// Verify file exists with correct permissions
	keyPath := filepath.Join(tmpDir, hmacKeyFile)
	info, err := os.Stat(keyPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "key file should have 0600 permissions")

	// Second call: returns the same key
	key2, err := GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, key1, key2, "should return the same key on second call")
}

func TestGetOrCreateHMACKey_Deterministic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hmac_deterministic_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	key, err := GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	token := "mcp_agt_test1234567890abcdef1234567890abcdef1234567890abcdef12345678"

	// Same key should produce the same HMAC hash consistently
	hash1 := HashToken(token, key)
	hash2 := HashToken(token, key)
	assert.Equal(t, hash1, hash2, "same key should produce consistent HMAC results")

	// Different data directory => different key => different hash
	tmpDir2, err := os.MkdirTemp("", "hmac_deterministic2_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir2)

	key2, err := GetOrCreateHMACKey(tmpDir2)
	require.NoError(t, err)

	hash3 := HashToken(token, key2)
	assert.NotEqual(t, hash1, hash3, "different keys should produce different hashes")
}

func TestGetOrCreateHMACKey_CreatesDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hmac_dir_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	nestedDir := filepath.Join(tmpDir, "nested", "deep")

	key, err := GetOrCreateHMACKey(nestedDir)
	require.NoError(t, err)
	assert.Len(t, key, 32)
}
