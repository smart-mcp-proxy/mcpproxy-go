package management

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestListServers_IsolationTriState round-trips the runtime projection through
// the map→contracts converter and asserts that the raw per-server override
// survives as a tri-state (GH #1142). Before the fix the converter read only a
// flattened `enabled` bool, so "inherits global" and "explicitly off" were
// indistinguishable at the REST boundary.
func TestListServers_IsolationTriState(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cfg := &config.Config{DockerIsolation: &config.DockerIsolationConfig{Enabled: true}}
	emitter := &mockEventEmitter{}

	rt := newMockRuntime()
	rt.servers = []map[string]interface{}{
		{
			"id": "inheriting", "name": "inheriting", "protocol": "stdio",
			"command": "uvx", "enabled": true,
			"isolation": map[string]interface{}{
				"enabled": true,
				"image":   "python:3.12",
			},
			"isolation_effective": map[string]interface{}{
				"mode": "docker", "isolated": true, "inherited": true,
				"global_mode": "docker", "source": config.IsolationSourceGlobal,
			},
		},
		{
			"id": "opted-out", "name": "opted-out", "protocol": "stdio",
			"command": "npx", "enabled": true,
			"isolation": map[string]interface{}{
				"enabled":          false,
				"enabled_override": false,
			},
			"isolation_effective": map[string]interface{}{
				"mode": "none", "isolated": false, "inherited": false,
				"global_mode": "docker", "source": config.IsolationSourceServerOptOut,
			},
		},
		{
			"id": "sandboxed", "name": "sandboxed", "protocol": "stdio",
			"command": "npx", "enabled": true,
			"isolation": map[string]interface{}{
				"enabled":       true,
				"mode_override": "sandbox",
			},
			"isolation_effective": map[string]interface{}{
				"mode": "sandbox", "isolated": true, "inherited": false,
				"global_mode": "docker", "source": config.IsolationSourceServerMode,
			},
		},
	}

	svc := NewService(rt, cfg, "", emitter, nil, logger)
	servers, _, err := svc.ListServers(context.Background())
	require.NoError(t, err)
	require.Len(t, servers, 3)

	byName := map[string]int{}
	for i, s := range servers {
		byName[s.Name] = i
	}

	inheriting := servers[byName["inheriting"]]
	require.NotNil(t, inheriting.Isolation)
	assert.True(t, inheriting.Isolation.Enabled, "effective state: isolated")
	assert.Nil(t, inheriting.Isolation.EnabledOverride,
		"no per-server override was set — the tri-state must stay nil, not collapse to false")
	require.NotNil(t, inheriting.IsolationEffective)
	assert.True(t, inheriting.IsolationEffective.Inherited)
	assert.Equal(t, "docker", inheriting.IsolationEffective.Mode)
	assert.Equal(t, config.IsolationSourceGlobal, inheriting.IsolationEffective.Source)

	optedOut := servers[byName["opted-out"]]
	require.NotNil(t, optedOut.Isolation)
	assert.False(t, optedOut.Isolation.Enabled)
	require.NotNil(t, optedOut.Isolation.EnabledOverride,
		"an explicit opt-out must be readable as a set override")
	assert.False(t, *optedOut.Isolation.EnabledOverride)
	require.NotNil(t, optedOut.IsolationEffective)
	assert.False(t, optedOut.IsolationEffective.Inherited)
	assert.Equal(t, config.IsolationSourceServerOptOut, optedOut.IsolationEffective.Source)

	sandboxed := servers[byName["sandboxed"]]
	require.NotNil(t, sandboxed.Isolation)
	assert.Equal(t, "sandbox", sandboxed.Isolation.ModeOverride)
	assert.Nil(t, sandboxed.Isolation.EnabledOverride)
	require.NotNil(t, sandboxed.IsolationEffective)
	assert.Equal(t, "sandbox", sandboxed.IsolationEffective.Mode)
}
