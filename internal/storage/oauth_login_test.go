package storage

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// dumpDB returns every key/value of every bucket plus the bucket names, so a
// test can assert that a failed or read-only call left the database as it was.
func dumpDB(t *testing.T, m *Manager) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bbolt.Bucket) error {
			out["bucket:"+string(name)] = ""
			return b.ForEach(func(k, v []byte) error {
				out[string(name)+"/"+string(k)] = string(v)
				return nil
			})
		})
	}))
	return out
}

func oauthBucketNames(t *testing.T, m *Manager) []string {
	t.Helper()
	var names []string
	require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bbolt.Bucket) error {
			switch string(name) {
			case OAuthRegistrationsBucket, OAuthGrantsBucket, OAuthGrantAccessIndexBucket, OAuthClientBindingsBucket:
				names = append(names, string(name))
			}
			return nil
		})
	}))
	sort.Strings(names)
	return names
}

// dbTxID is the id of the latest committed transaction: a read transaction
// sees the newest meta page, and every committed write transaction (even an
// empty one) advances it.
func dbTxID(t *testing.T, m *Manager) int {
	t.Helper()
	tx, err := m.db.db.Begin(false)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	return tx.ID()
}

func newTestReg(id string, created time.Time) OAuthRegistration {
	return OAuthRegistration{
		ClientID: id, ReportedName: "Test Client", RedirectURIs: []string{"http://127.0.0.1:9999/cb"},
		TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"},
		CreatedAt: created,
	}
}

func newTestGrant(id, reg, client string, now time.Time) OAuthGrant {
	return OAuthGrant{
		GrantID: id, RegistrationID: reg, Client: client,
		RedirectURI: "http://127.0.0.1:9999/cb", Resource: "http://127.0.0.1:8080/mcp/oauth", Issuer: "http://127.0.0.1:8080",
		RefreshHash: "rh-" + id, RefreshPrefix: "mcp_ort_" + id, AccessHash: "ah-" + id,
		AccessExpiresAt: now.Add(time.Hour), RefreshExpiresAt: now.Add(30 * 24 * time.Hour),
		ApprovedAt: now, ApprovedBy: OAuthActor{Kind: "admin", Name: "algis"}, DisplayPrefix: "mcp_oat_" + id,
	}
}

// commitNew commits a login for a brand-new client.
func commitNew(t *testing.T, m *Manager, regID, grantID, client, pin, mode string, now time.Time) OAuthGrant {
	t.Helper()
	require.NoError(t, m.CreateOAuthRegistration(newTestReg(regID, now)))
	g, err := m.CommitOAuthLogin(CommitLoginInput{
		Grant: newTestGrant(grantID, regID, client, now), Client: client, NewClient: true,
		Pin: pin, Mode: mode, DisplayName: "Custom", ExpectedBindingRevision: OAuthBindingAbsent, Now: now,
	})
	require.NoError(t, err)
	return g
}

