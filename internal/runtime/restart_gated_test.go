package runtime

import (
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pinRestartGated must revert EXACTLY the fields DetectConfigChanges refuses to
// hot-apply. A field gated by the detector but missed by the pinner is adopted
// in memory while the API keeps reporting it as pending — the drift the
// served/desired split exists to prevent; a field pinned but not gated is
// silently frozen and can never be changed without a restart.
//
// Driven off the detector itself rather than a second hand-written list.
func TestPinRestartGatedCoversEveryRestartGatedField(t *testing.T) {
	base := func() *config.Config {
		c := config.DefaultConfig()
		c.Listen = "127.0.0.1:8080"
		c.RoutingMode = config.RoutingModeRetrieveTools
		c.DataDir = "/tmp/one"
		c.APIKey = "key-one"
		return c
	}

	// Each mutation is a field the detector reports as restart-required.
	mutations := map[string]func(*config.Config){
		"listen":             func(c *config.Config) { c.Listen = "127.0.0.1:9999" },
		"routing_mode":       func(c *config.Config) { c.RoutingMode = config.RoutingModeDirect },
		"data_dir":           func(c *config.Config) { c.DataDir = "/tmp/two" },
		"api_key":            func(c *config.Config) { c.APIKey = "key-two" },
		"tls":                func(c *config.Config) { c.TLS = &config.TLSConfig{Enabled: true} },
		"http_read_timeout":  func(c *config.Config) { d := config.Duration(5 * time.Second); c.HTTPReadTimeout = &d },
		"http_write_timeout": func(c *config.Config) { d := config.Duration(5 * time.Second); c.HTTPWriteTimeout = &d },
		"http_idle_timeout":  func(c *config.Config) { d := config.Duration(5 * time.Second); c.HTTPIdleTimeout = &d },
		// The one restart-gated clause that does not early-return in the
		// detector, and so the one most easily missed by the pinner.
		"code_execution_pool_size": func(c *config.Config) { c.CodeExecutionPoolSize = 99 },
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			live := base()
			desired := base()
			mutate(desired)

			// Precondition: this really is a restart-gated field.
			require.True(t, DetectConfigChanges(live, desired).RequiresRestart,
				"test premise: %s must be restart-gated", name)

			// Pinning it back must leave nothing for the detector to defer.
			pinned := pinRestartGated(live, desired)
			result := DetectConfigChanges(live, pinned)
			assert.False(t, result.RequiresRestart,
				"pinRestartGated missed %s: it would be adopted in memory while the API reports it pending", name)
		})
	}
}

// …and a hot field must survive pinning, or a mixed write would lose its hot
// half all over again.
func TestPinRestartGatedKeepsHotFields(t *testing.T) {
	live := config.DefaultConfig()
	live.RoutingMode = config.RoutingModeRetrieveTools
	live.ToolsLimit = 15

	desired := config.DefaultConfig()
	desired.RoutingMode = config.RoutingModeDirect
	desired.ToolsLimit = 42
	desired.DirectToolResponseMode = config.DirectToolResponseModeDeferred

	pinned := pinRestartGated(live, desired)
	assert.Equal(t, config.RoutingModeRetrieveTools, pinned.RoutingMode)
	assert.Equal(t, 42, pinned.ToolsLimit)
	assert.Equal(t, config.DirectToolResponseModeDeferred, pinned.DirectToolResponseMode)
	assert.Equal(t, config.RoutingModeDirect, desired.RoutingMode, "the input must not be mutated")
}
