package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Item 4a: on an upgrade start the pre-fix advisory must be emitted once per
// process, not once per gate pass. Startup runs the gate in gateInitialConfig
// (before the supervisor starts) and again in LoadConfiguredServers
// (backgroundInitialization), and every later configsvc publish runs it again
// through the pre-publish hook. None of those passes changes the affected set.
func TestConfigLoadAdmissionGate_PreFixAdvisoryOncePerProcess(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	rt, _, _ := gateEnvAt(t, []map[string]any{
		{"name": "pre-fix", "command": "./poison", "protocol": "stdio", "enabled": true},
		{"name": "reviewed", "command": "./ok", "protocol": "stdio", "enabled": true, "quarantined": false},
	}, nil, zap.New(core))

	// What an older release left behind: both servers known to config.db, live.
	for _, name := range []string{"pre-fix", "reviewed"} {
		require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{
			Name: name, Command: "./x", Protocol: "stdio", Enabled: true,
		}))
		seedApproval(t, rt, name, "read_graph", storage.ToolApprovalStatusApproved, "h1")
	}

	// Production startup order (lifecycle.go): hook + initial gate, then
	// backgroundInitialization -> LoadConfiguredServers(nil).
	rt.installAdmissionGateHook()
	rt.gateInitialConfig()
	require.NoError(t, rt.LoadConfiguredServers(nil))

	// An unrelated publish (e.g. a hot reload of an unchanged file).
	cur := rt.configSvc.Current().Config
	require.NoError(t, rt.configSvc.Update(cur, configsvc.UpdateTypeReload, "test_reload"))

	require.Len(t, logs.FilterMessageSnippet("predate").All(), 1,
		"the same pre-fix set must be reported once per process, not once per gate pass")
}
