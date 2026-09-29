package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
