package supervisor_test

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
)

// UX-01 r7: a delayed supervisor action carries the config snapshot it was
// planned from. After a newer commit removed, re-pointed or quarantined the
// server, the old action must change nothing.

func staleActionRuntime(t *testing.T) *runtime.Runtime {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"mcpServers":[]}`), 0o600))
	rt, err := runtime.New(&config.Config{DataDir: dir, Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{}}, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func markerServer(marker, tag string, quarantined bool) *config.ServerConfig {
	return &config.ServerConfig{
		Name: "stale-x", Protocol: "stdio", Command: "/bin/sh", Enabled: true, Quarantined: quarantined,
		Args: []string{"-c", "touch " + marker + "; exec cat", tag}, Created: time.Now(),
	}
}

func commitServers(t *testing.T, rt *runtime.Runtime, servers ...*config.ServerConfig) {
	t.Helper()
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = servers
		return &n, nil
	}))
}

func TestStaleSupervisorAction_AfterRemoval_DoesNotRecreateClient(t *testing.T) {
	rt := staleActionRuntime(t)
	marker := filepath.Join(t.TempDir(), "m")
	old := markerServer(marker, "old", false)
	commitServers(t, rt, old)
	snap := rt.ConfigSnapshot()

	commitServers(t, rt) // newer commit removes the server
	rt.UpstreamManager().RemoveServer("stale-x")

	for _, act := range []supervisor.ReconcileAction{supervisor.ActionConnect, supervisor.ActionReconnect} {
		require.NoError(t, rt.Supervisor().ExecuteActionForTest("stale-x", act, snap))
	}
	_, exists := rt.UpstreamManager().GetClient("stale-x")
	require.False(t, exists, "stale action recreated the removed client")
	time.Sleep(200 * time.Millisecond)
	require.NoFileExists(t, marker)
}

func TestStaleSupervisorAction_AfterEndpointReplace_KeepsLatestConfig(t *testing.T) {
	rt := staleActionRuntime(t)
	dir := t.TempDir()
	oldMarker, newMarker := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	commitServers(t, rt, markerServer(oldMarker, "old", false))
	snap := rt.ConfigSnapshot()

	latest := markerServer(newMarker, "new", false)
	commitServers(t, rt, latest)
	require.NoError(t, rt.UpstreamManager().AddServerConfig("stale-x", latest)) // newer action settled

	for _, act := range []supervisor.ReconcileAction{supervisor.ActionConnect, supervisor.ActionReconnect, supervisor.ActionDisconnect} {
		require.NoError(t, rt.Supervisor().ExecuteActionForTest("stale-x", act, snap))
	}
	c, ok := rt.UpstreamManager().GetClient("stale-x")
	require.True(t, ok)
	require.Equal(t, latest.Args, c.GetConfig().Args, "stale action replaced the newer client config")
	require.False(t, c.IsRetired())
	time.Sleep(200 * time.Millisecond)
	require.NoFileExists(t, oldMarker, "stale action launched the old endpoint")
}

func TestStaleSupervisorAction_AfterQuarantine_KeepsQuarantine(t *testing.T) {
	rt := staleActionRuntime(t)
	marker := filepath.Join(t.TempDir(), "m")
	commitServers(t, rt, markerServer(marker, "same", false))
	snap := rt.ConfigSnapshot()

	q := markerServer(marker, "same", true)
	commitServers(t, rt, q)
	require.NoError(t, rt.UpstreamManager().AddServerConfig("stale-x", q))

	for _, act := range []supervisor.ReconcileAction{supervisor.ActionConnect, supervisor.ActionReconnect} {
		require.NoError(t, rt.Supervisor().ExecuteActionForTest("stale-x", act, snap))
	}
	c, ok := rt.UpstreamManager().GetClient("stale-x")
	require.True(t, ok)
	require.True(t, c.GetConfig().Quarantined, "stale action replaced the quarantined client with the old unquarantined config")
	time.Sleep(200 * time.Millisecond)
	require.NoFileExists(t, marker, "stale action connected a quarantined server")
}

// UX-01 r8 (finding 2): a connect that captured its client under the guard and
// is parked before the dial must not launch the old endpoint once a newer
// commit has been published, even though the manager reconciliation for that
// commit has not run.
func TestSupervisorConnect_CapturedClient_NewerCommitPublished_NeverLaunches(t *testing.T) {
	rt := staleActionRuntime(t)
	marker := filepath.Join(t.TempDir(), "m")
	old := markerServer(marker, "old", false)
	commitServers(t, rt, old)
	snap := rt.ConfigSnapshot()

	var launches atomic.Int32
	core.AfterLaunchHook = func(string) { launches.Add(1) }
	t.Cleanup(func() { core.AfterLaunchHook = nil })

	reached := make(chan struct{})
	resume := make(chan struct{})
	supervisor.SetConnectAfterCaptureHookForTest(func(string) {
		close(reached)
		<-resume
	})
	t.Cleanup(func() { supervisor.SetConnectAfterCaptureHookForTest(nil) })

	done := make(chan error, 1)
	go func() { done <- rt.Supervisor().ExecuteActionForTest("stale-x", supervisor.ActionConnect, snap) }()
	<-reached

	commitServers(t, rt) // publishes the removal; manager reconciliation not run
	_, held := rt.UpstreamManager().GetClient("stale-x")
	require.True(t, held, "test premise: the manager still holds the captured client")

	close(resume)
	require.NoError(t, <-done)
	time.Sleep(700 * time.Millisecond)
	require.Zero(t, launches.Load(), "the removed server's command was started")
	require.NoFileExists(t, marker)
}
