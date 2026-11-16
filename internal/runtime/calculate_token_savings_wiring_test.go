package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 109-k / audit finding F-Token: no test anywhere calls
// Runtime.CalculateTokenSavings — token_metrics_estimate_test.go covers only
// the pure resolveAverageQueryResultSize decision and usage_aggregate_test.go
// only the counter folding, leaving the actual runtime glue (the Estimated
// flag wiring, realRetrieveToolsAvgRespBytes, and the SavedTokens/percentage
// recompute with its zero-total clamp) untested at every level. This exercises
// CalculateTokenSavings itself, end to end, through a real Runtime.
func TestRuntime_CalculateTokenSavings_Wiring(t *testing.T) {
	t.Run("no real retrieve_tools call yet: estimated, synthetic size", func(t *testing.T) {
		rt := newTestRuntime(t)

		metrics, err := rt.CalculateTokenSavings()
		require.NoError(t, err)
		assert.True(t, metrics.Estimated, "no real retrieve_tools call has completed yet")
		assert.Positive(t, metrics.AverageQueryResultSize, "the synthetic simulation must still produce a size")
	})

	t.Run("zero-total clamp: no upstream tools means SavedTokens/percentage stay zero, no div-by-zero", func(t *testing.T) {
		rt := newTestRuntime(t)

		metrics, err := rt.CalculateTokenSavings()
		require.NoError(t, err)
		assert.Equal(t, 0, metrics.TotalServerToolListSize, "no upstream servers are connected in this fixture")
		assert.Equal(t, 0, metrics.SavedTokens)
		assert.Zero(t, metrics.SavedTokensPercentage)
	})

	t.Run("a real retrieve_tools call flips Estimated and threads the real size through", func(t *testing.T) {
		rt := newTestRuntime(t)

		before, err := rt.CalculateTokenSavings()
		require.NoError(t, err)
		require.True(t, before.Estimated)

		rt.ActivityService().usage.Apply(&storage.ActivityRecord{
			Type:          storage.ActivityTypeInternalToolCall,
			ToolName:      "retrieve_tools",
			Status:        storage.ActivityStatusSuccess,
			ResponseBytes: 4000,
			Timestamp:     time.Now(),
		})

		after, err := rt.CalculateTokenSavings()
		require.NoError(t, err)
		assert.False(t, after.Estimated, "a real completed retrieve_tools call must flip Estimated to false")
		// bytesPerTokenApprox = 4 (token_metrics_estimate.go): 4000 bytes -> 1000.
		assert.Equal(t, 1000, after.AverageQueryResultSize)
		assert.NotEqual(t, before.AverageQueryResultSize, after.AverageQueryResultSize,
			"the real size must differ from the synthetic simulation, or the wiring silently kept the old value")
	})

	t.Run("a truncated-only retrieve_tools call still flips Estimated via the tool_response_limit fallback", func(t *testing.T) {
		rt := newRealRetrieveToolsTestRuntime(t, 20000)

		rt.ActivityService().usage.Apply(&storage.ActivityRecord{
			Type:              storage.ActivityTypeInternalToolCall,
			ToolName:          "retrieve_tools",
			Status:            storage.ActivityStatusSuccess,
			ResponseBytes:     1_000_000,
			ResponseTruncated: true,
			Timestamp:         time.Now(),
		})

		metrics, err := rt.CalculateTokenSavings()
		require.NoError(t, err)
		assert.False(t, metrics.Estimated, "a real call completed (even truncated) must still flip Estimated")
		// bytesPerTokenApprox = 4: 20000 / 4 = 5000.
		assert.Equal(t, 5000, metrics.AverageQueryResultSize)
	})
}

// CalculateTokenSavings reads tool lists from the StateView (no live
// tools/list per upstream on every usage poll) and reuses its tokenized
// estimate until a server's tool set changes.
func TestRuntime_CalculateTokenSavings_UsesStateViewAndCaches(t *testing.T) {
	rt := newTestRuntime(t)
	sup := rt.Supervisor()
	require.NotNil(t, sup)
	view := sup.StateView()
	require.NotNil(t, view)

	setTools := func(epoch int64, names ...string) {
		view.UpdateServer("alpha", func(s *stateview.ServerStatus) {
			s.Connected = true
			s.ToolsDiscovered = true
			s.DiscoveryEpoch = epoch
			s.Tools = nil
			for _, n := range names {
				s.Tools = append(s.Tools, stateview.ToolInfo{
					Name:        n,
					Description: "a tool with a reasonably long description so it has tokens",
					InputSchema: map[string]interface{}{"type": "object"},
				})
			}
		})
	}

	setTools(1, "one", "two")
	first, err := rt.CalculateTokenSavings()
	require.NoError(t, err)
	require.Positive(t, first.TotalServerToolListSize, "tools in the StateView must be counted")
	require.Contains(t, first.PerServerToolListSizes, "alpha")

	// Callers mutating the result must not change the cached estimate.
	first.PerServerToolListSizes["alpha"] = -1
	again, err := rt.CalculateTokenSavings()
	require.NoError(t, err)
	assert.Equal(t, first.TotalServerToolListSize, again.TotalServerToolListSize)
	assert.Positive(t, again.PerServerToolListSizes["alpha"])

	// A new discovery (new epoch, more tools) invalidates the cache.
	setTools(2, "one", "two", "three")
	grown, err := rt.CalculateTokenSavings()
	require.NoError(t, err)
	assert.Greater(t, grown.TotalServerToolListSize, first.TotalServerToolListSize)

	// A disconnected server is left out, as before.
	view.UpdateServer("alpha", func(s *stateview.ServerStatus) { s.Connected = false })
	gone, err := rt.CalculateTokenSavings()
	require.NoError(t, err)
	assert.Equal(t, 0, gone.TotalServerToolListSize)
}

