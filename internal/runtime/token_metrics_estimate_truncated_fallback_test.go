package runtime

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// newRealRetrieveToolsTestRuntime builds a Runtime with a specific
// tool_response_limit — newTestRuntime hardcodes 0 (disabled), which would
// mask the fallback this test exercises.
func newRealRetrieveToolsTestRuntime(t *testing.T, toolResponseLimit int) *Runtime {
	t.Helper()
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:           tempDir,
		Listen:            "127.0.0.1:0",
		ToolResponseLimit: toolResponseLimit,
		Servers:           []*config.ServerConfig{},
	}
	rt, err := New(cfg, filepath.Join(tempDir, "config.json"), zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// TestRuntime_RealRetrieveToolsAvgRespBytes_TruncatedFallback pins the review
// fix (usage_aggregate.go truncatedBuiltinOverstatesDelivery /
// contracts.ServerTokenMetrics.Estimated contract): a deployment whose
// retrieve_tools responses routinely exceed tool_response_limit must still
// see the Estimated flag flip to false once a real call completes — even
// though every observed call so far was truncated and so contributes nothing
// to the exact SIZE average — instead of the synthetic per-topK simulation
// persisting forever.
func TestRuntime_RealRetrieveToolsAvgRespBytes_TruncatedFallback(t *testing.T) {
	rt := newRealRetrieveToolsTestRuntime(t, 20000)

	_, ok := rt.realRetrieveToolsAvgRespBytes()
	assert.False(t, ok, "no retrieve_tools call has completed yet")

	rt.ActivityService().usage.Apply(&storage.ActivityRecord{
		Type:              storage.ActivityTypeInternalToolCall,
		ToolName:          "retrieve_tools",
		Status:            storage.ActivityStatusSuccess,
		ResponseBytes:     1_000_000,
		ResponseTruncated: true,
		Timestamp:         time.Now(),
	})

	avg, ok := rt.realRetrieveToolsAvgRespBytes()
	require.True(t, ok, "a real call completed (even truncated) — Estimated must flip to false")
	assert.EqualValues(t, 20000, avg, "tool_response_limit is the conservative stand-in for the delivered size")
}

// TestRuntime_RealRetrieveToolsAvgRespBytes_SizedCallPreferred asserts the
// fallback never shadows an exact, non-truncated observation.
func TestRuntime_RealRetrieveToolsAvgRespBytes_SizedCallPreferred(t *testing.T) {
	rt := newRealRetrieveToolsTestRuntime(t, 20000)

	rt.ActivityService().usage.Apply(&storage.ActivityRecord{
		Type:          storage.ActivityTypeInternalToolCall,
		ToolName:      "retrieve_tools",
		Status:        storage.ActivityStatusSuccess,
		ResponseBytes: 4000,
		Timestamp:     time.Now(),
	})

	avg, ok := rt.realRetrieveToolsAvgRespBytes()
	require.True(t, ok)
	assert.EqualValues(t, 4000, avg, "an exact sized call must win over the tool_response_limit fallback")
}

// TestRuntime_RealRetrieveToolsAvgRespBytes_NoLimitConfigured asserts the
// fallback does not fabricate a bogus size (0 or negative) when
// tool_response_limit is disabled (0, or the field's zero value).
func TestRuntime_RealRetrieveToolsAvgRespBytes_NoLimitConfigured(t *testing.T) {
	rt := newRealRetrieveToolsTestRuntime(t, 0)

	rt.ActivityService().usage.Apply(&storage.ActivityRecord{
		Type:              storage.ActivityTypeInternalToolCall,
		ToolName:          "retrieve_tools",
		Status:            storage.ActivityStatusSuccess,
		ResponseBytes:     1_000_000,
		ResponseTruncated: true,
		Timestamp:         time.Now(),
	})

	_, ok := rt.realRetrieveToolsAvgRespBytes()
	assert.False(t, ok, "no exact size and no usable limit to fall back to")
}
