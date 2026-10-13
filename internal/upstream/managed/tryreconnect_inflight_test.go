package managed

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// TestTryReconnect_SkipsDisconnectWhileCallInFlight is the round-1 review
// finding for #1317: performHealthCheck's in-flight check happens strictly
// BEFORE tryReconnect() runs, so a call starting in that gap would still
// reach the unconditional Disconnect() with the OLD fix. tryReconnect()
// itself must re-check right before disconnecting.
//
// newTestClientForHealth leaves mc.coreClient nil (see health_ping_test.go),
// so reaching mc.coreClient.Disconnect() would panic -- that panic is the
// proof the guard did NOT hold. No panic here means the guard worked.
func TestTryReconnect_SkipsDisconnectWhileCallInFlight(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.StateManager.SetError(errors.New("context deadline exceeded"))

	endCall := mc.beginInFlightCall()
	defer endCall()

	require.NotPanics(t, func() { mc.tryReconnect() },
		"tryReconnect must not reach coreClient.Disconnect() while a call is in flight")

	assert.Equal(t, types.StateError, mc.StateManager.GetState(),
		"a skipped reconnect leaves the state as-is for the next tick to retry")
	assert.False(t, mc.reconnectInProgress,
		"the reconnect-in-progress flag must still be cleared on the skip path")
}

// TestTryReconnect_ProceedsOnceSuppressionCapExceeded proves the guard is
// not a permanent bypass: once inFlightSuppressionCap has elapsed for the
// current busy streak, tryReconnect() proceeds to disconnect exactly as
// before, in-flight call or not.
func TestTryReconnect_ProceedsOnceSuppressionCapExceeded(t *testing.T) {
	withInFlightSuppressionCap(t, 5*time.Millisecond)

	mc := newTestClientForHealth(t)
	mc.StateManager.SetError(errors.New("context deadline exceeded"))

	endCall := mc.beginInFlightCall()
	defer endCall()

	// Open the suppression window now, then wait past the (shrunk) cap.
	mc.trackInFlightSuppression()
	time.Sleep(10 * time.Millisecond)

	assert.Panics(t, func() { mc.tryReconnect() },
		"once the suppression cap is exceeded, tryReconnect must reach coreClient.Disconnect() (nil coreClient panics -- that IS the proof)")
}

// TestTryReconnect_HardFailureEvictsImmediatelyEvenWithCallInFlight is the
// round-2 review finding: the in-flight guard must only defer a reconnect
// triggered by the SAME transient/ambiguous signal the health check
// tolerates. A hard connection failure (connection refused/reset, broken
// pipe) has nothing to do with a busy single-flight transport and must
// still evict immediately, in-flight call or not -- exactly like
// performHealthCheck's own policy.
func TestTryReconnect_HardFailureEvictsImmediatelyEvenWithCallInFlight(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.StateManager.SetError(errors.New("dial tcp 127.0.0.1:65535: connect: connection refused"))

	endCall := mc.beginInFlightCall()
	defer endCall()

	assert.Panics(t, func() { mc.tryReconnect() },
		"a hard-failure-triggered reconnect must proceed to coreClient.Disconnect() immediately, regardless of any in-flight call")
}

// TestTryReconnect_OAuthErrorEvictsImmediatelyEvenWithCallInFlight is a
// round-3 review finding: an OAuth error's message can itself contain
// "timeout" (e.g. "OAuth authorization timeout"), which would otherwise
// pass isTransientHealthCheckError's substring check and incorrectly defer
// an OAuth-triggered reconnect. The guard must exclude OAuth errors
// explicitly via ConnectionInfo.IsOAuthError, not just by error text.
func TestTryReconnect_OAuthErrorEvictsImmediatelyEvenWithCallInFlight(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.StateManager.SetOAuthError(errors.New("OAuth authorization timeout"))

	endCall := mc.beginInFlightCall()
	defer endCall()

	assert.Panics(t, func() { mc.tryReconnect() },
		"an OAuth-error-triggered reconnect must proceed to coreClient.Disconnect() immediately, regardless of any in-flight call")
}

