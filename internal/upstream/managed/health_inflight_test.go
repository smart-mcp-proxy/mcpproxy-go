package managed

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// withInFlightSuppressionCap temporarily shrinks the package-level
// inFlightSuppressionCap so a test can exercise "the cap was exceeded"
// without sleeping the real 15-minute default.
func withInFlightSuppressionCap(t *testing.T, cap time.Duration) {
	t.Helper()
	orig := inFlightSuppressionCap
	inFlightSuppressionCap = cap
	t.Cleanup(func() { inFlightSuppressionCap = orig })
}

// TestPerformHealthCheck_TransientPingToleratedWhileCallInFlight is the
// regression test for #1317: a stdio upstream (e.g. chrome-devtools-mcp
// waiting on a human consent prompt) serves one JSON-RPC exchange at a time,
// so a legitimately slow tool call can make the health loop's concurrent
// `ping` time out too. Before the fix, healthCheckFailureThreshold
// consecutive misses of that kind flipped the server to Error and
// tryReconnect() then disconnected — killing the process the in-flight call
// was still waiting on. The fix must tolerate transient ping failures
// indefinitely while a call is in flight, so the process stays alive for the
// call to either finish or hit its own CallToolTimeout.
func TestPerformHealthCheck_TransientPingToleratedWhileCallInFlight(t *testing.T) {
	mc := newTestClientForHealth(t)
	fake := &fakeProber{pingErr: errors.New("context deadline exceeded")}
	mc.healthProbe = fake

	endCall := mc.beginInFlightCall() // simulate the stuck-on-consent tool call
	defer endCall()

	// Run well past healthCheckFailureThreshold consecutive transient
	// failures. Without the in-flight guard this would flip to Error.
	for i := 0; i < healthCheckFailureThreshold*3; i++ {
		mc.performHealthCheck()
	}

	assert.Equal(t, healthCheckFailureThreshold*3, fake.pingCalls)
	assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
		"a transient ping failure must not evict the server while a tool call is in flight")
	assert.Equal(t, 0, mc.consecutiveHealthFailures,
		"the in-flight guard must not accumulate toward the threshold either")
}

// TestPerformHealthCheck_TransientPingResumesNormalThresholdAfterCallEnds
// verifies the in-flight guard is scoped to the call's lifetime, not a
// permanent bypass: once the call ends (counter back to zero), the ordinary
// consecutive-failure threshold still applies and a genuinely dead upstream
// is still detected.
func TestPerformHealthCheck_TransientPingResumesNormalThresholdAfterCallEnds(t *testing.T) {
	mc := newTestClientForHealth(t)
	fake := &fakeProber{pingErr: errors.New("context deadline exceeded")}
	mc.healthProbe = fake

	endCall := mc.beginInFlightCall()
	mc.performHealthCheck()
	mc.performHealthCheck()
	assert.Equal(t, types.StateReady, mc.StateManager.GetState())
	assert.Equal(t, 0, mc.consecutiveHealthFailures, "no counting while the call is in flight")
	endCall() // the call finished (or hit its own timeout)

	for i := 1; i < healthCheckFailureThreshold; i++ {
		mc.performHealthCheck()
		assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
			"transient failure #%d after the call ended should still be tolerated below threshold", i)
	}
	mc.performHealthCheck()
	assert.Equal(t, types.StateError, mc.StateManager.GetState(),
		"the Nth consecutive transient failure after the call ended must still flip to Error")
}

// TestPerformHealthCheck_SuppressionCapEventuallyEvictsWhileStillInFlight is
// the round-1 review finding: an in-flight call must not suppress eviction
// FOREVER (e.g. a caller retrying back-to-back, keeping the counter above
// zero continuously while the upstream is actually dead). Once
// inFlightSuppressionCap elapses, normal threshold-based eviction must
// resume even though a call is still (nominally) in flight.
func TestPerformHealthCheck_SuppressionCapEventuallyEvictsWhileStillInFlight(t *testing.T) {
	withInFlightSuppressionCap(t, 20*time.Millisecond)

	mc := newTestClientForHealth(t)
	fake := &fakeProber{pingErr: errors.New("context deadline exceeded")}
	mc.healthProbe = fake

	endCall := mc.beginInFlightCall()
	defer endCall()

	// Immediately: tolerated (within cap).
	mc.performHealthCheck()
	assert.Equal(t, types.StateReady, mc.StateManager.GetState())

	// Wait past the (shrunk) cap, then keep failing while still "in flight".
	require.Eventually(t, func() bool {
		mc.performHealthCheck()
		return mc.StateManager.GetState() == types.StateError
	}, time.Second, 5*time.Millisecond,
		"eviction must resume once the in-flight suppression cap elapses, even with a call still in flight")
}

// TestPerformHealthCheck_HardErrorEvictsEvenWithCallInFlight verifies the
// guard is narrow: it only tolerates the ambiguous "transport looks busy"
// signal. Hard evidence the connection is actually broken (connection
// refused/reset, broken pipe) must still evict immediately, in-flight call or
// not — the call is doomed either way, and the user should see it.
func TestPerformHealthCheck_HardErrorEvictsEvenWithCallInFlight(t *testing.T) {
	mc := newTestClientForHealth(t)
	fake := &fakeProber{pingErr: errors.New("dial tcp 127.0.0.1:65535: connect: connection refused")}
	mc.healthProbe = fake

	endCall := mc.beginInFlightCall()
	defer endCall()

	mc.performHealthCheck()

	assert.Equal(t, types.StateError, mc.StateManager.GetState(),
		"a hard connection error must still evict immediately even with a call in flight")
}