// A tools/list_changed re-discovery on the SAME connection keeps the discovery
// epoch (it is the connection token), and an edited definition keeps the tool
// count too. The estimate must still be recounted (#1911).
func TestRuntime_CalculateTokenSavings_RecountsOnSameEpochDefinitionChange(t *testing.T) {
	rt := newTestRuntime(t)
	view := rt.Supervisor().StateView()

	setTool := func(description string, schema map[string]interface{}) {
		view.UpdateServer("alpha", func(s *stateview.ServerStatus) {
			s.Connected = true
			s.ToolsDiscovered = true
			s.DiscoveryEpoch = 7
			s.Tools = []stateview.ToolInfo{{Name: "one", Description: description, InputSchema: schema}}
		})
	}

	setTool("short", map[string]interface{}{"type": "object"})
	before, err := rt.CalculateTokenSavings()
	require.NoError(t, err)

	setTool("a much longer description that costs clearly more tokens than the short one did before the upstream edited it", map[string]interface{}{"type": "object"})
	longer, err := rt.CalculateTokenSavings()
	require.NoError(t, err)
	assert.Greater(t, longer.TotalServerToolListSize, before.TotalServerToolListSize,
		"an edited description on the same connection must be recounted")

	setTool("short", map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string", "description": "file to read"}},
	})
	schema, err := rt.CalculateTokenSavings()
	require.NoError(t, err)
	assert.Greater(t, schema.TotalServerToolListSize, before.TotalServerToolListSize,
		"an edited input schema on the same connection must be recounted")
}

// The StateView lags a removal commit: until the supervisor reconciles it still
// holds the removed server's row and tools. Token savings must not count a
// server the committed configuration no longer has, nor hand a same-name
// replacement its predecessor's definitions (the guard GetServerTools applies).
func TestRuntime_CalculateTokenSavings_SkipsRemovedAndReplacedServers(t *testing.T) {
	cfgA := explicitServer(&config.ServerConfig{Name: "acme", URL: "http://127.0.0.1:9/a", Protocol: "http", Enabled: true})
	rt, _ := newSpec116Runtime(t, nil, cfgA)
	incA, ok := rt.ServerIncarnation("acme")
	require.True(t, ok)
	rt.supervisor.StateView().UpdateServer("acme", func(status *stateview.ServerStatus) {
		status.Connected = true
		status.Incarnation = incA
		status.Tools = []stateview.ToolInfo{{Name: "a_tool", Description: "the removed server's definition"}}
		status.ToolCount = 1
		status.ToolsDiscovered = true
	})

	counted, err := rt.CalculateTokenSavings()
	require.NoError(t, err)
	require.Positive(t, counted.PerServerToolListSizes["acme"], "control: the committed server is counted")

	// Removal only; the supervisor (not started) leaves the row in the view.
	_, err = rt.Commit("remove_server", func(tx *CommitTx) error { return tx.RemoveServer("acme") })
	require.NoError(t, err)
	removed, err := rt.CalculateTokenSavings()
	require.NoError(t, err)
	assert.NotContains(t, removed.PerServerToolListSizes, "acme", "a removed server is not counted")
	assert.Equal(t, 0, removed.TotalServerToolListSize)

	// Same-name replacement: the view still carries the old incarnation.
	_, err = rt.Commit("create_server", func(tx *CommitTx) error {
		return tx.CreateServer(explicitServer(&config.ServerConfig{Name: "acme", URL: "http://127.0.0.1:9/b", Protocol: "http", Enabled: true}))
	})
	require.NoError(t, err)
	replaced, err := rt.CalculateTokenSavings()
	require.NoError(t, err)
	assert.NotContains(t, replaced.PerServerToolListSizes, "acme", "a replacement does not inherit the removed server's tools")
}
