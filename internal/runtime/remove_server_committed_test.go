package runtime

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UX-01 r7: a REST/MCP removal is one commit. A reconciliation that runs while
// the removal is between its storage delete and its save must not re-create the
// deleted row or reconnect the server, and the removal must leave nothing behind.
func TestRemoveServerCommitted_ConcurrentReconcileCannotResurrect(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r5Server("srv-a", "http://127.0.0.1:1/mcp")
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{a}
		return &n, nil
	}))

	paused := make(chan struct{})
	resume := make(chan struct{})
	removeServerAfterStorageDeleteHook = func(string) {
		close(paused)
		<-resume
	}
	t.Cleanup(func() { removeServerAfterStorageDeleteHook = nil })

	removeDone := make(chan error, 1)
	go func() { removeDone <- rt.RemoveServerCommitted("srv-a") }()
	<-paused

	// Reconciliation starts while the removal is parked after the storage delete.
	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- rt.LoadConfiguredServers(nil) }()
	select {
	case <-reconcileDone:
		t.Fatal("reconciliation ran inside the removal's commit window")
	case <-time.After(300 * time.Millisecond):
	}

	close(resume)
	require.NoError(t, <-removeDone)
	require.NoError(t, <-reconcileDone)
	time.Sleep(600 * time.Millisecond) // drain scheduled connections / cleanups

	got, _ := rt.StorageManager().GetUpstreamServer("srv-a")
	require.Nil(t, got, "reconciliation recreated the deleted storage row")
	require.False(t, serverNames(rt.Config())["srv-a"], "runtime config still holds the server")
	raw, err := os.ReadFile(rt.ConfigPath())
	require.NoError(t, err)
	require.False(t, strings.Contains(string(raw), "srv-a"), "disk still holds the server")
	require.NotContains(t, rt.upstreamManager.GetAllServerNames(), "srv-a")
	require.NoError(t, rt.SaveConfiguration())
	require.False(t, serverNames(rt.Config())["srv-a"], "a later save republished the server")
}

func TestRemoveServerCommitted_UnknownServer(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	require.ErrorIs(t, rt.RemoveServerCommitted("nope"), ErrServerNotFound)
}
