package storage

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func putTokenForTest(tx *bbolt.Tx, hash []byte, tok *auth.AgentToken) error {
	data, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return tx.Bucket([]byte(AgentTokensBucket)).Put(hash, data)
}

// TestWriteClientBindingTx_ValidatesBeforeAnyWrite: an invalid binding is an
// error and neither copy is touched, for API-key-only, OAuth-only and dual clients.
func TestWriteClientBindingTx_ValidatesBeforeAnyWrite(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	mintTestClient(t, m, "keyonly", auth.ProfileModeLocked, "ro")
	commitNew(t, m, "r", "g1", "webonly", "ro", auth.ProfileModeLocked, time.Now())
	clientWithBoth(t, m, "both", auth.ProfileModeLocked, "ro")

	// (An API-key-only client is deliberately NOT validated: it is written as
	// before Spec 132, see TestAPIKeyOnlyClientsAreNotValidated.)
	for _, id := range []string{"webonly", "both"} {
		for _, bad := range []struct{ pin, mode string }{
			{"x", "sticky"}, {"", auth.ProfileModeLocked}, {"x", ""},
		} {
			before := dumpDB(t, m)
			err := m.db.db.Update(func(tx *bbolt.Tx) error {
				return writeClientBindingTx(tx, id, bad.pin, bad.mode)
			})
			require.Error(t, err, "%s %+v", id, bad)
			assert.Equal(t, before, dumpDB(t, m), "%s: the transaction rolled back", id)
		}
	}

	// A revoked/expired tombstone is still updated as before.
	_, err := m.RevokeClientAPIKey("keyonly")
	require.NoError(t, err)
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		return writeClientBindingTx(tx, "keyonly", "full", auth.ProfileModeLocked)
	}))
	pin, mode, _, _, _, _ := bindings(t, m, "keyonly")
	assert.Equal(t, "full", pin)
	assert.Equal(t, auth.ProfileModeLocked, mode)
}

func TestRepairOAuthClientBindings_InvalidAPIKeyBindingIsSkipped(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "ro")
	clientWithBoth(t, m, "zed", auth.ProfileModeLocked, "ro")
	// Corrupt cursor's API-key record (locked, empty pin) and drift zed's entry.
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		hash, tok, err := findClientTokenRecordLocked(tx, "cursor")
		require.NoError(t, err)
		tok.ProfilePin = ""
		return putTokenForTest(tx, hash, tok)
	}))
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		return oauthPutJSON(tx, OAuthClientBindingsBucket, "zed", &OAuthClientBinding{ProfilePin: "stale", ProfileMode: auth.ProfileModeLocked})
	}))

	repaired, skipped, err := m.RepairOAuthClientBindings()
	require.NoError(t, err)
	assert.Equal(t, []string{"zed"}, repaired)
	assert.Equal(t, []string{"cursor"}, skipped)
	_, _, _, oPin, _, _ := bindings(t, m, "cursor")
	assert.Equal(t, "ro", oPin, "the invalid binding was not copied")
}

func TestUpdateClientCredentialBinding_ErrorTextUnchangedForAPIKeyOnly(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	mintTestClient(t, m, "cursor", auth.ProfileModeLocked, "ro")
	// Refusals keep their pre-slice text.
	_, _, err := m.UpdateClientCredentialBinding("cursor", "", auth.ProfileModeLocked)
	require.EqualError(t, err, "a locked client credential requires a non-empty profile")
	_, _, err = m.UpdateClientCredentialBinding("cursor", "ro", "sticky")
	require.EqualError(t, err, `invalid profile mode "sticky"`)
	// The writer's own wording is never doubled (OAuth-side validation).
	commitNew(t, m, "r", "g1", "webonly", "ro", auth.ProfileModeLocked, time.Now())
	err = m.db.db.Update(func(tx *bbolt.Tx) error { return writeClientBindingTx(tx, "webonly", "x", "sticky") })
	require.EqualError(t, err, `invalid profile mode "sticky"`)
}

// TestRepinProfile_KeepsKeyOrderForRecordsAndHooks: existing records are handled
// in ONE list in bbolt key order, whether regular token or client, so `before`
// and the hook order are what they were before Spec 132.
func TestRepinProfile_KeepsKeyOrderForRecordsAndHooks(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	mintTestClient(t, m, "cursor", auth.ProfileModeLocked, "p")

	keyOrder := func() []string {
		var names []string
		require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
			return tx.Bucket([]byte(AgentTokensBucket)).ForEach(func(_, v []byte) error {
				var tk auth.AgentToken
				require.NoError(t, jsonUnmarshal(v, &tk))
				names = append(names, tk.Name)
				return nil
			})
		}))
		return names
	}
	// Add agent tokens pinned to p until the client record sits in the middle.
	for i := 0; i < 30; i++ {
		tok, raw := makeTestToken("agent-" + string(rune('a'+i)))
		tok.ProfilePin = "p"
		require.NoError(t, m.CreateAgentToken(tok, raw, testHMACKey))
		names := keyOrder()
		if len(names) >= 3 && names[0] != "client-cursor" && names[len(names)-1] != "client-cursor" {
			break
		}
	}
	want := keyOrder()
	require.NotEqual(t, "client-cursor", want[0])
	require.NotEqual(t, "client-cursor", want[len(want)-1])

	var hooked []int
	before, err := m.repinProfile("p", "q", func(w int) error { hooked = append(hooked, w); return nil })
	require.NoError(t, err)
	var gotNames []string
	for _, b := range before {
		gotNames = append(gotNames, b.Name)
	}
	assert.Equal(t, want, gotNames, "before is in bbolt key order, clients and tokens interleaved")
	var seq []int
	for i := range want {
		seq = append(seq, i)
	}
	assert.Equal(t, seq, hooked)
}

