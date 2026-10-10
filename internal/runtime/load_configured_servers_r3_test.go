package runtime

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// UX-01 review r3 (finding 1): the stale reconcile must not prune the tool
// approvals, blocks or call history of a server the live config holds.
func TestLoadConfiguredServers_StaleSnapshotKeepsNewServerApprovalsAndHistory(t *testing.T) {
	rt := newPurgeTestRuntime(t)

	added := &config.ServerConfig{Name: "late-add", URL: "http://127.0.0.1:1/mcp", Protocol: "http", Enabled: false, Quarantined: true, Created: time.Now()}
	// Non-empty stale snapshot (pre-add), as an older apply would have captured.
	other := &config.ServerConfig{Name: "early", URL: "http://127.0.0.1:2/mcp", Protocol: "http", Enabled: false, Quarantined: true, Created: time.Now()}
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(other))
	stale := *rt.Config()
	stale.Servers = []*config.ServerConfig{other}

	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		if err := rt.StorageManager().CreateUpstreamServer(added); err != nil {
			return nil, err
		}
		n := *cur
		n.Servers = append(append([]*config.ServerConfig(nil), cur.Servers...), other, added)
		return &n, nil
	}))

	require.NoError(t, rt.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "late-add", ToolName: "blocked_tool", ApprovedHash: "h", CurrentHash: "h",
		Status: "approved", Disabled: true,
	}))
	require.NoError(t, rt.StorageManager().RecordToolCall(&storage.ToolCallRecord{
		ID: "c1", ServerID: "late-add", ServerName: "late-add", ToolName: "blocked_tool", Timestamp: time.Now(),
	}))

	require.NoError(t, rt.LoadConfiguredServers(&stale))
	time.Sleep(300 * time.Millisecond)

	rec, err := rt.StorageManager().GetToolApproval("late-add", "blocked_tool")
	require.NoError(t, err)
	require.NotNil(t, rec, "approval record must survive a stale reconcile")
	require.True(t, rec.Disabled, "blocked tool must stay blocked")
	calls, err := rt.StorageManager().GetServerToolCalls("late-add", 10)
	require.NoError(t, err)
	require.Len(t, calls, 1, "call history must survive a stale reconcile")
}

func serverNames(cfg *config.Config) map[string]bool {
	m := map[string]bool{}
	for _, s := range cfg.Servers {
		m[s.Name] = true
	}
	return m
}

// UX-01 review r3 (finding 2): a removal committed by an applied config must
// still be carried out when a later save resurrected the server into the live
// config before the async cleanup ran.
func TestServerRemovalCleanup_NotCancelledBySaveResurrection(t *testing.T) {
	rt := newPurgeTestRuntime(t)

	mk := func(n string) *config.ServerConfig {
		return &config.ServerConfig{Name: n, URL: "http://127.0.0.1:1/mcp", Protocol: "http", Enabled: false, Quarantined: true, Created: time.Now()}
	}
	a, b := mk("srv-a"), mk("srv-b")
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{a}
		return &n, nil
	}))

	gate := make(chan struct{})
	var once sync.Once
	reached := make(chan struct{})
	serverRemovalCleanupHook = func(name string) {
		if name == "srv-a" {
			once.Do(func() { close(reached) })
			<-gate
		}
	}
	t.Cleanup(func() { serverRemovalCleanupHook = nil })

	// Apply a config WITHOUT A.
	_, err := rt.ApplyConfig(validConfigWith(rt), "")
	require.NoError(t, err)
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("removal cleanup never started")
	}

	// B is added and its save republishes A from the still-present storage row.
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		if err := rt.StorageManager().CreateUpstreamServer(b); err != nil {
			return nil, err
		}
		rt.ClearPendingServerRemoval(b.Name)
		n := *cur
		n.Servers = append(append([]*config.ServerConfig(nil), cur.Servers...), b)
		return &n, nil
	}))
	require.NoError(t, rt.SaveConfiguration())
	require.False(t, serverNames(rt.Config())["srv-a"], "save republished a server pending removal (UX-01 r10)")

	close(gate)
	require.Eventually(t, func() bool {
		got, _ := rt.StorageManager().GetUpstreamServer("srv-a")
		return got == nil
	}, 5*time.Second, 20*time.Millisecond, "A must be removed from storage")

	// The cleanup itself must leave runtime config and disk consistent (the
	// config watcher would otherwise read the stale file back as an edit).
	require.Eventually(t, func() bool { return !serverNames(rt.Config())["srv-a"] }, 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		raw, rerr := os.ReadFile(rt.ConfigPath())
		return rerr == nil && !strings.Contains(string(raw), "srv-a")
	}, 5*time.Second, 20*time.Millisecond, "A must be absent from the config file")
	require.NoError(t, rt.SaveConfiguration())
	names := serverNames(rt.Config())
	require.False(t, names["srv-a"], "A must be absent from the runtime config")
	require.True(t, names["srv-b"], "B must survive")
	gotB, err := rt.StorageManager().GetUpstreamServer("srv-b")
	require.NoError(t, err)
	require.NotNil(t, gotB)
}

