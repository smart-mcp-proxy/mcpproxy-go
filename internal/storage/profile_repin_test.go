package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

func repinFixture(t *testing.T, m *Manager) (agentRaw, clientRaw string) {
	t.Helper()
	var err error
	agentRaw, err = auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, m.CreateAgentToken(auth.AgentToken{
		Name: "ro-bot", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		ExpiresAt: time.Now().Add(time.Hour), ProfilePin: "work-readonly",
	}, agentRaw, testHMACKey))
	other, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, m.CreateAgentToken(auth.AgentToken{
		Name: "other", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		ExpiresAt: time.Now().Add(time.Hour), ProfilePin: "work-full",
	}, other, testHMACKey))
	clientRaw, err = auth.GenerateClientToken()
	require.NoError(t, err)
	_, err = m.MintClientCredential("cursor", clientRaw, testHMACKey, auth.ProfileModeLocked, "work-readonly", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	return agentRaw, clientRaw
}

func pinOf(t *testing.T, m *Manager, name string) string {
	t.Helper()
	tok, err := m.GetAgentTokenByName(name)
	require.NoError(t, err)
	require.NotNil(t, tok, name)
	return tok.ProfilePin
}

func TestRepinProfile_RewritesBothKinds(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	repinFixture(t, m)

	before, err := m.RepinProfile("work-readonly", "work-ro")
	require.NoError(t, err)
	assert.Len(t, before, 2)
	for _, b := range before {
		assert.Equal(t, "work-readonly", b.ProfilePin, "the returned records are the pre-rewrite state")
	}
	assert.Equal(t, "work-ro", pinOf(t, m, "ro-bot"))
	assert.Equal(t, "work-ro", pinOf(t, m, "client-cursor"))
	assert.Equal(t, "work-full", pinOf(t, m, "other"), "a token naming another profile is untouched")

	// A revoked record is rewritten too: reinstating it must not resurrect a
	// dangling pin.
	require.NoError(t, m.RevokeAgentToken("ro-bot"))
	_, err = m.RepinProfile("work-ro", "renamed")
	require.NoError(t, err)
	assert.Equal(t, "renamed", pinOf(t, m, "ro-bot"))

	// Restoring puts back exactly the recorded pins.
	moved, err := m.RepinProfile("renamed", "elsewhere")
	require.NoError(t, err)
	require.NoError(t, m.RestorePins(moved))
	assert.Equal(t, "renamed", pinOf(t, m, "ro-bot"))
	assert.Equal(t, "renamed", pinOf(t, m, "client-cursor"))
}

func TestRepinProfile_RefusesEmptyEnds(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	repinFixture(t, m)
	_, err := m.RepinProfile("work-readonly", "")
	require.Error(t, err)
	_, err = m.RepinProfile("", "x")
	require.Error(t, err)
	assert.Equal(t, "work-readonly", pinOf(t, m, "ro-bot"))
}

func TestRepinProfile_OneTransaction(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	repinFixture(t, m)

	boom := errors.New("injected failure")
	_, err := m.repinProfile("work-readonly", "work-ro", func(written int) error {
		if written == 1 {
			return boom
		}
		return nil
	})
	require.ErrorIs(t, err, boom)
	assert.Equal(t, "work-readonly", pinOf(t, m, "ro-bot"), "the first rewrite was rolled back with the rest")
	assert.Equal(t, "work-readonly", pinOf(t, m, "client-cursor"))
}

func TestRepinProfile_PendingRotationStillAuthenticates(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	_, oldSecret := repinFixture(t, m)

	newSecret, err := auth.GenerateClientToken()
	require.NoError(t, err)
	_, err = m.StageClientCredentialRotation("cursor", newSecret, testHMACKey)
	require.NoError(t, err)

	_, err = m.RepinProfile("work-readonly", "work-ro")
	require.NoError(t, err)

	for name, secret := range map[string]string{"old": oldSecret, "pending": newSecret} {
		tok, err := m.ValidateAgentToken(secret, testHMACKey)
		require.NoError(t, err, name)
		assert.Equal(t, "work-ro", tok.ProfilePin, name)
	}
}

func TestRepinProfile_IgnoresOwnedServerEditionTokens(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, m.CreateAgentToken(auth.AgentToken{
		Name: "owned", UserID: "user-1", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		ExpiresAt: time.Now().Add(time.Hour), ProfilePin: "work-readonly",
	}, raw, testHMACKey))

	before, err := m.RepinProfile("work-readonly", "work-ro")
	require.NoError(t, err)
	assert.Empty(t, before)
	owned, err := m.GetAgentTokenByOwnerAndName("user-1", "owned")
	require.NoError(t, err)
	require.NotNil(t, owned)
	assert.Equal(t, "work-readonly", owned.ProfilePin)
}
