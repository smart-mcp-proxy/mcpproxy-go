package runtime

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UX-01 r9: an applied config is published to storage in the same commit as the
// live config, so an unrelated commit that lands before the queued
// reconciliation (and rebuilds the server list from storage) cannot revert the
// applied quarantine/disable or drop a newly configured server.
func TestApplyConfig_StateSurvivesUnrelatedCommitBeforeReconcile(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r6Server("srv-a", "http://127.0.0.1:1/mcp", true)
	b := r6Server("srv-b", "http://127.0.0.1:2/mcp", true)
	for _, sc := range []*config.ServerConfig{a, b} {
		require.NoError(t, rt.StorageManager().CreateUpstreamServer(sc))
	}
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{a, b}
		return &n, nil
	}))

	reached, release := parkFirstSync(t)
	t.Cleanup(release)

	a2 := r6Server("srv-a", "http://127.0.0.1:1/mcp", false)
	a2.Quarantined = true
	a2.MarkQuarantineExplicitlySet(true)
	c := r6Server("srv-c", "http://127.0.0.1:3/mcp", false)
	c.Quarantined = true
	_, err := rt.ApplyConfig(validConfigWith(rt, a2, b, c), "")
	require.NoError(t, err)
	<-reached // the reconciliation is queued, not yet run

	// An unrelated toggle commits from storage while the reconciliation waits.
	require.NoError(t, rt.EnableServer("srv-b", false))

	check := func(where string, get func(string) *config.ServerConfig) {
		t.Helper()
		ga := get("srv-a")
		require.NotNil(t, ga, where+": srv-a missing")
		require.False(t, ga.Enabled, where+": disable of srv-a reverted")
		require.True(t, ga.Quarantined, where+": quarantine of srv-a reverted")
		require.NotNil(t, get("srv-c"), where+": newly configured srv-c dropped")
		gb := get("srv-b")
		require.NotNil(t, gb, where)
		require.False(t, gb.Enabled, where+": unrelated toggle lost")
	}
	fromStorage := func(n string) *config.ServerConfig { s, _ := rt.StorageManager().GetUpstreamServer(n); return s }
	fromLive := func(n string) *config.ServerConfig { return liveServerNamed(rt, n) }
	fromDisk := func(n string) *config.ServerConfig {
		raw, err := os.ReadFile(rt.ConfigPath())
		require.NoError(t, err)
		var c config.Config
		require.NoError(t, json.Unmarshal(raw, &c))
		for _, s := range c.Servers {
			if s.Name == n {
				return s
			}
		}
		return nil
	}
	check("storage", fromStorage)
	check("live config", fromLive)
	check("disk", fromDisk)

	release()
	time.Sleep(600 * time.Millisecond) // reconciliation runs against the surviving state
	check("storage after reconcile", fromStorage)
	check("live config after reconcile", fromLive)
	check("disk after reconcile", fromDisk)
	for _, n := range rt.upstreamManager.GetAllServerNames() {
		if n == "srv-a" {
			if cl, ok := rt.upstreamManager.GetClient(n); ok {
				require.False(t, cl.GetConfig().Enabled, "manager still holds srv-a enabled")
			}
		}
	}
}
