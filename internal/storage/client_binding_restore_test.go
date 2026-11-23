package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// assertCopiesAgree: after any RestorePins, every client that has both an
// API-key record and an OAuth entry holds the same pin and mode in both.
func assertCopiesAgree(t *testing.T, m *Manager) {
	t.Helper()
	entries, err := m.ListOAuthClientBindings()
	require.NoError(t, err)
	for id, e := range entries {
		apiPin, apiMode, apiOK, _, _, _ := bindings(t, m, id)
		if apiOK {
			assert.Equal(t, apiPin, e.ProfilePin, "%s pin copies differ", id)
			assert.Equal(t, apiMode, e.ProfileMode, "%s mode copies differ", id)
		}
	}
}

func restoreAndCheck(t *testing.T, m *Manager, before []auth.AgentToken) {
	t.Helper()
	require.NoError(t, m.RestorePins(before))
	assertCopiesAgree(t, m)
}

// firstLogin gives an existing API-key client its first browser login.
func firstLogin(t *testing.T, m *Manager, clientID string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, m.CreateOAuthRegistration(newTestReg("reg-"+clientID, now)))
	rev, err := m.ClientBindingRevision(clientID)
	require.NoError(t, err)
	_, err = m.CommitOAuthLogin(CommitLoginInput{
		Grant: newTestGrant("grant-"+clientID, "reg-"+clientID, clientID, now), Client: clientID,
		ExpectedBindingRevision: rev, Now: now,
	})
	require.NoError(t, err)
}

