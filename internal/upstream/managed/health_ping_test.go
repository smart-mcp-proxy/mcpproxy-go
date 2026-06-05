package managed

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// fakeProber records how the health loop probes liveness. It implements only
// Ping — deliberately NOT ListTools — so the health path provably cannot fall
// back to a heavyweight tools/list. The managed client's coreClient is left nil
// in these tests, so any accidental ListTools on the core client would panic and
// fail the test outright.
type fakeProber struct {
	pingCalls int
	pingErr   error
}

func (f *fakeProber) Ping(_ context.Context) error {
	f.pingCalls++
	return f.pingErr
}

// TestPerformHealthCheck_UsesPingNotListTools asserts that a health-check cycle
// on a healthy, connected, non-Docker server probes liveness with a single
// `ping` and does not list tools (SC-001, FR-001).
func TestPerformHealthCheck_UsesPingNotListTools(t *testing.T) {
	mc := newTestClientForHealth(t)
	fake := &fakeProber{}
	mc.healthProbe = fake

	mc.performHealthCheck()

	assert.Equal(t, 1, fake.pingCalls, "health check must issue exactly one ping")
	assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
		"a successful ping must leave the server Ready")
	assert.Equal(t, 0, mc.consecutiveHealthFailures,
		"a successful ping must reset the failure counter")
}

// TestPerformHealthCheck_PingHardErrorFlipsToError asserts a hard connection
// error from the ping probe is classified and flips the server to Error on the
// first occurrence (parity with the previous ListTools-based probe, FR-002).
func TestPerformHealthCheck_PingHardErrorFlipsToError(t *testing.T) {
	mc := newTestClientForHealth(t)
	fake := &fakeProber{pingErr: errors.New("dial tcp 127.0.0.1:65535: connect: connection refused")}
	mc.healthProbe = fake

	mc.performHealthCheck()

	assert.Equal(t, 1, fake.pingCalls, "health check must issue exactly one ping")
	assert.Equal(t, types.StateError, mc.StateManager.GetState(),
		"a hard connection error from ping must flip the server to Error")
}

// TestPerformHealthCheck_PingTransientErrorToleratedBelowThreshold asserts a
// single transient ping failure does not flip the server to Error (flap
// resistance preserved, FR-002).
func TestPerformHealthCheck_PingTransientErrorToleratedBelowThreshold(t *testing.T) {
	mc := newTestClientForHealth(t)
	fake := &fakeProber{pingErr: errors.New("context deadline exceeded")}
	mc.healthProbe = fake

	mc.performHealthCheck()

	assert.Equal(t, 1, fake.pingCalls)
	assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
		"a single transient ping failure must be tolerated below threshold")
	assert.Equal(t, 1, mc.consecutiveHealthFailures)
}