// TestOAuthLoginLazyBuckets: opening a database and calling every READ function
// (and the sweeps and writers that find nothing to do) creates no OAuth bucket
// and no marker (Spec 132 FR-002).
func TestOAuthLoginLazyBuckets(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	before := dumpDB(t, m)
	now := time.Now()

	_, err := m.GetOAuthRegistration("x")
	assert.ErrorIs(t, err, ErrOAuthRegistrationGone)
	regs, err := m.ListOAuthRegistrations()
	require.NoError(t, err)
	assert.Empty(t, regs)
	_, err = m.GetOAuthGrant("x")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
	_, err = m.getOAuthGrantByAccessHash("h")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
	gs, err := m.ListOAuthGrants()
	require.NoError(t, err)
	assert.Empty(t, gs)
	gs, err = m.ListOAuthGrantsForClient("cursor")
	require.NoError(t, err)
	assert.Empty(t, gs)
	used, err := m.OAuthLoginEverUsed()
	require.NoError(t, err)
	assert.False(t, used)
	rev, err := m.ClientBindingRevision("cursor")
	require.NoError(t, err)
	assert.Equal(t, OAuthBindingAbsent, rev)
	b, err := m.GetOAuthClientBinding("cursor")
	require.NoError(t, err)
	assert.Nil(t, b)
	bs, err := m.ListOAuthClientBindings()
	require.NoError(t, err)
	assert.Empty(t, bs)

	// Writers that find nothing.
	n, err := m.SweepOAuthRegistrations(now)
	require.NoError(t, err)
	assert.Zero(t, n)
	n, err = m.SweepOAuthGrants(now)
	require.NoError(t, err)
	assert.Zero(t, n)
	outcome, _, err := m.RotateOAuthRefresh("g", "h", "n", "p", "a", now, time.Hour, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, RefreshInvalid, outcome)
	_, err = m.RevokeOAuthGrant("g", OAuthRevokeAdmin, now)
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
	assert.ErrorIs(t, m.TouchOAuthGrant("g", now, ""), ErrOAuthGrantNotFound)
	assert.ErrorIs(t, m.MarkOAuthRegistrationAuthorized("r", now), ErrOAuthRegistrationGone)
	rep, _, err := m.RepairOAuthClientBindings()
	require.NoError(t, err)
	assert.Empty(t, rep)
	_, err = m.ForgetClient("cursor")
	assert.ErrorIs(t, err, ErrClientCredentialNotFound)
	_, err = m.RevokeClient("cursor", now)
	assert.ErrorIs(t, err, ErrClientCredentialNotFound)
	_, err = m.ForgetClientLogins("cursor")
	require.NoError(t, err)
	_, err = m.RepinProfile("a", "b")
	require.NoError(t, err)

	assert.Empty(t, oauthBucketNames(t, m), "no OAuth bucket may exist")
	assert.Equal(t, before, dumpDB(t, m), "no key may appear")

	// The existing API-key writers must not create them either.
	mintTestClient(t, m, "cursor", auth.ProfileModeLocked, "ro")
	_, _, err = m.UpdateClientCredentialBinding("cursor", "full", auth.ProfileModeSwitchable)
	require.NoError(t, err)
	_, err = m.RepinProfile("full", "x")
	require.NoError(t, err)
	_, err = m.ForgetClientCredential("cursor")
	require.NoError(t, err)
	assert.Empty(t, oauthBucketNames(t, m))
	used, err = m.OAuthLoginEverUsed()
	require.NoError(t, err)
	assert.False(t, used)
}

func TestOAuthRegistration_CreateGetStateSweep(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()

	require.NoError(t, m.CreateOAuthRegistration(newTestReg("p1", now.Add(-20*time.Minute))))
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("p2", now.Add(-5*time.Minute))))
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("a1", now.Add(-48*time.Hour))))
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("a2", now.Add(-48*time.Hour))))

	r, err := m.GetOAuthRegistration("p2")
	require.NoError(t, err)
	assert.Equal(t, OAuthRegistrationPending, r.State)
	assert.Equal(t, "Test Client", r.ReportedName)

	require.NoError(t, m.MarkOAuthRegistrationAuthorized("a1", now.Add(-25*time.Hour)))
	require.NoError(t, m.MarkOAuthRegistrationAuthorized("a2", now.Add(-time.Hour)))
	r, err = m.GetOAuthRegistration("a1")
	require.NoError(t, err)
	assert.Equal(t, OAuthRegistrationAuthorized, r.State)
	require.NotNil(t, r.AuthorizedAt)

	// A second authorize does not extend the clock.
	require.NoError(t, m.MarkOAuthRegistrationAuthorized("a1", now))
	r, _ = m.GetOAuthRegistration("a1")
	assert.WithinDuration(t, now.Add(-25*time.Hour), *r.AuthorizedAt, time.Second)

	// An approved row survives any sweep.
	commitNew(t, m, "ok", "g1ok", "cursor", "", auth.ProfileModeSwitchable, now.Add(-100*24*time.Hour))

	n, err := m.SweepOAuthRegistrations(now)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "pending older than 15 min and authorized older than 24 h")
	_, err = m.GetOAuthRegistration("p1")
	assert.ErrorIs(t, err, ErrOAuthRegistrationGone)
	_, err = m.GetOAuthRegistration("a1")
	assert.ErrorIs(t, err, ErrOAuthRegistrationGone)
	for _, id := range []string{"p2", "a2", "ok"} {
		_, err = m.GetOAuthRegistration(id)
		assert.NoError(t, err, id)
	}
	r, _ = m.GetOAuthRegistration("ok")
	assert.Equal(t, OAuthRegistrationApproved, r.State)
}

