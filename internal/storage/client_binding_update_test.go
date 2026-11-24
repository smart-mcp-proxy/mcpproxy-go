package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

func TestUpdateOAuthClientBinding_OAuthOnlyClient(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	commitNew(t, m, "r", "g1", "webonly", "ro", auth.ProfileModeLocked, time.Now().UTC())

	pin, mode, err := m.UpdateOAuthClientBinding("webonly", "", auth.ProfileModeSwitchable)
	require.NoError(t, err)
	assert.Equal(t, "ro", pin)
	assert.Equal(t, auth.ProfileModeLocked, mode)
	p, md, found, err := m.CurrentClientBinding("webonly")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "", p)
	assert.Equal(t, auth.ProfileModeSwitchable, md)

	// Invalid: locked needs a pin; nothing changes.
	_, _, err = m.UpdateOAuthClientBinding("webonly", "", auth.ProfileModeLocked)
	require.Error(t, err)
	_, md, _, _ = m.CurrentClientBinding("webonly")
	assert.Equal(t, auth.ProfileModeSwitchable, md)

	// A client with no browser-login entry is NotFound.
	_, _, err = m.UpdateOAuthClientBinding("ghost", "ro", auth.ProfileModeLocked)
	assert.ErrorIs(t, err, ErrClientCredentialNotFound)
}

func TestUpdateOAuthClientBinding_KeepsBothCopiesEqualEvenWhenKeyIsRevoked(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	twoLoginClient(t, m)
	_, err := m.RevokeClientAPIKey("cursor")
	require.NoError(t, err)

	_, _, err = m.UpdateOAuthClientBinding("cursor", "rw", auth.ProfileModeSwitchable)
	require.NoError(t, err, "a revoked API key does not block a binding change of a client with live logins")
	assertEqualBindings(t, m, "cursor", "rw", auth.ProfileModeSwitchable)
}