// TestTryReconnectSync_SkipsDisconnectWhileCallInFlight is the round-3
// review finding on TryReconnectSync: it is reached whenever IsConnected()
// is false, which can be caused by a failure entirely unrelated to a
// specific in-flight CallTool (e.g. a concurrent ListTools timeout) --
// IsConnected()==false does not prove every call on the connection is
// doomed. It must share tryReconnect()'s in-flight guard.
func TestTryReconnectSync_SkipsDisconnectWhileCallInFlight(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.StateManager.SetError(errors.New("context deadline exceeded"))

	endCall := mc.beginInFlightCall()
	defer endCall()

	var err error
	require.NotPanics(t, func() { err = mc.TryReconnectSync(context.Background()) },
		"TryReconnectSync must not reach coreClient.Disconnect() while a call is in flight")
	assert.Error(t, err, "a deferred reconnect must report that it deferred, not silently succeed")
}

// TestConnect_SkipsDisconnectWhileCallInFlight is a round-5 review finding:
// Client.Connect() is ALSO reached from Error state by the runtime's
// periodic backgroundConnections sweep (Manager.ConnectAll every 60s), a
// third automatic reconnect path besides tryReconnect and TryReconnectSync
// that disconnected unconditionally before this guard.
func TestConnect_SkipsDisconnectWhileCallInFlight(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.StateManager.SetError(errors.New("context deadline exceeded"))

	endCall := mc.beginInFlightCall()
	defer endCall()

	var err error
	require.NotPanics(t, func() { err = mc.Connect(context.Background()) },
		"Connect must not reach coreClient.Disconnect() while a call is in flight")
	assert.Error(t, err, "a deferred connect must report that it deferred, not silently succeed")
	assert.Equal(t, types.StateError, mc.StateManager.GetState(),
		"a skipped connect leaves the state as-is for the next attempt to retry")
}

// TestInFlightSuppression_ResetsWhenCallCountReachesZero is the other
// round-2 review finding: the suppression clock must close the moment the
// LAST in-flight call actually ends (the decrement in callTool reaching
// 0), not only when some later health-check tick happens to observe zero.
// Otherwise a brand-new busy streak starting right after an already-capped
// streak ended would inherit the stale, already-expired clock and get zero
// grace period.
func TestInFlightSuppression_ResetsWhenCallCountReachesZero(t *testing.T) {
	mc := newTestClientForHealth(t)
	fake := newBlockingToolCaller()
	mc.toolInvoker = fake

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = mc.CallTool(context.Background(), "navigate_page", map[string]interface{}{"url": "https://example.com"})
	}()
	select {
	case <-fake.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the fake tool call to start")
	}
	require.Eventually(t, mc.hasInFlightToolCall, time.Second, 5*time.Millisecond)

	// Open (and, by directly manipulating time, effectively expire) a
	// suppression window for this first call.
	mc.trackInFlightSuppression()
	expired := time.Now().Add(-2 * inFlightSuppressionCap)
	mc.inFlightMu.Lock()
	mc.inFlightSuppressionSince = &expired
	mc.inFlightMu.Unlock()

	close(fake.release) // the first call ends
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first call to return")
	}
	require.Eventually(t, func() bool { return !mc.hasInFlightToolCall() }, time.Second, 5*time.Millisecond)

	// A brand-new call starts. Its streak must get a FRESH window, not the
	// expired timestamp left over from the first call.
	fake2 := newBlockingToolCaller()
	mc.toolInvoker = fake2
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		_, _ = mc.CallTool(context.Background(), "navigate_page", map[string]interface{}{"url": "https://example.com"})
	}()
	defer func() {
		close(fake2.release)
		<-done2
	}()
	select {
	case <-fake2.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the second fake tool call to start")
	}
	require.Eventually(t, mc.hasInFlightToolCall, time.Second, 5*time.Millisecond)

	elapsed, withinCap, inFlight := mc.trackInFlightSuppression()
	assert.True(t, inFlight, "the second call must be observed as in flight")
	assert.True(t, withinCap, "a brand-new busy streak must start its own fresh suppression window")
	assert.Less(t, elapsed, time.Second, "a fresh window's elapsed time must be small, not inherited from the prior (expired) streak")
}
