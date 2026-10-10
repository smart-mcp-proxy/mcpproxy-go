package runtime

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UX-01 review r5: an older reconciliation parked before its storage-save phase
// must not write storage, runtime config, disk or connections for a server a
// newer commit removed, re-added or re-configured.

func parkFirstSync(t *testing.T) (reached <-chan struct{}, release func()) {
	t.Helper()
	r := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	loadConfiguredServersBeforeSyncHook = func() {
		first := false
		once.Do(func() { first = true })
		if first {
			close(r)
			<-gate
		}
	}
	t.Cleanup(func() { loadConfiguredServersBeforeSyncHook = nil })
	var relOnce sync.Once
	return r, func() { relOnce.Do(func() { close(gate) }) }
}

func r5Server(name, url string) *config.ServerConfig {
	return &config.ServerConfig{Name: name, URL: url, Protocol: "http", Enabled: false, Quarantined: true, Created: time.Now()}
}

func waitServerGone(t *testing.T, rt *Runtime, name string) {
	t.Helper()
	require.Eventually(t, func() bool {
		got, _ := rt.StorageManager().GetUpstreamServer(name)
		return got == nil && !serverNames(rt.Config())[name]
	}, 5*time.Second, 20*time.Millisecond, "newer removal never completed")
}

func TestLoadConfiguredServers_StaleSyncCannotResurrectRemovedServer(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r5Server("srv-a", "http://127.0.0.1:1/mcp")
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{a}
		return &n, nil
	}))

	reached, release := parkFirstSync(t)
	t.Cleanup(release)

	other := r5Server("srv-other", "http://127.0.0.1:3/mcp")
	_, err := rt.ApplyConfig(validConfigWith(rt, a, other), "")
	require.NoError(t, err)
	<-reached

	// Newer commit removes A (and the other); its cleanup runs to completion.
	_, err = rt.ApplyConfig(validConfigWith(rt), "")
	require.NoError(t, err)
	waitServerGone(t, rt, "srv-a")

	release()
	time.Sleep(600 * time.Millisecond)

	got, _ := rt.StorageManager().GetUpstreamServer("srv-a")
	require.Nil(t, got, "stale sync recreated the storage row")
	require.False(t, serverNames(rt.Config())["srv-a"])
	require.NoError(t, rt.SaveConfiguration())
	require.False(t, serverNames(rt.Config())["srv-a"], "a later save republished the resurrected server")
	raw, err := os.ReadFile(rt.ConfigPath())
	require.NoError(t, err)
	require.False(t, strings.Contains(string(raw), "srv-a"), "disk republished the removed server")
	require.NotContains(t, rt.upstreamManager.GetAllServerNames(), "srv-a")
}

func TestLoadConfiguredServers_StaleSyncCannotOverwriteReAddedServer(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r5Server("srv-a", "http://127.0.0.1:1/mcp")
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{a}
		return &n, nil
	}))

	reached, release := parkFirstSync(t)
	t.Cleanup(release)

	other := r5Server("srv-other", "http://127.0.0.1:3/mcp")
	_, err := rt.ApplyConfig(validConfigWith(rt, a, other), "")
	require.NoError(t, err)
	<-reached

	_, err = rt.ApplyConfig(validConfigWith(rt), "")
	require.NoError(t, err)
	waitServerGone(t, rt, "srv-a")

	// Create-only re-add with different URL, args, env and quarantine.
	fresh := &config.ServerConfig{
		Name: "srv-a", URL: "http://127.0.0.1:9/new", Protocol: "http", Enabled: false,
		Quarantined: false, Created: time.Now(),
		Args: []string{"--new"}, Env: map[string]string{"K": "new"},
	}
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		if err := rt.StorageManager().CreateUpstreamServer(fresh); err != nil {
			return nil, err
		}
		rt.ClearPendingServerRemoval(fresh.Name)
		n := *cur
		n.Servers = append(append([]*config.ServerConfig(nil), cur.Servers...), fresh)
		return &n, nil
	}))

	release()
	time.Sleep(600 * time.Millisecond)

	got, err := rt.StorageManager().GetUpstreamServer("srv-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "http://127.0.0.1:9/new", got.URL, "stale sync overwrote the re-added storage record")
	require.Equal(t, []string{"--new"}, got.Args)
	require.Equal(t, "new", got.Env["K"])

	var live *config.ServerConfig
	for _, s := range rt.Config().Servers {
		if s.Name == "srv-a" {
			live = s
		}
	}
	require.NotNil(t, live)
	require.Equal(t, "http://127.0.0.1:9/new", live.URL)
	require.NoError(t, rt.SaveConfiguration())
	raw, err := os.ReadFile(rt.ConfigPath())
	require.NoError(t, err)
	require.Contains(t, string(raw), "127.0.0.1:9/new")
	require.NotContains(t, string(raw), "127.0.0.1:1/mcp")
}

func TestLoadConfiguredServers_StaleSyncCannotUndoNewerQuarantine(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r5Server("srv-a", "http://127.0.0.1:1/mcp")
	a.Quarantined = false
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{a}
		return &n, nil
	}))

	reached, release := parkFirstSync(t)
	t.Cleanup(release)

	other := r5Server("srv-other", "http://127.0.0.1:3/mcp")
	unq := *a
	_, err := rt.ApplyConfig(validConfigWith(rt, &unq, other), "")
	require.NoError(t, err)
	<-reached

	q := *a
	q.Quarantined = true
	_, err = rt.ApplyConfig(validConfigWith(rt, &q), "")
	require.NoError(t, err)

	release()
	time.Sleep(600 * time.Millisecond)

	got, err := rt.StorageManager().GetUpstreamServer("srv-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.True(t, got.Quarantined, "stale sync lifted a newer quarantine in storage")
	for _, s := range rt.Config().Servers {
		if s.Name == "srv-a" {
			require.True(t, s.Quarantined, "runtime config lost the newer quarantine")
		}
	}
}
