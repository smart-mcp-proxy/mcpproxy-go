package managed

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Spec 105 FR-009 "stale generation" (research D4; codex r3 D2). The server
// certifies a tool identity — its registration in the discovery snapshot,
// its tier, its approval hash — against ONE connection generation
// (ConnectionEpoch). The dispatch that follows must reach the transport on
// that same generation: a call authorized on connection A that is executed
// on connection B (a disconnect + reconnect that completed while the call
// was queued behind admission control, or between the check and the
// transport) reaches B before B's own discovery has run, so a tool B changed
// or removed under the same name gets one un-gated call. CallToolOnEpoch
// re-checks the generation AFTER the queue wait, immediately before the
// transport, under the same mutex Connect/Disconnect bump it, and refuses
// with ErrConnectionGenerationChanged on mismatch — zero transport calls.

// simulateReconnect replays the generation bump Connect performs in its
// phase 4 (epoch bump under epochMu, then Ready), on a hand-built client
// whose Disconnect already closed the previous generation.
func simulateReconnect(mc *Client) {
	mc.epochMu.Lock()
	mc.connectionEpoch.Store(nextConnectionEpoch())
	mc.epochMu.Unlock()
	if mc.StateManager.GetState() != types.StateReady {
		mc.StateManager.TransitionTo(types.StateConnecting)
		mc.StateManager.TransitionTo(types.StateReady)
	}
}

// TestCallToolOnEpoch_RefusesWhenGenerationChangesWhileQueued is the D2
// reproduction: the slot is held, the pinned call parks in the admission
// queue, the connection drops and comes back (two epoch bumps), the slot is
// released — and the queued call must be refused, never invoked.
func TestCallToolOnEpoch_RefusesWhenGenerationChangesWhileQueued(t *testing.T) {
	sc := &config.ServerConfig{
		Name:                  "db",
		Enabled:               true,
		MaxConcurrentRequests: intPtrAdm(1),
		QueueSize:             intPtrAdm(4),
		QueueTimeout:          durPtrAdm(30 * time.Second),
	}
	cfg := &config.Config{Servers: []*config.ServerConfig{sc}}
	mc, reg := newAdmissionClient(t, cfg, "db", nil)
	fake := &fakeToolCaller{}
	mc.toolInvoker = fake

	validated := mc.ConnectionEpoch()

	// Hold the single slot so the pinned call has to queue.
	holdSlot, err := mc.acquireAdmission(context.Background(), "hold")
	require.NoError(t, err)

	type outcome struct {
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		_, callErr := mc.CallToolOnEpoch(context.Background(), "erase", nil, validated)
		done <- outcome{err: callErr}
	}()
	require.Eventually(t, func() bool {
		return reg.Server("db").Stats().Queued == 1
	}, 2*time.Second, 5*time.Millisecond, "the pinned call must be parked in the admission queue")

	// The connection drops and a new generation comes up while the call is
	// queued: Disconnect bumps, the reconnect bumps again and returns Ready.
	require.NoError(t, mc.Disconnect())
	simulateReconnect(mc)
	require.True(t, mc.IsConnected(), "fixture: the NEW generation is Ready")
	require.NotEqual(t, validated, mc.ConnectionEpoch(), "fixture: the generation moved")

	holdSlot()

	select {
	case out := <-done:
		require.Error(t, out.err)
		assert.True(t, errors.Is(out.err, ErrConnectionGenerationChanged), "got %v", out.err)
	case <-time.After(5 * time.Second):
		t.Fatal("the queued call never returned")
	}
	assert.Equal(t, 0, fake.callCount(), "a call certified on the previous generation must never reach the transport")
}

// TestCallToolOnEpoch_MismatchAtEntryIsRefused covers the cheap pre-queue
// check: a generation that already moved is refused before any slot is
// taken, and a disconnected client (whose Disconnect bumped the epoch) is
// refused the same way — a reconnect is a new generation by construction.
func TestCallToolOnEpoch_MismatchAtEntryIsRefused(t *testing.T) {
	mc, fake := newTestClientForCallTool(t, nil)
	stale := mc.ConnectionEpoch() - 1

	_, err := mc.CallToolOnEpoch(context.Background(), "erase", nil, stale)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConnectionGenerationChanged), "got %v", err)
	assert.Equal(t, 0, fake.callCount())

	validated := mc.ConnectionEpoch()
	require.NoError(t, mc.Disconnect())
	_, err = mc.CallToolOnEpoch(context.Background(), "erase", nil, validated)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConnectionGenerationChanged), "a dropped connection is a closed generation: %v", err)
	assert.Equal(t, 0, fake.callCount())
}

// TestCallToolOnEpoch_SameGenerationDispatches is the positive control: the
// pinned call on the generation it was certified for reaches the transport
// exactly like CallTool.
func TestCallToolOnEpoch_SameGenerationDispatches(t *testing.T) {
	mc, fake := newTestClientForCallTool(t, nil)

	_, err := mc.CallToolOnEpoch(context.Background(), "erase", nil, mc.ConnectionEpoch())
	require.NoError(t, err)
	assert.Equal(t, 1, fake.callCount())

	// The unpinned entry point is unchanged.
	_, err = mc.CallTool(context.Background(), "erase", nil)
	require.NoError(t, err)
	assert.Equal(t, 2, fake.callCount())
}

// TestConnectionEpoch_UniqueAcrossReplacementClients pins codex round 8: a
// replacement client instance installed under the SAME server name must not
// restart the epoch sequence, or a discovery stamp certified against the old
// instance's epoch would match the new instance's first connection and an
// epoch-pinned dispatch would send a stale name to it. Epochs are drawn from a
// process-wide counter, so the two instances' first connections never share a
// value and the pinned call against the old epoch is refused on the new one.
func TestConnectionEpoch_UniqueAcrossReplacementClients(t *testing.T) {
	sc := &config.ServerConfig{Name: "db", Enabled: true}
	cfg := &config.Config{Servers: []*config.ServerConfig{sc}}

	a, _ := newAdmissionClient(t, cfg, "db", nil)
	simulateReconnect(a) // instance A: first connection
	epochA := a.ConnectionEpoch()

	b, _ := newAdmissionClient(t, cfg, "db", nil)
	simulateReconnect(b) // instance B replaces A under the same name
	epochB := b.ConnectionEpoch()

	require.NotEqual(t, epochA, epochB, "two client instances under one server name must never share a connection epoch")
	require.Greater(t, epochB, epochA, "epochs are strictly increasing process-wide")

	fake := &fakeToolCaller{}
	b.toolInvoker = fake
	_, err := b.CallToolOnEpoch(context.Background(), "erase", nil, epochA)
	require.Error(t, err, "a dispatch pinned to instance A's epoch must be refused by instance B")
	require.Zero(t, fake.callCount(), "the stale-pinned call must never reach the transport")
}
