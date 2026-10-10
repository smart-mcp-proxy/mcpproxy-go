package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UX-01 review r6: connection goroutines and storage-first state toggles must
// be ordered against newer commits, and a stale reconcile must still schedule
// a committed removal.

// parkConnectionGoroutine parks the first connect (enabled=true) or disconnect
// (enabled=false) goroutine for name until release is called.
func parkConnectionGoroutine(t *testing.T, name string, enabled bool) (reached <-chan struct{}, release func()) {
	t.Helper()
	r := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	syncConnectionGoroutineHook = func(n string, en bool) {
		if n != name || en != enabled {
			return
		}
		first := false
		once.Do(func() { first = true })
		if first {
			close(r)
			<-gate
		}
	}
	t.Cleanup(func() { syncConnectionGoroutineHook = nil })
	var relOnce sync.Once
	return r, func() { relOnce.Do(func() { close(gate) }) }
}

// parkSyncAfterSnapshot parks the first sync while it HOLDS the commit lock,
// after it captured its storage/config snapshot (the orphan-GC keep-set seam).
func parkSyncAfterSnapshot(t *testing.T) (reached <-chan struct{}, release func()) {
	t.Helper()
	r := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	loadConfiguredServersKeepSetHook = func() {
		first := false
		once.Do(func() { first = true })
		if first {
			close(r)
			<-gate
		}
	}
	t.Cleanup(func() { loadConfiguredServersKeepSetHook = nil })
	var relOnce sync.Once
	return r, func() { relOnce.Do(func() { close(gate) }) }
}

func r6Server(name, url string, enabled bool) *config.ServerConfig {
	return &config.ServerConfig{Name: name, URL: url, Protocol: "http", Enabled: enabled, Quarantined: false, Created: time.Now()}
}

func clientURL(rt *Runtime, name string) (string, bool) {
	c, ok := rt.upstreamManager.GetClient(name)
	if !ok {
		return "", false
	}
	return c.GetConfig().URL, true
}

func TestSyncConnectGoroutine_StaleAddCannotReviveRemovedOrReplacedServer(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	reached, release := parkConnectionGoroutine(t, "srv-a", true)
	t.Cleanup(release)

	a := r6Server("srv-a", "http://127.0.0.1:1/old", true)
	_, err := rt.ApplyConfig(validConfigWith(rt, a), "")
	require.NoError(t, err)
	<-reached

	// Newer commit removes A entirely.
	_, err = rt.ApplyConfig(validConfigWith(rt), "")
	require.NoError(t, err)
	waitServerGone(t, rt, "srv-a")

	release()
	time.Sleep(500 * time.Millisecond)
	_, present := clientURL(rt, "srv-a")
	require.False(t, present, "late connect goroutine recreated the removed server's client")
	require.NotContains(t, rt.upstreamManager.GetAllServerNames(), "srv-a")
}

func TestSyncConnectGoroutine_StaleAddCannotReplaceReAddedEndpoint(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	reached, release := parkConnectionGoroutine(t, "srv-a", true)
	t.Cleanup(release)

	a := r6Server("srv-a", "http://127.0.0.1:1/old", true)
	_, err := rt.ApplyConfig(validConfigWith(rt, a), "")
	require.NoError(t, err)
	<-reached

	_, err = rt.ApplyConfig(validConfigWith(rt), "")
	require.NoError(t, err)
	waitServerGone(t, rt, "srv-a")

	// Re-add with a different endpoint, create-only, and register it.
	fresh := r6Server("srv-a", "http://127.0.0.1:9/new", true)
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		if err := rt.StorageManager().CreateUpstreamServer(fresh); err != nil {
			return nil, err
		}
		rt.ClearPendingServerRemoval(fresh.Name)
		n := *cur
		n.Servers = append(append([]*config.ServerConfig(nil), cur.Servers...), fresh)
		return &n, nil
	}))
	require.NoError(t, rt.upstreamManager.AddServerConfig(fresh.Name, fresh))

	release()
	time.Sleep(500 * time.Millisecond)
	url, present := clientURL(rt, "srv-a")
	require.True(t, present)
	require.Equal(t, "http://127.0.0.1:9/new", url, "late connect goroutine replaced the re-added endpoint")
}

func TestSyncConnectGoroutine_StaleAddCannotUndoQuarantine(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	reached, release := parkConnectionGoroutine(t, "srv-a", true)
	t.Cleanup(release)

	a := r6Server("srv-a", "http://127.0.0.1:1/old", true)
	_, err := rt.ApplyConfig(validConfigWith(rt, a), "")
	require.NoError(t, err)
	<-reached

	q := *a
	q.Quarantined = true
	_, err = rt.ApplyConfig(validConfigWith(rt, &q), "")
	require.NoError(t, err)

	release()
	time.Sleep(500 * time.Millisecond)
	if c, ok := rt.upstreamManager.GetClient("srv-a"); ok {
		require.True(t, c.GetConfig().Quarantined, "late connect goroutine replaced the quarantined config with the stale one")
	}
}

