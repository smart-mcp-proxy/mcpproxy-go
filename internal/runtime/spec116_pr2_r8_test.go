package runtime

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 116 PR2 review round 8 runtime tests.

// seedLaggingView publishes tools on the StateView under incarnation inc, as
// reconcile + discovery would have for that incarnation.
func seedLaggingView(rt *Runtime, name string, inc int64, tools ...stateview.ToolInfo) {
	rt.supervisor.StateView().UpdateServer(name, func(status *stateview.ServerStatus) {
		status.Connected = true
		status.State = "ready"
		status.Incarnation = inc
		status.Tools = tools
		status.ToolCount = len(tools)
		status.ToolsDiscovered = true
	})
}

// The bulk toggle and the single toggle bind to the replacement B's
// incarnation, but read the tool inventory (bulk) and the synthesized
// record's contract (both) from the StateView. A view still holding the
// removed server A's tools must not have its definitions persisted as B's
// approval records. The control (bulk disable bound to A before the removal)
// proves the view is consulted when it matches.
func TestToolToggle_R8_LaggingViewNotPersistedUnderReplacementIncarnation(t *testing.T) {
	cfgA := explicitServer(&config.ServerConfig{Name: "acme", URL: "http://127.0.0.1:9/a", Protocol: "http", Enabled: true})
	rt, _ := newSpec116Runtime(t, nil, cfgA)
	incA, ok := rt.ServerIncarnation("acme")
	require.True(t, ok)

	aOnly := stateview.ToolInfo{Name: "a_only", Description: "A's definition",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"a": map[string]interface{}{"type": "string"}}}}
	shared := stateview.ToolInfo{Name: "shared", Description: "A's shared definition",
		InputSchema: map[string]interface{}{"type": "object"}}
	seedLaggingView(rt, "acme", incA, aOnly, shared)

	// Control: bound to A while A is committed, the view is the inventory.
	changed, err := rt.SetAllToolsEnabledAt("acme", false, "test", incA)
	require.NoError(t, err)
	require.Equal(t, 2, changed, "control: the view's tools are toggled under their own incarnation")
	rec, err := rt.storageManager.GetToolApproval("acme", "a_only")
	require.NoError(t, err)
	require.Equal(t, "A's definition", rec.CurrentDescription, "control: the matching view supplies the contract")

	// Remove A and re-add B; reconcile is parked, so the view keeps A's tools
	// under A's incarnation (re-published here after the removal purged them).
	removeAndReAdd(t, rt, explicitServer(&config.ServerConfig{
		Name: "acme", URL: "http://127.0.0.1:9/b", Protocol: "http", Enabled: true}))
	incB, ok := rt.ServerIncarnation("acme")
	require.True(t, ok)
	require.NotEqual(t, incA, incB)
	seedLaggingView(rt, "acme", incA, aOnly, shared)
	_, err = rt.storageManager.GetToolApproval("acme", "a_only")
	require.True(t, errors.Is(err, storage.ErrToolApprovalNotFound), "the removal purged A's records")

	// Bulk disable bound to B.
	_, err = rt.SetAllToolsEnabledAt("acme", false, "test", incB)
	require.NoError(t, err)
	_, err = rt.storageManager.GetToolApproval("acme", "a_only")
	assert.True(t, errors.Is(err, storage.ErrToolApprovalNotFound),
		"a bulk disable bound to B must not file a record for a tool only A listed (err=%v)", err)

	// Single disable of a name both incarnations may list, bound to B.
	require.NoError(t, rt.SetToolEnabledAt("acme", "shared", false, "test", incB))
	rec, err = rt.storageManager.GetToolApproval("acme", "shared")
	require.NoError(t, err)
	assert.NotEqual(t, "A's shared definition", rec.CurrentDescription,
		"B's record must not carry A's description")
	assert.Empty(t, rec.CurrentSchema, "B's record must not carry A's schema")
	assert.Empty(t, rec.CurrentHash, "B's record must not carry a hash of A's contract")
}
