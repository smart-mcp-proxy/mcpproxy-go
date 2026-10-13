package upstream

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 116 PR4, T4.15 (US4 scenario 6, review R5.4): the old instance is
// parked before its stdio launch, or before its HTTP send; RestartServer /
// ForceReconnectAll / Docker recovery replace it at the SAME config
// generation (all three go through ReplaceInstanceAt). Released, the old
// instance launches nothing and sends nothing; the replacement connects and
// only its own traffic reaches the upstream.

// replaceParked runs ReplaceInstanceAt while old is parked. ReplaceInstanceAt
// retires old under the manager locks and then disconnects it, which waits
// for old's parked connect; the test therefore synchronises on retirement
// (the barrier closed) and releases the park before ReplaceInstanceAt
// returns.
func replaceParked(t *testing.T, m *Manager, name string, old *managed.Client, release func()) *managed.Client {
	t.Helper()
	type res struct {
		inst *managed.Client
		err  error
	}
	done := make(chan res, 1)
	go func() {
		inst, err := m.ReplaceInstanceAt(name, old.Generation(), old, nil)
		done <- res{inst, err}
	}()
	require.Eventually(t, old.Retired, 5*time.Second, time.Millisecond,
		"the same-generation replacement retires the old instance")
	require.True(t, old.Gate().Retired())
	release()
	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.NotNil(t, r.inst)
		require.NotEqual(t, old.InstanceID(), r.inst.InstanceID())
		require.Equal(t, old.Generation(), r.inst.Generation(), "same generation")
		return r.inst
	case <-time.After(20 * time.Second):
		t.Fatal("ReplaceInstanceAt did not return")
		return nil
	}
}

func TestRetire_SameGenerationReplace_StdioOldLaunchesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	dir := t.TempDir()
	m := newGenTestManager(t)
	cfg := markerCommand(t, "marker-restart", dir)
	m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
	require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
	old := registered(t, m, cfg.Name)

	parked, release := parkFirstAdmit(old)
	defer release()
	errc := connectAsync(old)
	<-parked

	repl := replaceParked(t, m, cfg.Name, old, release)
	t.Cleanup(func() { _ = repl.Disconnect() })
	require.Error(t, waitErr(t, errc))
	time.Sleep(300 * time.Millisecond)
	assert.Zero(t, markers(t, dir), "the replaced instance launched nothing")

	// The replacement is unaffected: it launches its own process.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = repl.Connect(ctx) // the marker command is not an MCP server
	require.Eventually(t, func() bool { return markers(t, dir) == 1 }, 5*time.Second, 50*time.Millisecond,
		"exactly one launch: the replacement's")
}

func TestRetire_SameGenerationReplace_HTTPOldSendsNothing(t *testing.T) {
	up := newCountingUpstream(t, "http")
	m := newGenTestManager(t)
	cfg := httpServerConfig("acme", up.url, "http")
	m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
	require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
	old := registered(t, m, cfg.Name)

	parked, release := parkFirstAdmit(old)
	defer release()
	errc := connectAsync(old)
	<-parked

	repl := replaceParked(t, m, cfg.Name, old, release)
	t.Cleanup(func() { _ = repl.Disconnect() })
	require.Error(t, waitErr(t, errc))
	time.Sleep(100 * time.Millisecond)
	req, auth := up.counts()
	assert.Zero(t, req, "the replaced instance sent nothing")
	assert.Zero(t, auth)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, repl.Connect(ctx), "the replacement connects normally")
	req, auth = up.counts()
	assert.Positive(t, req)
	assert.Positive(t, auth)
}