func TestOAuthRegistration_CapDropsOldestNonApproved(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	base := time.Now().UTC().Add(-time.Hour)

	// One approved row, the oldest of all: it is never evicted.
	commitNew(t, m, "approved", "gapproved", "cursor", "", auth.ProfileModeSwitchable, base.Add(-time.Hour))

	for i := 0; i < MaxUnapprovedOAuthRegistrations; i++ {
		require.NoError(t, m.CreateOAuthRegistration(newTestReg(fmt.Sprintf("r%02d", i), base.Add(time.Duration(i)*time.Second))))
	}
	// r00 is authorized: authorized and pending count together.
	require.NoError(t, m.MarkOAuthRegistrationAuthorized("r00", base))

	regs, err := m.ListOAuthRegistrations()
	require.NoError(t, err)
	assert.Len(t, regs, MaxUnapprovedOAuthRegistrations+1)

	require.NoError(t, m.CreateOAuthRegistration(newTestReg("new", base.Add(time.Hour))))
	regs, err = m.ListOAuthRegistrations()
	require.NoError(t, err)
	assert.Len(t, regs, MaxUnapprovedOAuthRegistrations+1, "still 50 unapproved + 1 approved")
	_, err = m.GetOAuthRegistration("r00")
	assert.ErrorIs(t, err, ErrOAuthRegistrationGone, "the oldest non-approved row (here authorized) is dropped")
	for _, id := range []string{"r01", "new", "approved"} {
		_, err = m.GetOAuthRegistration(id)
		assert.NoError(t, err, id)
	}
}

func TestCommitOAuthLogin_NewClient(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()

	used, _ := m.OAuthLoginEverUsed()
	require.False(t, used)

	g := commitNew(t, m, "reg1", "grant1", "myclient", "ro", auth.ProfileModeLocked, now)
	assert.Equal(t, "grant1", g.GrantID)

	reg, err := m.GetOAuthRegistration("reg1")
	require.NoError(t, err)
	assert.Equal(t, OAuthRegistrationApproved, reg.State)

	got, err := m.GetOAuthGrant("grant1")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:9999/cb", got.RedirectURI)
	assert.Equal(t, "myclient", got.Client)

	b, err := m.GetOAuthClientBinding("myclient")
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, "ro", b.ProfilePin)
	assert.Equal(t, auth.ProfileModeLocked, b.ProfileMode)
	assert.Equal(t, "Custom", b.DisplayName)

	byHash, err := m.getOAuthGrantByAccessHash("ah-grant1")
	require.NoError(t, err)
	assert.Equal(t, "grant1", byHash.GrantID)

	used, err = m.OAuthLoginEverUsed()
	require.NoError(t, err)
	assert.True(t, used)

	rev, err := m.ClientBindingRevision("myclient")
	require.NoError(t, err)
	assert.Equal(t, bindingRevision("myclient", "ro", auth.ProfileModeLocked), rev)
	assert.Len(t, rev, 16)

	// The marker keeps the FIRST login's time.
	var first string
	require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
		first = string(tx.Bucket([]byte(MetaBucket)).Get([]byte(OAuthLoginEverUsedKey)))
		return nil
	}))
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg2", now)))
	_, err = m.CommitOAuthLogin(CommitLoginInput{
		Grant: newTestGrant("grant2", "reg2", "myclient", now), Client: "myclient",
		ExpectedBindingRevision: rev, Now: now.Add(time.Hour),
	})
	require.NoError(t, err)
	require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
		assert.Equal(t, first, string(tx.Bucket([]byte(MetaBucket)).Get([]byte(OAuthLoginEverUsedKey))))
		return nil
	}))
}

func TestCommitOAuthLogin_ExistingAPIKeyClientGetsEntryFromCurrentRecord(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	mintTestClient(t, m, "cursor", auth.ProfileModeLocked, "ro")

	rev, err := m.ClientBindingRevision("cursor")
	require.NoError(t, err)
	assert.Equal(t, bindingRevision("cursor", "ro", auth.ProfileModeLocked), rev)

	require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg", now)))
	// Pin/Mode in the input are ignored for an existing client.
	_, err = m.CommitOAuthLogin(CommitLoginInput{
		Grant: newTestGrant("g1", "reg", "cursor", now), Client: "cursor",
		Pin: "full", Mode: auth.ProfileModeSwitchable, ExpectedBindingRevision: rev, Now: now,
	})
	require.NoError(t, err)

	b, err := m.GetOAuthClientBinding("cursor")
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, "ro", b.ProfilePin)
	assert.Equal(t, auth.ProfileModeLocked, b.ProfileMode)
	rec, err := m.GetAgentTokenByName("client-cursor")
	require.NoError(t, err)
	assert.Equal(t, b.ProfilePin, rec.ProfilePin)
	assert.Equal(t, b.ProfileMode, rec.ProfileMode)
}