// A deliberate re-add of the removed name (apply including it) before the
// cleanup runs must keep the server.
func TestServerRemovalCleanup_DeliberateReAddIsPreserved(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := &config.ServerConfig{Name: "srv-a", URL: "http://127.0.0.1:1/mcp", Protocol: "http", Enabled: false, Quarantined: true, Created: time.Now()}
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{a}
		return &n, nil
	}))

	gate := make(chan struct{})
	reached := make(chan struct{}, 4)
	serverRemovalCleanupHook = func(name string) {
		if name == "srv-a" {
			reached <- struct{}{}
			<-gate
		}
	}
	t.Cleanup(func() { serverRemovalCleanupHook = nil })

	_, err := rt.ApplyConfig(validConfigWith(rt), "")
	require.NoError(t, err)
	<-reached

	_, err = rt.ApplyConfig(validConfigWith(rt, a), "")
	require.NoError(t, err)

	close(gate)
	time.Sleep(500 * time.Millisecond)

	got, err := rt.StorageManager().GetUpstreamServer("srv-a")
	require.NoError(t, err)
	require.NotNil(t, got, "re-added server must not be reaped by the earlier removal")
	require.True(t, serverNames(rt.Config())["srv-a"])
}

// validConfigWith builds a valid full config for ApplyConfig carrying exactly
// the given servers.
func validConfigWith(rt *Runtime, servers ...*config.ServerConfig) *config.Config {
	live := rt.Config()
	c := config.DefaultConfig()
	c.Listen = live.Listen
	c.DataDir = live.DataDir
	c.Servers = servers
	return c
}

