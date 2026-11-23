package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// rawEditClient rewrites the client's stored API-key record without any validation.
func rawEditClient(t *testing.T, m *Manager, id string, edit func(*auth.AgentToken)) {
	t.Helper()
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		hash, tok, err := findClientTokenRecordLocked(tx, id)
		require.NoError(t, err)
		require.NotNil(t, tok)
		edit(tok)
		return putTokenForTest(tx, hash, tok)
	}))
}

var badPriors = []struct{ name, pin, mode string }{
	{"invalid mode", "p", "sticky"},
	{"locked with an empty pin", "", auth.ProfileModeLocked},
}

// TestAPIKeyOnlyClientsAreNotValidated: a client with no browser login behaves
// exactly as before Spec 132, even when its stored binding is malformed, so one
// bad legacy record cannot block a profile rename or a connect rollback.
func TestAPIKeyOnlyClientsAreNotValidated(t *testing.T) {
	t.Run("repin over an invalid mode", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		mintTestClient(t, m, "cursor", auth.ProfileModeLocked, "p")
		rawEditClient(t, m, "cursor", func(tk *auth.AgentToken) { tk.ProfileMode = "sticky" })

		before, err := m.RepinProfile("p", "q")
		require.NoError(t, err)
		require.Len(t, before, 1, "only the client record, no restore entries")
		assert.Equal(t, "p", before[0].ProfilePin)
		assert.Equal(t, "sticky", before[0].ProfileMode)
		pin, mode, _, _, _, _ := bindings(t, m, "cursor")
		assert.Equal(t, "q", pin)
		assert.Equal(t, "sticky", mode, "the mode is left as it was")
		require.NoError(t, m.RestorePins(before))
		pin, _, _, _, _, _ = bindings(t, m, "cursor")
		assert.Equal(t, "p", pin)
	})

	for _, prior := range badPriors {
		t.Run("connect rollback with a prior that is "+prior.name, func(t *testing.T) {
			m, cleanup := setupTestStorageForAgentTokens(t)
			defer cleanup()
			mintTestClient(t, m, "cursor", auth.ProfileModeSwitchable, "")
			rawEditClient(t, m, "cursor", func(tk *auth.AgentToken) {
				tk.PriorProfilePin, tk.PriorProfileMode = prior.pin, prior.mode
			})
			rec, err := m.ForgetClientCredentialRestoringPrior("cursor")
			require.NoError(t, err)
			assert.True(t, rec.Revoked)
			assert.Equal(t, prior.pin, rec.ProfilePin)
			assert.Equal(t, prior.mode, rec.ProfileMode)
			pin, mode, _, _, _, _ := bindings(t, m, "cursor")
			assert.Equal(t, prior.pin, pin)
			assert.Equal(t, prior.mode, mode)
		})
	}
}

// TestDualClientsWithInvalidBindingsFailClosed: with a browser-login copy the
// same malformed values are refused and nothing is written.
func TestDualClientsWithInvalidBindingsFailClosed(t *testing.T) {
	t.Run("repin", func(t *testing.T) {
		m, cleanup := setupTestStorageForAgentTokens(t)
		defer cleanup()
		clientWithBoth(t, m, "cursor", auth.ProfileModeLocked, "p")
		rawEditClient(t, m, "cursor", func(tk *auth.AgentToken) { tk.ProfileMode = "sticky" })
		before := dumpDB(t, m)
		_, err := m.RepinProfile("p", "q")
		require.Error(t, err)
		assert.Equal(t, before, dumpDB(t, m))
	})

	for _, prior := range badPriors {
		t.Run("rollback with a prior that is "+prior.name, func(t *testing.T) {
			m, cleanup := setupTestStorageForAgentTokens(t)
			defer cleanup()
			clientWithBoth(t, m, "cursor", auth.ProfileModeSwitchable, "")
			rawEditClient(t, m, "cursor", func(tk *auth.AgentToken) {
				tk.PriorProfilePin, tk.PriorProfileMode = prior.pin, prior.mode
			})
			before := dumpDB(t, m)
			_, err := m.ForgetClientCredentialRestoringPrior("cursor")
			require.Error(t, err)
			assert.Equal(t, before, dumpDB(t, m))
		})
	}
}