func TestCommitOAuthLogin_FailedChecksWriteNothing(t *testing.T) {
	now := time.Now().UTC()

	t.Run("registration gone", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		before := dumpDB(t, m)
		_, err := m.CommitOAuthLogin(CommitLoginInput{
			Grant: newTestGrant("g", "missing", "c1", now), Client: "c1", NewClient: true,
			Pin: "", Mode: auth.ProfileModeSwitchable, ExpectedBindingRevision: OAuthBindingAbsent, Now: now,
		})
		assert.ErrorIs(t, err, ErrOAuthRegistrationGone)
		assert.Equal(t, before, dumpDB(t, m))
		assert.Empty(t, oauthBucketNames(t, m))
	})

	t.Run("new client id held by an API key", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		mintTestClient(t, m, "cursor", auth.ProfileModeSwitchable, "")
		require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg", now)))
		before := dumpDB(t, m)
		_, err := m.CommitOAuthLogin(CommitLoginInput{
			Grant: newTestGrant("g", "reg", "cursor", now), Client: "cursor", NewClient: true,
			Mode: auth.ProfileModeSwitchable, ExpectedBindingRevision: OAuthBindingAbsent, Now: now,
		})
		assert.ErrorIs(t, err, ErrOAuthClientExists)
		assert.Equal(t, before, dumpDB(t, m))
	})

	t.Run("stale binding revision", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		mintTestClient(t, m, "cursor", auth.ProfileModeLocked, "ro")
		require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg", now)))
		before := dumpDB(t, m)
		_, err := m.CommitOAuthLogin(CommitLoginInput{
			Grant: newTestGrant("g", "reg", "cursor", now), Client: "cursor",
			ExpectedBindingRevision: bindingRevision("cursor", "other", auth.ProfileModeLocked), Now: now,
		})
		assert.ErrorIs(t, err, ErrOAuthBindingChanged)
		assert.Equal(t, before, dumpDB(t, m))
	})

	t.Run("existing-client decision whose client vanished", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg", now)))
		before := dumpDB(t, m)
		_, err := m.CommitOAuthLogin(CommitLoginInput{
			Grant: newTestGrant("g", "reg", "ghost", now), Client: "ghost",
			ExpectedBindingRevision: OAuthBindingAbsent, Now: now,
		})
		assert.ErrorIs(t, err, ErrOAuthBindingChanged)
		assert.Equal(t, before, dumpDB(t, m))
	})

	t.Run("invalid new-client binding writes nothing", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg", now)))
		before := dumpDB(t, m)
		_, err := m.CommitOAuthLogin(CommitLoginInput{
			Grant: newTestGrant("g", "reg", "c1", now), Client: "c1", NewClient: true,
			Mode: auth.ProfileModeLocked, Pin: "", ExpectedBindingRevision: OAuthBindingAbsent, Now: now,
		})
		require.Error(t, err)
		assert.Equal(t, before, dumpDB(t, m))
	})
}

func TestCommitOAuthLogin_TwoNewClientCommitsSecondFails(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("regA", now)))
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("regB", now)))
	in := func(reg, grant string) CommitLoginInput {
		return CommitLoginInput{
			Grant: newTestGrant(grant, reg, "newc", now), Client: "newc", NewClient: true,
			Mode: auth.ProfileModeSwitchable, ExpectedBindingRevision: OAuthBindingAbsent, Now: now,
		}
	}
	_, err := m.CommitOAuthLogin(in("regA", "gA"))
	require.NoError(t, err)
	before := dumpDB(t, m)
	_, err = m.CommitOAuthLogin(in("regB", "gB"))
	assert.ErrorIs(t, err, ErrOAuthClientExists, "the second is not turned into an existing-client login")
	assert.Equal(t, before, dumpDB(t, m))
}