// UX-01 review r4 (finding 1): a create-only add that lands AFTER the orphan-GC
// keep-set was captured must not have its approvals/history pruned. The
// reconcile pauses right after the capture; the add (with a blocked tool and a
// recorded call) runs concurrently; the prunes then resume.
func TestLoadConfiguredServers_AddAfterKeepSetCaptureKeepsRecords(t *testing.T) {
	rt := newPurgeTestRuntime(t)

	other := &config.ServerConfig{Name: "early", URL: "http://127.0.0.1:2/mcp", Protocol: "http", Enabled: false, Quarantined: true, Created: time.Now()}
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(other))
	stale := *rt.Config()
	stale.Servers = []*config.ServerConfig{other}

	captured := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	loadConfiguredServersKeepSetHook = func() {
		once.Do(func() {
			close(captured)
			<-gate
		})
	}
	t.Cleanup(func() { loadConfiguredServersKeepSetHook = nil })

	loadDone := make(chan error, 1)
	go func() { loadDone <- rt.LoadConfiguredServers(&stale) }()
	select {
	case <-captured:
	case <-time.After(5 * time.Second):
		t.Fatal("keep-set capture never reached")
	}

	added := &config.ServerConfig{Name: "late-add", URL: "http://127.0.0.1:1/mcp", Protocol: "http", Enabled: false, Quarantined: true, Created: time.Now()}
	addDone := make(chan struct{})
	go func() {
		defer close(addDone)
		_ = rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
			if err := rt.StorageManager().CreateUpstreamServer(added); err != nil {
				return nil, err
			}
			n := *cur
			n.Servers = append(append([]*config.ServerConfig(nil), cur.Servers...), other, added)
			return &n, nil
		})
		_ = rt.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "late-add", ToolName: "blocked_tool", ApprovedHash: "h", CurrentHash: "h",
			Status: "approved", Disabled: true,
		})
		_ = rt.StorageManager().RecordToolCall(&storage.ToolCallRecord{
			ID: "c1", ServerID: "late-add", ServerName: "late-add", ToolName: "blocked_tool", Timestamp: time.Now(),
		})
	}()
	// Give the add the chance to run inside the paused window.
	select {
	case <-addDone:
	case <-time.After(300 * time.Millisecond):
	}
	close(gate)
	require.NoError(t, <-loadDone)
	<-addDone
	time.Sleep(300 * time.Millisecond)

	rec, err := rt.StorageManager().GetToolApproval("late-add", "blocked_tool")
	require.NoError(t, err)
	require.NotNil(t, rec, "approval record must survive the paused reconcile")
	require.True(t, rec.Disabled, "blocked tool must stay blocked")
	calls, err := rt.StorageManager().GetServerToolCalls("late-add", 10)
	require.NoError(t, err)
	require.Len(t, calls, 1, "call history must survive the paused reconcile")
}

// UX-01 review r4 (finding 2): an unrelated applied config that merely carries
// the resurrected name forward must not cancel the pending removal.
func TestServerRemovalCleanup_NotCancelledByUnrelatedApplyCarryingResurrectedName(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	mk := func(n string) *config.ServerConfig {
		return &config.ServerConfig{Name: n, URL: "http://127.0.0.1:1/mcp", Protocol: "http", Enabled: false, Quarantined: true, Created: time.Now()}
	}
	a, b := mk("srv-a"), mk("srv-b")
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{a}
		return &n, nil
	}))

	gate := make(chan struct{})
	var once sync.Once
	reached := make(chan struct{})
	serverRemovalCleanupHook = func(name string) {
		if name == "srv-a" {
			once.Do(func() { close(reached) })
			<-gate
		}
	}
	t.Cleanup(func() { serverRemovalCleanupHook = nil })

	_, err := rt.ApplyConfig(validConfigWith(rt), "")
	require.NoError(t, err)
	<-reached

	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		if err := rt.StorageManager().CreateUpstreamServer(b); err != nil {
			return nil, err
		}
		rt.ClearPendingServerRemoval(b.Name)
		n := *cur
		n.Servers = append(append([]*config.ServerConfig(nil), cur.Servers...), b)
		return &n, nil
	}))
	require.NoError(t, rt.SaveConfiguration())
	// A save no longer republishes the server pending removal (UX-01 r10), so
	// there is no resurrected entry for an unrelated write to carry forward.
	require.False(t, serverNames(rt.Config())["srv-a"], "save republished a server pending removal (UX-01 r10)")

	// Unrelated config write that carries B.
	_, err = rt.ApplyConfig(validConfigWith(rt, b), "")
	require.NoError(t, err)

	close(gate)
	require.Eventually(t, func() bool {
		got, _ := rt.StorageManager().GetUpstreamServer("srv-a")
		return got == nil
	}, 5*time.Second, 20*time.Millisecond, "A must still be reaped")
	require.Eventually(t, func() bool { return !serverNames(rt.Config())["srv-a"] }, 5*time.Second, 20*time.Millisecond)
	gotB, err := rt.StorageManager().GetUpstreamServer("srv-b")
	require.NoError(t, err)
	require.NotNil(t, gotB, "B must survive")
	require.True(t, serverNames(rt.Config())["srv-b"])
}
