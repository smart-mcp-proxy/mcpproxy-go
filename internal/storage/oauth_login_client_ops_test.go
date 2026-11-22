package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// twoLoginClient: an API-key client with two browser logins on two registrations,
// plus a bystander client with one login, so deletions can be checked for reach.
func twoLoginClient(t *testing.T, m *Manager) {
	t.Helper()
	now := time.Now().UTC()
	clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "ro") // grant-cursor on reg-cursor
	rev, err := m.ClientBindingRevision("cursor")
	require.NoError(t, err)
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg-cursor-2", now)))
	_, err = m.CommitOAuthLogin(CommitLoginInput{
		Grant: newTestGrant("grant-cursor-2", "reg-cursor-2", "cursor", now), Client: "cursor",
		ExpectedBindingRevision: rev, Now: now,
	})
	require.NoError(t, err)
	commitNew(t, m, "reg-by", "grant-by", "bystander", "", auth.ProfileModeSwitchable, now)
}

func TestForgetClient_DeletesLoginsBindingAndOrphanRegistrations(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	twoLoginClient(t, m)

	rec, err := m.ForgetClient("cursor")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.True(t, rec.Revoked, "the API key is revoked exactly as ForgetClientCredential does")

	gs, err := m.ListOAuthGrantsForClient("cursor")
	require.NoError(t, err)
	assert.Empty(t, gs)
	_, err = m.GetOAuthGrantByAccessHash("ah-grant-cursor")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
	b, _ := m.GetOAuthClientBinding("cursor")
	assert.Nil(t, b)
	for _, id := range []string{"reg-cursor", "reg-cursor-2"} {
		_, err = m.GetOAuthRegistration(id)
		assert.ErrorIs(t, err, ErrOAuthRegistrationGone, id)
	}

	// The bystander is untouched.
	_, err = m.GetOAuthGrant("grant-by")
	require.NoError(t, err)
	_, err = m.GetOAuthRegistration("reg-by")
	require.NoError(t, err)
	b, _ = m.GetOAuthClientBinding("bystander")
	assert.NotNil(t, b)
	used, _ := m.OAuthLoginEverUsed()
	assert.True(t, used)
}

func TestForgetClient_OAuthOnlyClientAndNothing(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	commitNew(t, m, "r", "g1", "webonly", "", auth.ProfileModeSwitchable, time.Now())

	rec, err := m.ForgetClient("webonly")
	require.NoError(t, err)
	assert.Nil(t, rec, "no API-key record")
	_, err = m.GetOAuthGrant("g1")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)

	_, err = m.ForgetClient("webonly")
	assert.ErrorIs(t, err, ErrClientCredentialNotFound, "nothing left")
}

func TestForgetClientLogins_LeavesAPIKeyAlone(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	twoLoginClient(t, m)

	n, err := m.ForgetClientLogins("cursor")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	rec, err := m.GetAgentTokenByName("client-cursor")
	require.NoError(t, err)
	assert.False(t, rec.Revoked)
	b, _ := m.GetOAuthClientBinding("cursor")
	assert.Nil(t, b)
}

func TestRevokeClient_RevokesKeyAndAllGrantsKeepsRecords(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	twoLoginClient(t, m)
	now := time.Now().UTC()

	rec, err := m.RevokeClient("cursor", now)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.True(t, rec.Revoked)

	gs, err := m.ListOAuthGrantsForClient("cursor")
	require.NoError(t, err)
	require.Len(t, gs, 2)
	for _, g := range gs {
		assert.True(t, g.Revoked, g.GrantID)
		assert.Equal(t, OAuthRevokeClientRevoked, g.RevokeReason)
		require.NotNil(t, g.RevokedAt)
	}
	_, err = m.GetOAuthGrantByAccessHash("ah-grant-cursor")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound, "revoked grants leave the access index")
	b, _ := m.GetOAuthClientBinding("cursor")
	assert.NotNil(t, b, "the binding entry is kept for display")
	_, err = m.GetOAuthRegistration("reg-cursor")
	assert.NoError(t, err)

	by, err := m.GetOAuthGrant("grant-by")
	require.NoError(t, err)
	assert.False(t, by.Revoked)

	// An OAuth-only client revokes too; nothing at all is NotFound.
	commitNew(t, m, "r", "g1", "webonly", "", auth.ProfileModeSwitchable, now)
	rec, err = m.RevokeClient("webonly", now)
	require.NoError(t, err)
	assert.Nil(t, rec)
	g, _ := m.GetOAuthGrant("g1")
	assert.True(t, g.Revoked)
	_, err = m.RevokeClient("ghost", now)
	assert.ErrorIs(t, err, ErrClientCredentialNotFound)
}

func TestRevokeClientAPIKey_LeavesGrantsAndBinding(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	twoLoginClient(t, m)

	rec, err := m.RevokeClientAPIKey("cursor")
	require.NoError(t, err)
	assert.True(t, rec.Revoked)

	gs, err := m.ListOAuthGrantsForClient("cursor")
	require.NoError(t, err)
	require.Len(t, gs, 2)
	for _, g := range gs {
		assert.False(t, g.Revoked)
	}
	b, _ := m.GetOAuthClientBinding("cursor")
	require.NotNil(t, b)
	assert.Equal(t, "ro", b.ProfilePin)
}

func TestConnectRollbackLeavesGrantsUntouched(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	twoLoginClient(t, m)
	_, err := m.RevokeClientAPIKey("cursor")
	require.NoError(t, err)
	mintTestClient(t, m, "cursor", auth.ProfileModeSwitchable, "") // connect
	_, err = m.ForgetClientCredentialRestoringPrior("cursor")      // undo
	require.NoError(t, err)

	gs, err := m.ListOAuthGrantsForClient("cursor")
	require.NoError(t, err)
	require.Len(t, gs, 2)
	for _, g := range gs {
		assert.False(t, g.Revoked)
	}
	assertEqualBindings(t, m, "cursor", "ro", auth.ProfileModeLocked)
}
