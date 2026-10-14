package upstream

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
)

// Spec 116 PR2b (#1607): the manager binds every instance it builds to the
// committed credential binding of the generation it was built for.

func bound(name string, gen int64, b commitview.CredentialBinding, url string) GenerationChange {
	ch := present(name, gen, genCfg(name, url))
	ch.Binding = b
	return ch
}

// An instance carries the binding the commit recorded with its generation,
// including the replacement a same-generation ReplaceInstanceAt builds.
func TestManagerBinding_InstanceCarriesCommittedBinding(t *testing.T) {
	m := newGenTestManager(t)
	b1 := commitview.CredentialBinding{Incarnation: 5, OAuthEpoch: 5}
	m.AdvanceGenerations([]GenerationChange{bound("acme", 5, b1, "http://127.0.0.1:1/a")})
	require.NoError(t, m.AddServerConfigAt("acme", genCfg("acme", "http://127.0.0.1:1/a"), 5))
	i1 := registered(t, m, "acme")
	assert.Equal(t, b1, i1.CredentialBinding())
	got, ok := m.CurrentCredentialBinding("acme")
	require.True(t, ok)
	assert.Equal(t, b1, got)

	i2, err := m.ReplaceInstanceAt("acme", 5, i1, nil)
	require.NoError(t, err)
	assert.NotSame(t, i1, i2)
	assert.Equal(t, b1, i2.CredentialBinding(), "a restart reuses the binding of the instance it replaces")
}

// A binding move always replaces the instance, even when the connection
// settings are equal (it would otherwise be restamped in place): an
// instance never serves a binding it was not built for.
func TestManagerBinding_BindingMoveUnregisters(t *testing.T) {
	m := newGenTestManager(t)
	b1 := commitview.CredentialBinding{Incarnation: 5, OAuthEpoch: 5}
	m.AdvanceGenerations([]GenerationChange{bound("acme", 5, b1, "http://127.0.0.1:1/a")})
	require.NoError(t, m.AddServerConfigAt("acme", genCfg("acme", "http://127.0.0.1:1/a"), 5))
	i1 := registered(t, m, "acme")

	b2 := commitview.CredentialBinding{Incarnation: 5, OAuthEpoch: 6}
	res := m.AdvanceGenerations([]GenerationChange{bound("acme", 6, b2, "http://127.0.0.1:1/a")})
	require.Len(t, res.Unregistered, 1)
	assert.Same(t, i1, res.Unregistered[0])
	_, ok := m.GetClient("acme")
	assert.False(t, ok, "the instance of the old binding is unregistered")
	assert.True(t, i1.Retired(), "and retired")

	require.NoError(t, m.AddServerConfigAt("acme", genCfg("acme", "http://127.0.0.1:1/a"), 6))
	assert.Equal(t, b2, registered(t, m, "acme").CredentialBinding())
}

// T2.14 (review R6.1) at the manager: a worker for (acme, G1) that reaches
// construction after a newer commit wrote acme's row (step 6) but before
// that commit advanced the manager (step 7) passes the desired-generation
// check, and the committed-view check refuses it: no instance, so no OAuth
// config is built for the old view.
func TestManagerBinding_CommittedViewCheckRefusesConstruction(t *testing.T) {
	m := newGenTestManager(t)
	b1 := commitview.CredentialBinding{Incarnation: 5, OAuthEpoch: 5}
	m.AdvanceGenerations([]GenerationChange{bound("acme", 5, b1, "http://127.0.0.1:1/a")})

	row := commitview.ServerGen{Gen: 6, Incarnation: 5, OAuthEpoch: 6} // the newer commit's row
	var checked []commitview.ServerGen
	m.SetCommittedViewChecker(func(name string, sg commitview.ServerGen) error {
		checked = append(checked, sg)
		if sg != row {
			return commitview.ErrStaleGeneration
		}
		return nil
	})
	var seen []commitview.ServerGen
	restore := SetBeforeBindingCheckHookForTest(func(name string, sg commitview.ServerGen) { seen = append(seen, sg) })
	defer restore()

	err := m.AddServerConfigAt("acme", genCfg("acme", "http://127.0.0.1:1/a"), 5)
	assert.ErrorIs(t, err, ErrStaleGeneration)
	_, ok := m.GetClient("acme")
	assert.False(t, ok, "no instance is built for a committed view the row moved past")
	require.Len(t, checked, 1)
	assert.Equal(t, commitview.ServerGen{Gen: 5, Incarnation: 5, OAuthEpoch: 5}, checked[0], "the check uses the manager's committed view, never the row")
	assert.Equal(t, checked, seen)

	// Once the newer commit advanced the manager, its own worker builds.
	m.AdvanceGenerations([]GenerationChange{bound("acme", 6, row.Binding(), "http://127.0.0.1:1/a")})
	require.NoError(t, m.AddServerConfigAt("acme", genCfg("acme", "http://127.0.0.1:1/a"), 6))
	assert.Equal(t, row.Binding(), registered(t, m, "acme").CredentialBinding())
}