func TestSyncDisconnectGoroutine_StaleDisableCannotDisconnectReEnabledServer(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	reached, release := parkConnectionGoroutine(t, "srv-a", false)
	t.Cleanup(release)

	off := r6Server("srv-a", "http://127.0.0.1:1/x", false)
	_, err := rt.ApplyConfig(validConfigWith(rt, off), "")
	require.NoError(t, err)
	<-reached

	// Newer commit re-enables; its connect goroutine registers the client.
	on := r6Server("srv-a", "http://127.0.0.1:1/x", true)
	_, err = rt.ApplyConfig(validConfigWith(rt, on), "")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, ok := clientURL(rt, "srv-a")
		return ok
	}, 5*time.Second, 20*time.Millisecond, "enabled client never registered")

	release()
	time.Sleep(500 * time.Millisecond)
	_, present := clientURL(rt, "srv-a")
	require.True(t, present, "late disable goroutine disconnected the re-enabled client")
}

// Finding 2: a quarantine decision whose storage write lands while a stale
// reconcile holds the commit lock must survive it.
func TestQuarantineServer_NotRevertedByConcurrentStaleReconcile(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r6Server("srv-a", "http://127.0.0.1:1/x", false)
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		c := config.CopyServerConfig(a)
		c.MarkQuarantineExplicitlySet(true)
		n.Servers = []*config.ServerConfig{c}
		return &n, nil
	}))

	reached, release := parkSyncAfterSnapshot(t)
	t.Cleanup(release)
	syncDone := make(chan error, 1)
	go func() { syncDone <- rt.LoadConfiguredServers(nil) }()
	<-reached // sync parked holding the commit lock, snapshot taken

	qDone := make(chan error, 1)
	go func() { qDone <- rt.QuarantineServer("srv-a", true) }()
	time.Sleep(300 * time.Millisecond)
	release()
	require.NoError(t, <-syncDone)
	require.NoError(t, <-qDone)
	time.Sleep(300 * time.Millisecond)

	got, err := rt.StorageManager().GetUpstreamServer("srv-a")
	require.NoError(t, err)
	require.True(t, got.Quarantined, "storage reverted to unquarantined")
	for _, s := range rt.Config().Servers {
		if s.Name == "srv-a" {
			require.True(t, s.Quarantined, "runtime config reverted to unquarantined")
		}
	}
}

func TestEnableServer_NotRevertedByConcurrentStaleReconcile(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r6Server("srv-a", "http://127.0.0.1:1/x", false)
	a.Quarantined = true
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{config.CopyServerConfig(a)}
		return &n, nil
	}))

	reached, release := parkSyncAfterSnapshot(t)
	t.Cleanup(release)
	syncDone := make(chan error, 1)
	go func() { syncDone <- rt.LoadConfiguredServers(nil) }()
	<-reached

	eDone := make(chan error, 1)
	go func() { eDone <- rt.EnableServer("srv-a", true) }()
	time.Sleep(300 * time.Millisecond)
	release()
	require.NoError(t, <-syncDone)
	require.NoError(t, <-eDone)
	time.Sleep(300 * time.Millisecond)

	got, err := rt.StorageManager().GetUpstreamServer("srv-a")
	require.NoError(t, err)
	require.True(t, got.Enabled, "storage enabled flag reverted")
	for _, s := range rt.Config().Servers {
		if s.Name == "srv-a" {
			require.True(t, s.Enabled, "runtime config enabled flag reverted")
		}
	}
}

// Finding 3: a delayed removal sync, after a later save resurrected the
// removed server, must still reap it.
func TestLoadConfiguredServers_DelayedRemovalSyncSurvivesResurrectingSave(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r5Server("srv-a", "http://127.0.0.1:1/mcp")
	keep := r5Server("srv-keep", "http://127.0.0.1:2/mcp")
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(keep))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{a, keep}
		return &n, nil
	}))

	reached, release := parkFirstSync(t)
	t.Cleanup(release)

	// Apply a config removing A; its async sync parks before the lock.
	_, err := rt.ApplyConfig(validConfigWith(rt, keep), "")
	require.NoError(t, err)
	<-reached

	// Add B, then a save that republishes A's still-existing storage row.
	b := r5Server("srv-b", "http://127.0.0.1:3/mcp")
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

	release()
	waitServerGone(t, rt, "srv-a")
	require.NotContains(t, rt.upstreamManager.GetAllServerNames(), "srv-a")
	require.True(t, serverNames(rt.Config())["srv-b"], "unrelated B was lost")
	got, _ := rt.StorageManager().GetUpstreamServer("srv-b")
	require.NotNil(t, got)
}
