package supervisor

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

const terminalServerName = "broken-toolchain"

// terminalHarness boots a supervisor over one server, gets it connected once,
// then parks it exactly as a confirmed-permanent connect failure would: two
// attempts, terminal flag set, last attempt 19 hours ago — the shape of the
// server reported in GH #1145.
func terminalHarness(t *testing.T) (*Supervisor, *MockUpstreamAdapter, *config.Config, *configsvc.Service) {
	t.Helper()

	srv := &config.ServerConfig{
		Name:      terminalServerName,
		Command:   "uvx",
		Args:      []string{"broken-package"},
		Protocol:  "stdio",
		Enabled:   true,
		Isolation: &config.IsolationConfig{Image: "python:3.11"},
		Created:   time.Now(),
	}
	cfg := &config.Config{Listen: "127.0.0.1:8080", Servers: []*config.ServerConfig{srv}}

	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	t.Cleanup(configSvc.Close)

	mockUpstream := NewMockUpstreamAdapter()
	t.Cleanup(mockUpstream.Close)

	sup := New(configSvc, mockUpstream, zap.NewNop())
	require.NoError(t, sup.reconcile(configSvc.Current()))
	sup.actionWg.Wait()

	mockUpstream.mu.Lock()
	mockUpstream.connected[terminalServerName] = false
	if st, ok := mockUpstream.states[terminalServerName]; ok {
		st.Connected = false
		st.ConnectionInfo = &types.ConnectionInfo{
			State:         types.StateError,
			LastError:     errors.New(`exec: "uvx": executable file not found in $PATH`),
			RetryCount:    types.PermanentFailureAttempts,
			LastRetryTime: time.Now().Add(-19 * time.Hour),
			Terminal:      true,
			TerminalCode:  "MCPX_DOCKER_EXEC_NOT_FOUND",
		}
	}
	mockUpstream.mu.Unlock()

	return sup, mockUpstream, cfg, configSvc
}

func isConnected(m *MockUpstreamAdapter) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected[terminalServerName]
}

// snapshotWith returns a config snapshot carrying a mutated copy of the single
// server — the shape the config service publishes after a user edit.
func snapshotWith(cfg *config.Config, mutate func(*config.ServerConfig)) *configsvc.Snapshot {
	edited := *cfg.Servers[0]
	mutate(&edited)
	return &configsvc.Snapshot{
		Config:    &config.Config{Listen: cfg.Listen, Servers: []*config.ServerConfig{&edited}},
		Path:      "/tmp/config.json",
		Version:   2,
		Timestamp: time.Now(),
	}
}

// TestReconcile_TerminalServerPlansNoConnect is the headline regression: once a
// failure is proven permanent, the 30-second reconcile loop must stop dialing.
//
// On the unfixed tree the half-hourly gave-up probe fires (19h ≫ 30m) and the
// server is re-dialed — 35 of the 55 attempts in the report.
func TestReconcile_TerminalServerPlansNoConnect(t *testing.T) {
	sup, mockUpstream, _, configSvc := terminalHarness(t)

	plan := sup.computeReconcilePlan(configSvc.Current(), mockUpstream.GetAllStates(), map[string]bool{})
	require.Equal(t, ActionNone, plan.Actions[terminalServerName],
		"a confirmed-permanent failure must not be re-dialed by the reconcile loop")

	require.NoError(t, sup.reconcile(configSvc.Current()))
	sup.actionWg.Wait()
	require.False(t, isConnected(mockUpstream), "supervisor re-dialed a permanently failed server")
}

// TestReconcile_TerminalServerRecoversOnConfigChange is the required recovery
// test: parking is only acceptable because fixing the config un-parks. Both
// sub-cases edit a field the OLD five-field configChanged could not see, which
// is exactly why widening it was part of this fix.
func TestReconcile_TerminalServerRecoversOnConfigChange(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.ServerConfig)
	}{
		{"args fixed", func(c *config.ServerConfig) { c.Args = []string{"working-package"} }},
		{"isolation image fixed", func(c *config.ServerConfig) { c.Isolation = &config.IsolationConfig{Image: "ghcr.io/astral-sh/uv:latest"} }},
		{"env fixed", func(c *config.ServerConfig) { c.Env = map[string]string{"PATH": "/usr/local/bin"} }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sup, mockUpstream, cfg, _ := terminalHarness(t)

			edited := snapshotWith(cfg, tc.mutate)
			plan := sup.computeReconcilePlan(edited, mockUpstream.GetAllStates(), map[string]bool{})
			require.Equal(t, ActionReconnect, plan.Actions[terminalServerName],
				"a user who fixes the config must get a reconnect, not silence")

			require.NoError(t, sup.reconcile(edited))
			sup.actionWg.Wait()
			require.True(t, isConnected(mockUpstream), "the fixed server never came back")
		})
	}
}

// TestReconcile_TerminalServerRecoversOnEnableToggle guards the disable/enable
// button in the UI, the other explicit "the user did something" signal.
func TestReconcile_TerminalServerRecoversOnEnableToggle(t *testing.T) {
	sup, mockUpstream, cfg, _ := terminalHarness(t)

	disabled := snapshotWith(cfg, func(c *config.ServerConfig) { c.Enabled = false })
	require.NoError(t, sup.reconcile(disabled))
	sup.actionWg.Wait()

	enabled := snapshotWith(cfg, func(c *config.ServerConfig) { c.Enabled = true })
	plan := sup.computeReconcilePlan(enabled, mockUpstream.GetAllStates(), map[string]bool{})
	require.Equal(t, ActionReconnect, plan.Actions[terminalServerName],
		"re-enabling a parked server must reconnect it")
}

// TestReconcile_TerminalStateIsVisible pins the no-silent-swallow requirement:
// the parked state and the reason reach the read model the API and CLI serve.
func TestReconcile_TerminalStateIsVisible(t *testing.T) {
	sup, _, _, configSvc := terminalHarness(t)

	require.NoError(t, sup.reconcile(configSvc.Current()))
	sup.actionWg.Wait()

	status, ok := sup.StateView().GetServer(terminalServerName)
	require.True(t, ok)
	require.True(t, status.RetryStopped, "a parked server must be visible as such")
	require.Equal(t, "MCPX_DOCKER_EXEC_NOT_FOUND", status.RetryStoppedCode)
	require.NotEmpty(t, status.RetryStoppedReason, "the reason must be surfaced, not swallowed")
}
