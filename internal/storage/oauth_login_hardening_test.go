package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

func TestAuthenticateOAuthAccess(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	g := commitNew(t, m, "reg1", "grant1", "myclient", "", auth.ProfileModeSwitchable, now)

	got, err := m.AuthenticateOAuthAccess("ah-grant1", now)
	require.NoError(t, err)
	assert.Equal(t, "grant1", got.GrantID)

	_, err = m.AuthenticateOAuthAccess("ah-grant1", g.AccessExpiresAt)
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound, "at the expiry instant the token is no longer valid")
	_, err = m.AuthenticateOAuthAccess("ah-grant1", g.AccessExpiresAt.Add(-time.Nanosecond))
	assert.NoError(t, err)
	_, err = m.AuthenticateOAuthAccess("", now)
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
	_, err = m.AuthenticateOAuthAccess("nope", now)
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)

	// Lapsed refresh, not yet swept: refused even with a live access expiry.
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg2", now)))
	lapsed := newTestGrant("grant2", "reg2", "myclient", now)
	lapsed.AccessExpiresAt = now.Add(time.Hour)
	lapsed.RefreshExpiresAt = now.Add(time.Minute)
	rev, _ := m.ClientBindingRevision("myclient")
	_, err = m.CommitOAuthLogin(CommitLoginInput{Grant: lapsed, Client: "myclient", ExpectedBindingRevision: rev, Now: now})
	require.NoError(t, err)
	_, err = m.AuthenticateOAuthAccess("ah-grant2", now)
	require.NoError(t, err)
	_, err = m.AuthenticateOAuthAccess("ah-grant2", now.Add(2*time.Minute))
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)

	// Revoked.
	_, err = m.RevokeOAuthGrant("grant1", OAuthRevokeAdmin, now)
	require.NoError(t, err)
	_, err = m.AuthenticateOAuthAccess("ah-grant1", now)
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
	// A stale index entry pointing at a revoked grant never resolves either.
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		return putAccessIndexTx(tx, "ah-grant1", "grant1")
	}))
	_, err = m.getOAuthGrantByAccessHash("ah-grant1")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
}

func TestCreateOAuthRegistration_DuplicateIDRefused(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	commitNew(t, m, "reg1", "grant1", "myclient", "", auth.ProfileModeSwitchable, now)
	before := dumpDB(t, m)

	err := m.CreateOAuthRegistration(newTestReg("reg1", now.Add(time.Hour)))
	assert.ErrorIs(t, err, ErrOAuthRegistrationExists)
	assert.Equal(t, before, dumpDB(t, m), "nothing written")
	r, err := m.GetOAuthRegistration("reg1")
	require.NoError(t, err)
	assert.Equal(t, OAuthRegistrationApproved, r.State)
	g, err := m.GetOAuthGrant("grant1")
	require.NoError(t, err)
	assert.False(t, g.Revoked)
}

func TestCreateOAuthRegistration_BackdatedInsertDoesNotEvictNewer(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < MaxUnapprovedOAuthRegistrations; i++ {
		require.NoError(t, m.CreateOAuthRegistration(newTestReg(fmt.Sprintf("r%02d", i), base.Add(time.Duration(i)*time.Second))))
	}
	// Older than every existing row: it is itself the oldest and goes; no
	// newer row is evicted in its place.
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("ancient", base.Add(-time.Hour))))
	_, err := m.GetOAuthRegistration("ancient")
	assert.ErrorIs(t, err, ErrOAuthRegistrationGone)
	regs, err := m.ListOAuthRegistrations()
	require.NoError(t, err)
	assert.Len(t, regs, MaxUnapprovedOAuthRegistrations)
	_, err = m.GetOAuthRegistration("r00")
	assert.NoError(t, err)
}

func TestRotateOAuthRefresh_EmptyPresentedHashIsInvalidNotReplay(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	commitNew(t, m, "reg1", "grant1", "myclient", "", auth.ProfileModeSwitchable, now)
	before := dumpDB(t, m)
	tx := dbTxID(t, m)
	out, _, err := m.RotateOAuthRefresh("grant1", "", "x", "p", "y", now, time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, RefreshInvalid, out)
	assert.Equal(t, before, dumpDB(t, m))
	assert.Equal(t, tx, dbTxID(t, m))
}

// TestNoopCallsCommitNoWriteTransaction: with nothing to do, the sweeps and
// the repair open no Update transaction (an empty one still commits a meta page).
func TestNoopCallsCommitNoWriteTransaction(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	check := func(name string) {
		tx := dbTxID(t, m)
		_, err := m.SweepOAuthRegistrations(now)
		require.NoError(t, err)
		_, err = m.SweepOAuthGrants(now)
		require.NoError(t, err)
		_, _, err = m.RepairOAuthClientBindings()
		require.NoError(t, err)
		assert.Equal(t, tx, dbTxID(t, m), name)
	}
	check("fresh database")

	// With live data and nothing expired or drifted, still no write.
	clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "ro")
	check("live data")
}

func TestCurrentClientBinding(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	_, _, found, err := m.CurrentClientBinding("cursor")
	require.NoError(t, err)
	assert.False(t, found)

	commitNew(t, m, "r", "g1", "webonly", "ro", auth.ProfileModeLocked, time.Now())
	pin, mode, found, err := m.CurrentClientBinding("webonly")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "ro", pin)
	assert.Equal(t, auth.ProfileModeLocked, mode)

	// The API-key record wins over a stale OAuth entry (older binary).
	clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "ro")
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		return oauthPutJSON(tx, OAuthClientBindingsBucket, "cursor", &OAuthClientBinding{
			ProfilePin: "stale", ProfileMode: auth.ProfileModeSwitchable,
		})
	}))
	pin, mode, found, err = m.CurrentClientBinding("cursor")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "ro", pin)
	assert.Equal(t, auth.ProfileModeLocked, mode)
}
