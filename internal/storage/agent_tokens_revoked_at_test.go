package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 115 T006: revocation stamps revoked_at once, and the report variant
// tells a caller whether the revoke changed anything.
func TestRevokeAgentTokenReport_StampsRevokedAtOnce(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	key := []byte("0123456789abcdef0123456789abcdef")
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, m.CreateAgentToken(auth.AgentToken{
		Name: "t1", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		ExpiresAt: time.Now().Add(time.Hour),
	}, raw, key))

	before, after, err := m.RevokeAgentTokenReport("", "t1")
	require.NoError(t, err)
	assert.False(t, before.Revoked)
	assert.Nil(t, before.RevokedAt)
	require.True(t, after.Revoked)
	require.NotNil(t, after.RevokedAt)
	first := *after.RevokedAt

	time.Sleep(5 * time.Millisecond)
	before2, after2, err := m.RevokeAgentTokenReport("", "t1")
	require.NoError(t, err)
	assert.True(t, before2.Revoked, "a second revoke sees the token already revoked")
	require.NotNil(t, after2.RevokedAt)
	assert.True(t, first.Equal(*after2.RevokedAt), "revoked_at is never overwritten")

	stored, err := m.GetAgentTokenByName("t1")
	require.NoError(t, err)
	require.NotNil(t, stored.RevokedAt)
	assert.True(t, first.Equal(*stored.RevokedAt))

	_, _, err = m.RevokeAgentTokenReport("", "nope")
	assert.ErrorIs(t, err, ErrAgentTokenNotFound)
}

func TestRevokeAgentTokensForOwner_StampsRevokedAt(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	key := []byte("0123456789abcdef0123456789abcdef")
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, m.CreateAgentToken(auth.AgentToken{
		Name: "owned", UserID: "u1", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		ExpiresAt: time.Now().Add(time.Hour),
	}, raw, key))
	n, err := m.RevokeAgentTokensForOwner("u1")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	tok, err := m.GetAgentTokenByOwnerAndName("u1", "owned")
	require.NoError(t, err)
	require.NotNil(t, tok.RevokedAt)
}

func TestForgetClientCredential_StampsRevokedAt(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	key := []byte("0123456789abcdef0123456789abcdef")
	raw, err := auth.GenerateClientToken()
	require.NoError(t, err)
	issuer := &auth.CredentialIssuer{ActorKind: "api_key", Surface: "mcp"}
	minted, err := m.MintClientCredentialWith("w1", raw, key, ClientMintOptions{
		Mode: auth.ProfileModeLocked, Pin: "p", ExpiresAt: time.Now().Add(time.Hour), Issuer: issuer, Purpose: "task",
	})
	require.NoError(t, err)
	assert.Equal(t, issuer, minted.Issuer)
	assert.Equal(t, "task", minted.Purpose)

	revoked, err := m.ForgetClientCredential("w1")
	require.NoError(t, err)
	require.NotNil(t, revoked.RevokedAt)
	first := *revoked.RevokedAt
	again, err := m.ForgetClientCredential("w1")
	require.NoError(t, err)
	assert.True(t, first.Equal(*again.RevokedAt))
	assert.Equal(t, "task", again.Purpose)
	assert.Equal(t, issuer.Surface, again.Issuer.Surface)
}
