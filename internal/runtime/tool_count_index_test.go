package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// GetToolCount feeds the telemetry heartbeat's tool_count, which is the field
// the activation funnel is keyed on. It used to read the upstream manager's
// per-client tool-count cache — but every indexing pass zeroes that cache as its
// last step (lifecycle.go's InvalidateAllToolCountCaches), so the heartbeat
// reported 0 for installs holding a fully populated index. The only paths that
// refilled the cache without re-zeroing it were UI/API-triggered ListTools
// calls, which biased the metric toward installs whose owner opened the
// dashboard.
//
// These tests pin the corrected contract: the count comes from the Bleve index,
// which holds one document per tool and is durable across restarts.
func TestGetToolCount_ReadsIndexDocumentCount(t *testing.T) {
	r, mgr := newProfileTestRuntime(t, &config.Config{})

	// r.upstreamManager is nil, so the legacy cache path yields 0 — the same
	// value it yields on a real install right after a sweep invalidates it.
	require.Equal(t, 0, extractToolCount(nil), "precondition: the cache path reports zero here")

	require.NoError(t, mgr.BatchIndexTools([]*config.ToolMetadata{
		toolMeta("github", "create_issue"),
		toolMeta("github", "list_repos"),
		toolMeta("slack", "post_message"),
	}))
	assert.Equal(t, 3, r.GetToolCount(), "tool_count must reflect the indexed tools")

	// It tracks the index rather than reporting a constant.
	require.NoError(t, mgr.IndexTool(toolMeta("slack", "list_channels")))
	assert.Equal(t, 4, r.GetToolCount(), "a newly indexed tool must be counted")

	require.NoError(t, mgr.DeleteServerTools("github"))
	assert.Equal(t, 2, r.GetToolCount(), "removing a server's tools must lower the count")
}

// An install with no index manager at all must still report a number rather
// than panicking — telemetry calls this on every heartbeat.
func TestGetToolCount_NoIndexManagerIsZero(t *testing.T) {
	r := &Runtime{}
	assert.Equal(t, 0, r.GetToolCount())
}
