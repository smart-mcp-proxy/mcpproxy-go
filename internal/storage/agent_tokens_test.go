package storage

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// hmacKey used across all agent token tests.
var testHMACKey = []byte("test-hmac-key-32-bytes-long!!!!!")

func setupTestStorageForAgentTokens(t *testing.T) (*Manager, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "agent_token_test_*")
	require.NoError(t, err)

	logger := zap.NewNop().Sugar()

	manager, err := NewManager(tmpDir, logger)
	require.NoError(t, err)

	cleanup := func() {
		manager.Close()
		os.RemoveAll(tmpDir)
	}

	return manager, cleanup
}

func makeTestToken(name string) (auth.AgentToken, string) {
	rawToken, _ := auth.GenerateToken()
	return auth.AgentToken{
		Name:           name,
		AllowedServers: []string{"github", "gitlab"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
		ExpiresAt:      time.Now().Add(24 * time.Hour).UTC(),
		CreatedAt:      time.Now().UTC(),
	}, rawToken
}

func TestAgentTokenCreateAndGetByName(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, rawToken := makeTestToken("deploy-bot")

	err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
	require.NoError(t, err)

	retrieved, err := mgr.GetAgentTokenByName("deploy-bot")
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.Equal(t, "deploy-bot", retrieved.Name)
	assert.Equal(t, auth.TokenPrefix(rawToken), retrieved.TokenPrefix)
	assert.Equal(t, []string{"github", "gitlab"}, retrieved.AllowedServers)
	assert.Equal(t, []string{auth.PermRead, auth.PermWrite}, retrieved.Permissions)
	assert.False(t, retrieved.Revoked)
	assert.NotEmpty(t, retrieved.TokenHash)
}

func TestAgentTokenCreateAndGetByHash(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, rawToken := makeTestToken("ci-runner")

	err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
	require.NoError(t, err)

	hash := auth.HashToken(rawToken, testHMACKey)
	retrieved, err := mgr.GetAgentTokenByHash(hash)
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.Equal(t, "ci-runner", retrieved.Name)
	assert.Equal(t, hash, retrieved.TokenHash)
}

func TestAgentTokenDuplicateName(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token1, rawToken1 := makeTestToken("same-name")
	err := mgr.CreateAgentToken(token1, rawToken1, testHMACKey)
	require.NoError(t, err)

	token2, rawToken2 := makeTestToken("same-name")
	err = mgr.CreateAgentToken(token2, rawToken2, testHMACKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestAgentTokenListAll(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	// Initially empty
	tokens, err := mgr.ListAgentTokens()
	require.NoError(t, err)
	assert.Empty(t, tokens)

	// Add tokens
	names := []string{"bot-1", "bot-2", "bot-3"}
	for _, name := range names {
		token, rawToken := makeTestToken(name)
		err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
		require.NoError(t, err)
	}

	tokens, err = mgr.ListAgentTokens()
	require.NoError(t, err)
	assert.Len(t, tokens, 3)

	// Verify all names present
	foundNames := make(map[string]bool)
	for _, tok := range tokens {
		foundNames[tok.Name] = true
	}
	for _, name := range names {
		assert.True(t, foundNames[name], "should find token %q", name)
	}
}

func TestAgentTokenRevoke(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, rawToken := makeTestToken("revoke-me")
	err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
	require.NoError(t, err)

	// Verify not revoked
	retrieved, err := mgr.GetAgentTokenByName("revoke-me")
	require.NoError(t, err)
	assert.False(t, retrieved.Revoked)

	// Revoke
	err = mgr.RevokeAgentToken("revoke-me")
	require.NoError(t, err)

	// Verify revoked
	retrieved, err = mgr.GetAgentTokenByName("revoke-me")
	require.NoError(t, err)
	assert.True(t, retrieved.Revoked)
}

func TestAgentTokenRevoke_NotFound(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	err := mgr.RevokeAgentToken("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAgentTokenRegenerate(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, rawToken := makeTestToken("regen-bot")
	err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
	require.NoError(t, err)

	// Revoke the original (to verify regeneration clears revoked status)
	err = mgr.RevokeAgentToken("regen-bot")
	require.NoError(t, err)

	// Generate new token
	newRawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	updated, err := mgr.RegenerateAgentToken("regen-bot", newRawToken, testHMACKey)
	require.NoError(t, err)
	require.NotNil(t, updated)

	// Verify configuration preserved
	assert.Equal(t, "regen-bot", updated.Name)
	assert.Equal(t, []string{"github", "gitlab"}, updated.AllowedServers)
	assert.Equal(t, []string{auth.PermRead, auth.PermWrite}, updated.Permissions)
	assert.False(t, updated.Revoked, "regeneration should clear revoked status")

	// Verify new hash
	newHash := auth.HashToken(newRawToken, testHMACKey)
	assert.Equal(t, newHash, updated.TokenHash)
	assert.Equal(t, auth.TokenPrefix(newRawToken), updated.TokenPrefix)

	// Old token hash should not work
	oldHash := auth.HashToken(rawToken, testHMACKey)
	old, err := mgr.GetAgentTokenByHash(oldHash)
	require.NoError(t, err)
	assert.Nil(t, old, "old hash should not resolve")

	// New token hash should work
	retrieved, err := mgr.GetAgentTokenByHash(newHash)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, "regen-bot", retrieved.Name)
}

func TestAgentTokenRegenerate_NotFound(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	newRawToken, _ := auth.GenerateToken()
	_, err := mgr.RegenerateAgentToken("nonexistent", newRawToken, testHMACKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAgentTokenUpdateLastUsed(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, rawToken := makeTestToken("usage-bot")
	err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
	require.NoError(t, err)

	// Initially nil
	retrieved, err := mgr.GetAgentTokenByName("usage-bot")
	require.NoError(t, err)
	assert.Nil(t, retrieved.LastUsedAt)

	// Update
	err = mgr.UpdateAgentTokenLastUsed("usage-bot")
	require.NoError(t, err)

	// Verify updated
	retrieved, err = mgr.GetAgentTokenByName("usage-bot")
	require.NoError(t, err)
	require.NotNil(t, retrieved.LastUsedAt)
	assert.WithinDuration(t, time.Now().UTC(), *retrieved.LastUsedAt, 2*time.Second)
}

func TestAgentTokenUpdateLastUsed_NotFound(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	err := mgr.UpdateAgentTokenLastUsed("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAgentTokenGetCount(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	// Initially zero
	count, err := mgr.GetAgentTokenCount()
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Add tokens
	for i := 0; i < 5; i++ {
		token, rawToken := makeTestToken(fmt.Sprintf("bot-%d", i))
		err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
		require.NoError(t, err)
	}

	count, err = mgr.GetAgentTokenCount()
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

func TestAgentTokenMaxLimit(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	// Create max tokens
	for i := 0; i < auth.MaxTokens; i++ {
		token, rawToken := makeTestToken(fmt.Sprintf("bot-%d", i))
		err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
		require.NoError(t, err, "should succeed for token %d", i)
	}

	// Verify count
	count, err := mgr.GetAgentTokenCount()
	require.NoError(t, err)
	assert.Equal(t, auth.MaxTokens, count)

	// 101st should be rejected
	token, rawToken := makeTestToken("one-too-many")
	err = mgr.CreateAgentToken(token, rawToken, testHMACKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum")
}

func TestAgentTokenValidate_Valid(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, rawToken := makeTestToken("valid-bot")
	err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
	require.NoError(t, err)

	validated, err := mgr.ValidateAgentToken(rawToken, testHMACKey)
	require.NoError(t, err)
	require.NotNil(t, validated)
	assert.Equal(t, "valid-bot", validated.Name)
}

func TestAgentTokenValidate_Expired(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	rawToken, _ := auth.GenerateToken()
	token := auth.AgentToken{
		Name:           "expired-bot",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead},
		ExpiresAt:      time.Now().Add(-1 * time.Hour).UTC(), // Already expired
		CreatedAt:      time.Now().UTC(),
	}

	err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
	require.NoError(t, err)

	_, err = mgr.ValidateAgentToken(rawToken, testHMACKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestAgentTokenValidate_Revoked(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, rawToken := makeTestToken("revoked-bot")
	err := mgr.CreateAgentToken(token, rawToken, testHMACKey)
	require.NoError(t, err)

	err = mgr.RevokeAgentToken("revoked-bot")
	require.NoError(t, err)

	_, err = mgr.ValidateAgentToken(rawToken, testHMACKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "revoked")
}

func TestAgentTokenValidate_NotFound(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	rawToken, _ := auth.GenerateToken()
	_, err := mgr.ValidateAgentToken(rawToken, testHMACKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAgentTokenValidate_InvalidFormat(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	_, err := mgr.ValidateAgentToken("not-a-valid-token", testHMACKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid token format")
}

func TestAgentTokenGetByName_NotFound(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, err := mgr.GetAgentTokenByName("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, token)
}

func TestAgentTokenGetByHash_NotFound(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	token, err := mgr.GetAgentTokenByHash("deadbeefdeadbeefdeadbeefdeadbeef")
	require.NoError(t, err)
	assert.Nil(t, token)
}