func TestCommitOAuthLogin_ReplaceGrant(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	commitNew(t, m, "reg1", "old1", "myclient", "", auth.ProfileModeSwitchable, now)
	commitNew(t, m, "reg2", "other", "otherclient", "", auth.ProfileModeSwitchable, now)
	rev, _ := m.ClientBindingRevision("myclient")

	require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg3", now)))
	_, err := m.CommitOAuthLogin(CommitLoginInput{
		Grant: newTestGrant("new1", "reg3", "myclient", now), Client: "myclient",
		ExpectedBindingRevision: rev, ReplaceGrantID: "old1", Now: now,
	})
	require.NoError(t, err)
	old, err := m.GetOAuthGrant("old1")
	require.NoError(t, err)
	assert.True(t, old.Revoked)
	assert.Equal(t, OAuthRevokeReplaced, old.RevokeReason)
	_, err = m.getOAuthGrantByAccessHash("ah-old1")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound, "a revoked grant leaves the access index")

	// A grant of ANOTHER client is ignored.
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg4", now)))
	_, err = m.CommitOAuthLogin(CommitLoginInput{
		Grant: newTestGrant("new2", "reg4", "myclient", now), Client: "myclient",
		ExpectedBindingRevision: rev, ReplaceGrantID: "other", Now: now,
	})
	require.NoError(t, err)
	o, err := m.GetOAuthGrant("other")
	require.NoError(t, err)
	assert.False(t, o.Revoked)
}

func TestRotateOAuthRefresh(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	commitNew(t, m, "reg1", "grant1", "myclient", "", auth.ProfileModeSwitchable, now)

	t.Run("current secret rotates", func(t *testing.T) {
		later := now.Add(time.Hour)
		out, g, err := m.RotateOAuthRefresh("grant1", "rh-grant1", "rh2", "mcp_ort_zz", "ah2", later, 15*time.Minute, 30*24*time.Hour)
		require.NoError(t, err)
		assert.Equal(t, RefreshRotated, out)
		assert.Equal(t, "rh2", g.RefreshHash)
		assert.Equal(t, "ah2", g.AccessHash)
		assert.WithinDuration(t, later.Add(15*time.Minute), g.AccessExpiresAt, time.Second)
		assert.WithinDuration(t, later.Add(30*24*time.Hour), g.RefreshExpiresAt, time.Second)
		_, err = m.getOAuthGrantByAccessHash("ah-grant1")
		assert.ErrorIs(t, err, ErrOAuthGrantNotFound, "the old access token is gone")
		byNew, err := m.getOAuthGrantByAccessHash("ah2")
		require.NoError(t, err)
		assert.Equal(t, "grant1", byNew.GrantID)
	})

	t.Run("an old secret revokes the grant as replay", func(t *testing.T) {
		out, g, err := m.RotateOAuthRefresh("grant1", "rh-grant1", "rh3", "p", "ah3", now.Add(2*time.Hour), time.Minute, time.Hour)
		require.NoError(t, err)
		assert.Equal(t, RefreshReplay, out)
		assert.True(t, g.Revoked)
		assert.Equal(t, OAuthRevokeReplay, g.RevokeReason)
		_, err = m.getOAuthGrantByAccessHash("ah2")
		assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
	})

	t.Run("a revoked grant is invalid and nothing is written", func(t *testing.T) {
		before := dumpDB(t, m)
		out, _, err := m.RotateOAuthRefresh("grant1", "rh2", "rh4", "p", "ah4", now.Add(3*time.Hour), time.Minute, time.Hour)
		require.NoError(t, err)
		assert.Equal(t, RefreshInvalid, out)
		assert.Equal(t, before, dumpDB(t, m))
	})
}

func TestRotateOAuthRefresh_UnknownAndLapsedWriteNothing(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	commitNew(t, m, "reg1", "grant1", "myclient", "", auth.ProfileModeSwitchable, now)

	before := dumpDB(t, m)
	txBefore := dbTxID(t, m)

	out, _, err := m.RotateOAuthRefresh("nope", "rh-grant1", "x", "p", "y", now, time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, RefreshInvalid, out)

	// The refresh lapsed: even the CURRENT secret is refused and the
	// lifetime is not extended.
	lapsed := now.Add(31 * 24 * time.Hour)
	out, _, err = m.RotateOAuthRefresh("grant1", "rh-grant1", "x", "p", "y", lapsed, time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, RefreshInvalid, out)
	out, _, err = m.RotateOAuthRefresh("grant1", "wrong", "x", "p", "y", lapsed, time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, RefreshInvalid, out, "a wrong secret on a lapsed grant is not a replay either")

	assert.Equal(t, before, dumpDB(t, m))
	g, err := m.GetOAuthGrant("grant1")
	require.NoError(t, err)
	assert.False(t, g.Revoked)
	assert.WithinDuration(t, now.Add(30*24*time.Hour), g.RefreshExpiresAt, time.Second)
	assert.Equal(t, txBefore, dbTxID(t, m), "no write transaction was committed")
}

