package httpapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1084: /api/v1/status embedded a status snapshot whose upstream_stats and
// tools_indexed were a point-in-time capture nothing refreshed. They sat at
// "Connecting"/0 for the life of a process whose servers were long Ready, while
// the sibling top-level upstream_stats was correct.
func staleSnapshot() map[string]interface{} {
	return map[string]interface{}{
		"running":     true,
		"phase":       "Ready",
		"listen_addr": "127.0.0.1:8080",
		"upstream_stats": map[string]interface{}{
			"servers":     map[string]interface{}{"everything": map[string]interface{}{"state": "Connecting"}},
			"total_tools": 0,
		},
		"tools_indexed": 0,
	}
}

func liveStats() map[string]interface{} {
	return map[string]interface{}{
		"servers":     map[string]interface{}{"everything": map[string]interface{}{"state": "Ready"}},
		"total_tools": 13,
	}
}

func TestWithLiveUpstreamStats_RefreshesTheStaleNestedFields(t *testing.T) {
	out, ok := withLiveUpstreamStats(t.Context(), staleSnapshot(), liveStats()).(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, 13, out["tools_indexed"], "tools_indexed must track the live count")
	stats, ok := out["upstream_stats"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 13, stats["total_tools"])

	servers := stats["servers"].(map[string]interface{})
	everything := servers["everything"].(map[string]interface{})
	assert.Equal(t, "Ready", everything["state"],
		"the nested per-server state must not remain stuck at Connecting")
}

// Unrelated keys survive: clients read data.status.* and the fix must refresh,
// not replace, the snapshot.
func TestWithLiveUpstreamStats_PreservesEverythingElse(t *testing.T) {
	out := withLiveUpstreamStats(t.Context(), staleSnapshot(), liveStats()).(map[string]interface{})
	assert.Equal(t, true, out["running"])
	assert.Equal(t, "Ready", out["phase"])
	assert.Equal(t, "127.0.0.1:8080", out["listen_addr"])
}

// It must COPY. The SSE path passes a map owned by the publisher, and editing
// it in place would mutate something another goroutine may still hold.
func TestWithLiveUpstreamStats_DoesNotMutateItsInput(t *testing.T) {
	in := staleSnapshot()
	withLiveUpstreamStats(t.Context(), in, liveStats())

	assert.Equal(t, 0, in["tools_indexed"], "the caller's snapshot must be untouched")
	original := in["upstream_stats"].(map[string]interface{})
	assert.Equal(t, 0, original["total_tools"])
}

// Degenerate inputs must pass through rather than panic or blank the payload.
func TestWithLiveUpstreamStats_PassesThroughWhenItCannotRefresh(t *testing.T) {
	assert.Nil(t, withLiveUpstreamStats(t.Context(), nil, liveStats()))

	// A non-map status (a future typed snapshot) is returned untouched.
	assert.Equal(t, "opaque", withLiveUpstreamStats(t.Context(), "opaque", liveStats()))

	// No live stats to apply: keep what we had rather than emit nothing.
	unchanged := withLiveUpstreamStats(t.Context(), staleSnapshot(), nil).(map[string]interface{})
	assert.Equal(t, 0, unchanged["tools_indexed"])

	// total_tools absent or wrongly typed leaves tools_indexed alone rather
	// than zeroing it.
	partial := withLiveUpstreamStats(t.Context(), staleSnapshot(), map[string]interface{}{"servers": map[string]interface{}{}}).(map[string]interface{})
	assert.Equal(t, 0, partial["tools_indexed"])
	assert.NotNil(t, partial["upstream_stats"])
}