func TestRestorePins_DecisionTable(t *testing.T) {
	locked := auth.ProfileModeLocked
	newMgr := func(t *testing.T) *Manager {
		m, cleanup := setupTestStorageForAgentTokens(t)
		t.Cleanup(cleanup)
		return m
	}

	t.Run("a: an administrator reassigned in between", func(t *testing.T) {
		m := newMgr(t)
		clientWithBoth(t, m, "cursor", locked, "A")
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		_, _, err = m.UpdateClientCredentialBinding("cursor", "C", locked)
		require.NoError(t, err)
		restoreAndCheck(t, m, before)
		assertEqualBindings(t, m, "cursor", "C", locked)
	})

	t.Run("b: forgotten and re-created with the same value", func(t *testing.T) {
		m := newMgr(t)
		commitNew(t, m, "r", "g1", "webonly", "A", locked, time.Now())
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		_, err = m.ForgetClient("webonly")
		require.NoError(t, err)
		commitNew(t, m, "r2", "g2", "webonly", "B", locked, time.Now())
		restoreAndCheck(t, m, before)
		_, _, _, oPin, _, oOK := bindings(t, m, "webonly")
		require.True(t, oOK)
		assert.Equal(t, "B", oPin, "a different incarnation is never restored")
	})

	t.Run("c: a connect minted an API key bound to the same value", func(t *testing.T) {
		m := newMgr(t)
		commitNew(t, m, "r", "g1", "webonly", "A", locked, time.Now())
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		mintTestClient(t, m, "webonly", locked, "B")
		restoreAndCheck(t, m, before)
		assertEqualBindings(t, m, "webonly", "B", locked)
	})

	t.Run("d: the client got its first login after the repin", func(t *testing.T) {
		m := newMgr(t)
		mintTestClient(t, m, "cursor", locked, "A")
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		require.Len(t, before, 1, "no restore entry: it had no entry at repin time")
		firstLogin(t, m, "cursor")
		assertEqualBindings(t, m, "cursor", "B", locked)
		restoreAndCheck(t, m, before)
		assertEqualBindings(t, m, "cursor", "A", locked)
	})

	t.Run("d2: first login, entry differs from the record: left alone", func(t *testing.T) {
		m := newMgr(t)
		mintTestClient(t, m, "cursor", locked, "A")
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		firstLogin(t, m, "cursor")
		require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
			return putOAuthBindingTx(tx, "cursor", &OAuthClientBinding{ProfilePin: "Z", ProfileMode: locked})
		}))
		require.NoError(t, m.RestorePins(before))
		apiPin, _, _, oPin, _, _ := bindings(t, m, "cursor")
		assert.Equal(t, "B", apiPin)
		assert.Equal(t, "Z", oPin)
	})

	t.Run("e: plain rollback of a dual client", func(t *testing.T) {
		m := newMgr(t)
		clientWithBoth(t, m, "cursor", locked, "A")
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		assertEqualBindings(t, m, "cursor", "B", locked)
		restoreAndCheck(t, m, before)
		assertEqualBindings(t, m, "cursor", "A", locked)
	})

	t.Run("f: dual client, API-key record removed", func(t *testing.T) {
		m := newMgr(t)
		clientWithBoth(t, m, "cursor", locked, "A")
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		require.NoError(t, m.DeleteAgentToken("client-cursor"))
		restoreAndCheck(t, m, before)
		_, _, apiOK, oPin, _, oOK := bindings(t, m, "cursor")
		assert.False(t, apiOK)
		require.True(t, oOK)
		assert.Equal(t, "A", oPin)
	})

	t.Run("f2: an entry that is gone is not re-created", func(t *testing.T) {
		m := newMgr(t)
		commitNew(t, m, "r", "g1", "webonly", "A", locked, time.Now())
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		_, err = m.ForgetClient("webonly")
		require.NoError(t, err)
		restoreAndCheck(t, m, before)
		_, _, _, _, _, oOK := bindings(t, m, "webonly")
		assert.False(t, oOK)
	})

	t.Run("g: a same-value repin and restore changes nothing", func(t *testing.T) {
		m := newMgr(t)
		commitNew(t, m, "r", "g1", "webonly", "", auth.ProfileModeSwitchable, time.Now())
		mintTestClient(t, m, "webonly", locked, "P") // snapshot: unpinned, switchable
		before, err := m.RepinProfile("P", "P")
		require.NoError(t, err)
		restoreAndCheck(t, m, before)
		assertEqualBindings(t, m, "webonly", "P", locked)
		tok, err := m.GetAgentTokenByName("client-webonly")
		require.NoError(t, err)
		assert.Equal(t, "", tok.PriorProfilePin)
		assert.Equal(t, auth.ProfileModeSwitchable, tok.PriorProfileMode, "the connect snapshot is untouched")
	})

	t.Run("h: an API-key-only install is as before", func(t *testing.T) {
		m := newMgr(t)
		mintTestClient(t, m, "cursor", locked, "A")
		tok, raw := makeTestToken("agent-one")
		tok.ProfilePin = "A"
		require.NoError(t, m.CreateAgentToken(tok, raw, testHMACKey))

		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		require.Len(t, before, 2, "the two records only: no restore entries")
		for i := range before {
			assert.False(t, isRepinRestoreEntry(&before[i]))
			assert.Equal(t, "A", before[i].ProfilePin)
		}
		// Unconditional, as before: even a record that moved on is put back.
		_, _, err = m.UpdateClientCredentialBinding("cursor", "C", locked)
		require.NoError(t, err)
		restoreAndCheck(t, m, before)
		pin, _, _, _, _, oOK := bindings(t, m, "cursor")
		assert.Equal(t, "A", pin)
		assert.False(t, oOK, "no entry is ever created")
		assert.Empty(t, oauthBucketNames(t, m))
		got, err := m.GetAgentTokenByName("agent-one")
		require.NoError(t, err)
		assert.Equal(t, "A", got.ProfilePin)
	})
}

// TestBindingWriteIDChangesOnEveryWrite: a deleted-and-recreated entry never has
// the old id, even with the same pin and mode; ClientBindingRevision ignores it.
func TestBindingWriteIDChangesOnEveryWrite(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	commitNew(t, m, "r", "g1", "webonly", "A", auth.ProfileModeLocked, time.Now())
	id := func() string {
		b, err := m.GetOAuthClientBinding("webonly")
		require.NoError(t, err)
		require.NotNil(t, b)
		require.Len(t, b.WriteID, 16)
		return b.WriteID
	}
	first := id()
	rev, _ := m.ClientBindingRevision("webonly")

	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		return writeClientBindingTx(tx, "webonly", "A", auth.ProfileModeLocked) // same value
	}))
	second := id()
	assert.NotEqual(t, first, second)
	rev2, _ := m.ClientBindingRevision("webonly")
	assert.Equal(t, rev, rev2)

	_, err := m.ForgetClient("webonly")
	require.NoError(t, err)
	commitNew(t, m, "r2", "g2", "webonly", "A", auth.ProfileModeLocked, time.Now())
	assert.NotEqual(t, second, id())
}