// TestRepinProfile_ClearsStaleRollbackSnapshotOfClientWithLogins: after a connect
// mint over an OAuth-only client and a reassignment, rolling the connect back must
// not bring the pre-reassignment binding back onto the existing logins.
func TestRepinProfile_ClearsStaleRollbackSnapshotOfClientWithLogins(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	commitNew(t, m, "r", "g1", "webonly", "", auth.ProfileModeSwitchable, time.Now())
	mintTestClient(t, m, "webonly", auth.ProfileModeLocked, "ro") // snapshot: switchable, unpinned

	_, err := m.RepinProfile("ro", "restricted")
	require.NoError(t, err)
	_, err = m.ForgetClientCredentialRestoringPrior("webonly")
	require.NoError(t, err)

	_, _, _, oPin, oMode, oOK := bindings(t, m, "webonly")
	require.True(t, oOK)
	assert.Equal(t, "restricted", oPin)
	assert.Equal(t, auth.ProfileModeLocked, oMode)
	g, err := m.GetOAuthGrant("g1")
	require.NoError(t, err)
	assert.False(t, g.Revoked)
}

func TestRestorePins_OAuthSideIsCompareAndSet(t *testing.T) {
	t.Run("a: dual client, API-key record removed, entry still restored", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "ro")
		before, err := m.RepinProfile("ro", "full")
		require.NoError(t, err)
		require.NoError(t, m.DeleteAgentToken("client-cursor"))
		require.NoError(t, m.RestorePins(before))
		_, _, apiOK, oPin, oMode, oOK := bindings(t, m, "cursor")
		assert.False(t, apiOK)
		require.True(t, oOK)
		assert.Equal(t, "ro", oPin)
		assert.Equal(t, auth.ProfileModeLocked, oMode)
	})

	t.Run("a2: dual client with its record present restores both", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "ro")
		before, err := m.RepinProfile("ro", "full")
		require.NoError(t, err)
		require.NoError(t, m.RestorePins(before))
		assertEqualBindings(t, m, "cursor", "ro", auth.ProfileModeLocked)
	})

	t.Run("b: forgotten and re-created client keeps its new binding", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		commitNew(t, m, "r", "g1", "webonly", "A", auth.ProfileModeLocked, time.Now())
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		_, err = m.ForgetClient("webonly")
		require.NoError(t, err)
		commitNew(t, m, "r2", "g2", "webonly", "P", auth.ProfileModeLocked, time.Now())
		require.NoError(t, m.RestorePins(before))
		_, _, _, oPin, _, _ := bindings(t, m, "webonly")
		assert.Equal(t, "P", oPin)
	})

	t.Run("b2: a forgotten entry is not re-created", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		commitNew(t, m, "r", "g1", "webonly", "A", auth.ProfileModeLocked, time.Now())
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		_, err = m.ForgetClient("webonly")
		require.NoError(t, err)
		require.NoError(t, m.RestorePins(before))
		_, _, _, _, _, oOK := bindings(t, m, "webonly")
		assert.False(t, oOK)
	})

	t.Run("c: a connect minted after the repin keeps both copies", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		commitNew(t, m, "r", "g1", "webonly", "A", auth.ProfileModeLocked, time.Now())
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		mintTestClient(t, m, "webonly", auth.ProfileModeLocked, "P")
		require.NoError(t, m.RestorePins(before))
		assertEqualBindings(t, m, "webonly", "P", auth.ProfileModeLocked)
	})

	t.Run("d: a stored record with an empty hash is not a restore entry", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		commitNew(t, m, "r", "g1", "webonly", "A", auth.ProfileModeLocked, time.Now())
		before, err := m.RepinProfile("A", "B")
		require.NoError(t, err)
		require.Len(t, before, 1)
		assert.True(t, isRepinRestoreEntry(&before[0]))

		// Same shape, no marker: ignored entirely.
		lookalike := auth.AgentToken{Name: "client-webonly", Kind: auth.KindClient, ClientID: "webonly",
			ProfilePin: "A", ProfileMode: auth.ProfileModeLocked, TokenHash: ""}
		assert.False(t, isRepinRestoreEntry(&lookalike))
		require.NoError(t, m.RestorePins([]auth.AgentToken{lookalike}))
		_, _, _, oPin, _, _ := bindings(t, m, "webonly")
		assert.Equal(t, "B", oPin)

		// The real entry still restores.
		require.NoError(t, m.RestorePins(before))
		_, _, _, oPin, _, _ = bindings(t, m, "webonly")
		assert.Equal(t, "A", oPin)
	})
}

func TestRepinProfile_FailureKeepsErrorTextAndRollsBack(t *testing.T) {
	m, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()
	mintTestClient(t, m, "cursor", auth.ProfileModeLocked, "p")
	boom := errors.New("boom")
	_, err := m.repinProfile("p", "q", func(int) error { return boom })
	require.ErrorIs(t, err, boom)
	pin, _, _, _, _, _ := bindings(t, m, "cursor")
	assert.Equal(t, "p", pin)
}
