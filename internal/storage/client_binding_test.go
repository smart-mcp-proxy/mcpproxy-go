package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

func mintTestClient(t *testing.T, mgr *Manager, clientID, mode, pin string) string {
	t.Helper()
	raw, err := auth.GenerateClientToken()
	require.NoError(t, err)
	_, err = mgr.MintClientCredential(clientID, raw, testHMACKey, mode, pin, time.Now().Add(365*24*time.Hour))
	require.NoError(t, err)
	return raw
}

// TestUpdateClientCredentialBinding: one transaction rewrites (pin, mode) of
// the ACTIVE kind=client record, returns both snapshots, keeps the secret
// valid, and re-validates the record invariants.
func TestUpdateClientCredentialBinding(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	raw := mintTestClient(t, mgr, "cursor", auth.ProfileModeLocked, "ro")

	before, after, err := mgr.UpdateClientCredentialBinding("cursor", "full", auth.ProfileModeSwitchable)
	require.NoError(t, err)
	assert.Equal(t, "ro", before.ProfilePin)
	assert.Equal(t, auth.ProfileModeLocked, before.ProfileMode)
	assert.Equal(t, "full", after.ProfilePin)
	assert.Equal(t, auth.ProfileModeSwitchable, after.ProfileMode)

	validated, err := mgr.ValidateAgentToken(raw, testHMACKey)
	require.NoError(t, err, "the same secret keeps authenticating after a binding change")
	assert.Equal(t, "full", validated.ProfilePin)
	assert.Equal(t, auth.ProfileModeSwitchable, validated.ProfileMode)
}

func TestUpdateClientCredentialBinding_Refusals(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	_, _, err := mgr.UpdateClientCredentialBinding("cursor", "ro", auth.ProfileModeSwitchable)
	assert.ErrorIs(t, err, ErrClientCredentialNotFound, "no record")

	mintTestClient(t, mgr, "cursor", auth.ProfileModeLocked, "ro")
	_, _, err = mgr.UpdateClientCredentialBinding("cursor", "", auth.ProfileModeLocked)
	require.Error(t, err, "locked with an empty pin is refused")
	_, _, err = mgr.UpdateClientCredentialBinding("cursor", "ro", "sticky")
	require.Error(t, err, "unknown mode is refused")
	_, _, err = mgr.UpdateClientCredentialBinding("Bad/Id", "ro", auth.ProfileModeLocked)
	require.Error(t, err, "invalid client id is refused")

	rec, err := mgr.GetAgentTokenByName("client-cursor")
	require.NoError(t, err)
	assert.Equal(t, "ro", rec.ProfilePin, "a refused update leaves the record untouched")

	_, err = mgr.ForgetClientCredential("cursor")
	require.NoError(t, err)
	_, _, err = mgr.UpdateClientCredentialBinding("cursor", "ro", auth.ProfileModeLocked)
	assert.ErrorIs(t, err, ErrClientCredentialNotActive, "revoked")

	createGrandfatheredRegularToken(t, mgr, auth.AgentToken{
		Name: "client-codex", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
	}, "mcp_agt_"+"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	_, _, err = mgr.UpdateClientCredentialBinding("codex", "ro", auth.ProfileModeLocked)
	assert.ErrorIs(t, err, ErrClientCredentialConflict, "a regular token holding the name is never touched")
}