func TestRevokeTouchOAuthGrant(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	commitNew(t, m, "reg1", "grant1", "myclient", "", auth.ProfileModeSwitchable, now)

	require.NoError(t, m.TouchOAuthGrant("grant1", now.Add(time.Minute), "Claude Code"))
	g, _ := m.GetOAuthGrant("grant1")
	require.NotNil(t, g.LastUsedAt)
	assert.Equal(t, "Claude Code", g.ObservedClientName)
	require.NoError(t, m.TouchOAuthGrant("grant1", now.Add(2*time.Minute), ""))
	g, _ = m.GetOAuthGrant("grant1")
	assert.Equal(t, "Claude Code", g.ObservedClientName, "an empty name keeps the observed one")

	changed, err := m.RevokeOAuthGrant("grant1", OAuthRevokeAdmin, now)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = m.RevokeOAuthGrant("grant1", OAuthRevokeReplay, now)
	require.NoError(t, err)
	assert.False(t, changed)
	g, _ = m.GetOAuthGrant("grant1")
	assert.Equal(t, OAuthRevokeAdmin, g.RevokeReason, "the first reason stays")
	_, err = m.getOAuthGrantByAccessHash("ah-grant1")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
}

func TestSweepOAuthGrants_RetentionAndCascade(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	now := time.Now().UTC()
	old := now.Add(-70 * 24 * time.Hour)

	// client "a": one revoked-long-ago grant (its registration is not shared).
	commitNew(t, m, "regA", "gA", "clienta", "", auth.ProfileModeSwitchable, old)
	_, err := m.RevokeOAuthGrant("gA", OAuthRevokeAdmin, old)
	require.NoError(t, err)

	// client "b": one live grant and one lapsed-long-ago grant on two registrations.
	commitNew(t, m, "regB1", "gB1", "clientb", "", auth.ProfileModeSwitchable, now)
	revB, _ := m.ClientBindingRevision("clientb")
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("regB2", old)))
	lapsed := newTestGrant("gB2", "regB2", "clientb", old) // refresh lapsed 40 days ago
	_, err = m.CommitOAuthLogin(CommitLoginInput{Grant: lapsed, Client: "clientb", ExpectedBindingRevision: revB, Now: old})
	require.NoError(t, err)

	// client "c": revoked 10 days ago: kept for display.
	commitNew(t, m, "regC", "gC", "clientc", "", auth.ProfileModeSwitchable, now)
	_, err = m.RevokeOAuthGrant("gC", OAuthRevokeAdmin, now.Add(-10*24*time.Hour))
	require.NoError(t, err)

	n, err := m.SweepOAuthGrants(now)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	_, err = m.GetOAuthGrant("gA")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
	_, err = m.GetOAuthGrant("gB2")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
	for _, id := range []string{"gB1", "gC"} {
		_, err = m.GetOAuthGrant(id)
		assert.NoError(t, err, id)
	}

	// Last grant record of a registration: the registration goes.
	_, err = m.GetOAuthRegistration("regA")
	assert.ErrorIs(t, err, ErrOAuthRegistrationGone)
	_, err = m.GetOAuthRegistration("regB2")
	assert.ErrorIs(t, err, ErrOAuthRegistrationGone)
	for _, id := range []string{"regB1", "regC"} {
		_, err = m.GetOAuthRegistration(id)
		assert.NoError(t, err, id)
	}

	// Last grant record of a client: its binding entry goes; others stay.
	b, _ := m.GetOAuthClientBinding("clienta")
	assert.Nil(t, b)
	for _, id := range []string{"clientb", "clientc"} {
		b, _ = m.GetOAuthClientBinding(id)
		assert.NotNil(t, b, id)
	}
	used, _ := m.OAuthLoginEverUsed()
	assert.True(t, used, "the marker is never deleted")
}
