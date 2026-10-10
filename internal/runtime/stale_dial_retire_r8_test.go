package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
)

// UX-01 r8 (finding 2): the interval between a commit publishing a config and
// the asynchronous manager reconciliation for it must not let a client that was
// captured earlier launch the old endpoint.

func stdioMarkerServer(name, marker string) *config.ServerConfig {
	return &config.ServerConfig{
		Name: name, Command: "/bin/sh", Protocol: "stdio", Enabled: true, Created: time.Now(),
		Args: []string{"-c", "touch " + marker + "; exec cat"},
	}
}

func parkAfterRegister(t *testing.T, name string) (reached <-chan struct{}, release func()) {
	t.Helper()
	r := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	connectAfterRegisterHook = func(n string) {
		if n != name {
			return
		}
		first := false
		once.Do(func() { first = true })
		if first {
			close(r)
			<-gate
		}
	}
	t.Cleanup(func() { connectAfterRegisterHook = nil })
	var relOnce sync.Once
	rel := func() { relOnce.Do(func() { close(gate) }) }
	t.Cleanup(rel)
	return r, rel
}

func countLaunches(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	core.AfterLaunchHook = func(string) { n.Add(1) }
	t.Cleanup(func() { core.AfterLaunchHook = nil })
	return &n
}

func TestStaleDial_RemovalPublishedWhileReconcileQueued_OldEndpointNeverStarts(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	marker := filepath.Join(t.TempDir(), "old-launched")
	launches := countLaunches(t)
	reached, release := parkAfterRegister(t, "srv-a")

	// Keep the removal's own manager reconciliation queued, as in the review's
	// interleaving: the publication is visible, the manager still holds A.
	cleanupGate := make(chan struct{})
	serverRemovalCleanupHook = func(n string) {
		if n == "srv-a" {
			<-cleanupGate
		}
	}
	t.Cleanup(func() { serverRemovalCleanupHook = nil })
	var cleanupOnce sync.Once
	t.Cleanup(func() { cleanupOnce.Do(func() { close(cleanupGate) }) })

	_, err := rt.ApplyConfig(validConfigWith(rt, stdioMarkerServer("srv-a", marker)), "")
	require.NoError(t, err)
	<-reached // registered with the manager, dial not started

	_, err = rt.ApplyConfig(validConfigWith(rt), "") // publishes the removal
	require.NoError(t, err)
	_, stillRegistered := rt.upstreamManager.GetClient("srv-a")
	require.True(t, stillRegistered, "test premise: manager reconciliation has not run yet")

	release()
	time.Sleep(700 * time.Millisecond)
	require.Zero(t, launches.Load(), "the removed server's command was started")
	require.NoFileExists(t, marker)
	cleanupOnce.Do(func() { close(cleanupGate) })
	waitServerGone(t, rt, "srv-a") // let the parked cleanup finish before the hook is reset
	time.Sleep(200 * time.Millisecond)
}

func TestStaleDial_DisablePublishedWhileReconcileQueued_OldEndpointNeverStarts(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	dir := t.TempDir()
	oldMarker := filepath.Join(dir, "old")
	reached, release := parkAfterRegister(t, "srv-a")
	launches := countLaunches(t)

	_, err := rt.ApplyConfig(validConfigWith(rt, stdioMarkerServer("srv-a", oldMarker)), "")
	require.NoError(t, err)
	<-reached

	// Disabled in the newer commit: nothing may start for srv-a at all.
	disabled := stdioMarkerServer("srv-a", oldMarker)
	disabled.Enabled = false
	_, err = rt.ApplyConfig(validConfigWith(rt, disabled), "")
	require.NoError(t, err)

	release()
	time.Sleep(700 * time.Millisecond)
	require.Zero(t, launches.Load(), "the disabled server's old command was started")
	require.NoFileExists(t, oldMarker)
}

// UX-01 r9: a published command change retires the captured old client even
// though the manager reconciliation for it is still queued; the old command
// must never start, and the new one must start once the reconciliation runs.
func TestStaleDial_CommandChangePublishedWhileReconcileQueued_OldEndpointNeverStarts(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	dir := t.TempDir()
	oldMarker := filepath.Join(dir, "old")
	newMarker := filepath.Join(dir, "new")
	reached, release := parkAfterRegister(t, "srv-a")
	// Counted by hand: the hook is cleared before the reconciliation is released
	// (no launch is in flight until then), so the new endpoint's later launch
	// never reads a global a cleanup is writing.
	var launches atomic.Int32
	core.AfterLaunchHook = func(string) { launches.Add(1) }

	// Park the SECOND reconciliation (the one for the endpoint change) before it
	// touches anything; the first one registered the old client and parked it.
	var syncs atomic.Int32
	syncParked := make(chan struct{})
	syncGate := make(chan struct{})
	loadConfiguredServersBeforeSyncHook = func() {
		if syncs.Add(1) == 2 {
			close(syncParked)
			<-syncGate
		}
	}
	t.Cleanup(func() { loadConfiguredServersBeforeSyncHook = nil })
	var syncOnce sync.Once
	releaseSync := func() { syncOnce.Do(func() { close(syncGate) }) }
	t.Cleanup(releaseSync)

	_, err := rt.ApplyConfig(validConfigWith(rt, stdioMarkerServer("srv-a", oldMarker)), "")
	require.NoError(t, err)
	<-reached // old client registered, dial not started

	_, err = rt.ApplyConfig(validConfigWith(rt, stdioMarkerServer("srv-a", newMarker)), "")
	require.NoError(t, err)
	<-syncParked
	live, ok := clientURLOrCommand(rt, "srv-a")
	require.True(t, ok)
	require.Contains(t, live, oldMarker, "test premise: the manager still holds the old endpoint")

	release()
	time.Sleep(700 * time.Millisecond)
	require.Zero(t, launches.Load(), "the replaced endpoint's old command was started")
	require.NoFileExists(t, oldMarker)

	core.AfterLaunchHook = nil
	releaseSync()
	require.Eventually(t, func() bool {
		_, err := os.Stat(newMarker)
		return err == nil
	}, 10*time.Second, 50*time.Millisecond, "the new endpoint never started after reconciliation")
	require.NoFileExists(t, oldMarker)
}

func clientURLOrCommand(rt *Runtime, name string) (string, bool) {
	c, ok := rt.upstreamManager.GetClient(name)
	if !ok {
		return "", false
	}
	cfg := c.GetConfig()
	return cfg.URL + " " + cfg.Command + " " + strings.Join(cfg.Args, " "), true
}
