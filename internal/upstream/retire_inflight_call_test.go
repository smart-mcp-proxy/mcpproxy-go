package upstream

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 116 PR4, T4.6 (FR-026): a tool call in flight on an instance being
// retired fails as "unavailable" and is not charged to the replacement; the
// retired instance's connection is closed by Retire and anything it would
// send afterwards is refused.
func TestRetire_InFlightToolCall_UnavailableAndReplacementUnaffected(t *testing.T) {
	up := newCountingUpstream(t, "http")
	m := newGenTestManager(t)
	cfg := httpServerConfig("acme", up.url, "http")
	m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
	require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
	inst := registered(t, m, cfg.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, inst.Connect(ctx))

	callErr := make(chan error, 1)
	go func() {
		_, err := m.CallTool(ctx, "acme:slow", nil)
		callErr <- err
	}()
	select {
	case <-up.inCall:
	case <-time.After(10 * time.Second):
		t.Fatal("the call never reached the upstream")
	}

	// Commit: a replacement endpoint at a newer generation.
	up2 := newCountingUpstream(t, "http")
	cfg2 := httpServerConfig("acme", up2.url, "http")
	start := time.Now()
	res := m.AdvanceGenerations([]GenerationChange{{Name: "acme", Gen: 2, Present: true, Reconnect: true, Config: cfg2}})
	assert.Less(t, time.Since(start), time.Second, "retiring an instance with a call in flight is bounded")
	require.Len(t, res.Unregistered, 1)
	require.True(t, inst.Retired())

	var err error
	select {
	case err = <-callErr:
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight call was not ended by retirement")
	}
	require.Error(t, err)
	assert.True(t, errors.Is(err, managed.ErrInstanceRetired), "unavailable, got %v", err)
	assert.Contains(t, err.Error(), "unavailable")

	// Nothing more leaves the retired instance.
	before, _ := up.counts()
	_, err = inst.CallTool(context.Background(), "echo", nil)
	assert.ErrorIs(t, err, managed.ErrInstanceRetired)
	_, err = inst.ListTools(context.Background())
	assert.ErrorIs(t, err, managed.ErrInstanceRetired)
	after, _ := up.counts()
	assert.Equal(t, before, after)

	// The replacement installs, connects and carries no failure history.
	require.NoError(t, m.AddServerConfigAt("acme", cfg2, 2))
	repl := registered(t, m, "acme")
	t.Cleanup(func() { _ = repl.Disconnect() })
	require.NoError(t, repl.Connect(ctx))
	assert.True(t, repl.IsConnected())
	calls, failures, _ := m.CallStats("acme")
	assert.Zero(t, calls, "the retired instance's failure is not charged to the replacement")
	assert.Zero(t, failures)
	_, err = m.CallTool(ctx, "acme:echo", nil)
	require.NoError(t, err)
}

// T4.5 (FR-023a): a tools/list on an instance retired mid-request is ended,
// its capture is not current, and the retired instance's later sends are
// refused without touching the old endpoint.
func TestRetire_ListToolsAfterRetire_NoTraffic(t *testing.T) {
	up := newCountingUpstream(t, "http")
	m := newGenTestManager(t)
	cfg := httpServerConfig("acme", up.url, "http")
	m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
	require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
	inst := registered(t, m, cfg.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, inst.Connect(ctx))
	_, capture, err := inst.ListToolsCaptured(ctx)
	require.NoError(t, err)
	require.True(t, inst.CaptureIsCurrent(capture))

	m.AdvanceGenerations([]GenerationChange{{Name: "acme", Gen: 2, Present: true, Reconnect: true, Config: httpServerConfig("acme", "http://127.0.0.1:1/new", "http")}})
	assert.False(t, inst.CaptureIsCurrent(capture), "a retired instance's capture is never current")

	before, _ := up.counts()
	_, _, err = inst.ListToolsCaptured(ctx)
	assert.ErrorIs(t, err, managed.ErrInstanceRetired)
	// Even a send that bypasses the managed checks is refused by the gate.
	assert.True(t, inst.Gate().Retired())
	_, err = inst.Gate().HTTPClient(time.Second).Get(up.url)
	assert.ErrorIs(t, err, dialgate.ErrRetired)
	after, _ := up.counts()
	assert.Equal(t, before, after)
}
