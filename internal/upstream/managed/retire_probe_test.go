package managed

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Sol review round 2 (Spec 116 PR4, FR-026): a health probe already in
// flight when the instance is retired fails with "transport closed" (the
// retirement killed its stdio child). That failure says nothing about the
// server: it must not move the retired instance to Error, count toward the
// failure threshold or burn the retry budget. The entry check alone missed
// it because the probe passed it before the retirement.

func TestHealthCheck_ProbeInFlightAtRetirement_NoErrorNoAccounting(t *testing.T) {
	mc := newTestClientForHealth(t)
	prober := newRecordingProber(transport.NewError(transport.ErrTransportClosed))
	prober.block = make(chan struct{})
	mc.healthProbe = prober

	done := make(chan struct{})
	go func() { mc.performHealthCheck(); close(done) }()
	require.Eventually(t, func() bool { return prober.pingCount() == 1 }, 2*time.Second, time.Millisecond,
		"the probe is in flight")

	mc.Retire() // without a disconnect: the instance stays Ready on paper
	close(prober.block)
	<-done

	require.Equal(t, types.StateReady, mc.StateManager.GetState(), "a retired instance's probe failure moves no state")
	require.Zero(t, mc.consecutiveHealthFailures.Load(), "nor counts toward the failure threshold")
	require.Zero(t, mc.StateManager.GetConnectionInfo().RetryCount, "nor burns the retry budget")
}

func TestAmbiguousProbe_InFlightAtRetirement_NoError(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, wrappedCanceled())
	prober := newRecordingProber(transport.NewError(transport.ErrTransportClosed))
	prober.block = make(chan struct{})
	mc.healthProbe = prober

	_, err := mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)
	require.Eventually(t, func() bool { return prober.pingCount() == 1 }, 2*time.Second, time.Millisecond,
		"the ambiguous-cancellation probe is in flight")

	mc.Retire()
	close(prober.block)
	prober.waitForPing(t)
	require.Eventually(t, func() bool { return !mc.ambiguousProbeInFlight.Load() }, 2*time.Second, time.Millisecond)

	require.Equal(t, types.StateReady, mc.StateManager.GetState(), "a retired instance's probe failure moves no state")
	require.Zero(t, mc.StateManager.GetConnectionInfo().RetryCount)
}

// Control: the same in-flight failure on an instance that is NOT retired
// does mark it Error, so the assertions above are not vacuous.
func TestHealthCheck_ProbeInFlight_NotRetired_MarksError(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.healthProbe = newRecordingProber(transport.NewError(transport.ErrTransportClosed))
	mc.performHealthCheck()
	require.Equal(t, types.StateError, mc.StateManager.GetState())
}
