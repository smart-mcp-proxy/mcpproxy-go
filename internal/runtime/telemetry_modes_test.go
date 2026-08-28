package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The telemetry getters must read the LIVE config, not the construction-time
// one: both modes are hot-reloadable, and reporting the startup value would
// measure the wrong thing precisely for the installs that experimented.
func TestTelemetryStats_SerializationModeGettersFollowLiveConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	// Unset normalizes to "full" on both axes, so "never configured" and
	// "explicitly full" land in one bucket rather than two.
	assert.Equal(t, config.ToolResponseModeFull, rt.GetToolResponseMode())
	assert.Equal(t, config.DirectToolResponseModeFull, rt.GetDirectToolResponseMode())

	live := rt.Config()
	live.ToolResponseMode = config.ToolResponseModeCompact
	live.DirectToolResponseMode = config.DirectToolResponseModeDeferred

	assert.Equal(t, config.ToolResponseModeCompact, rt.GetToolResponseMode(),
		"the getter must follow the live snapshot")
	assert.Equal(t, config.DirectToolResponseModeDeferred, rt.GetDirectToolResponseMode())
}
