package managed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// fakeToolCaller injects a canned tools/call outcome in place of the concrete
// core client, so the managed-side error classification (GH #965) can be
// exercised without a live upstream.
type fakeToolCaller struct {
	mu     sync.Mutex
	calls  int
	err    error
	result *mcp.CallToolResult
}

func (f *fakeToolCaller) CallTool(_ context.Context, _ string, _ map[string]interface{}) (*mcp.CallToolResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.result, f.err
}

func (f *fakeToolCaller) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// recordingProber is a livenessProber that counts pings, can block until
// released (to test the in-flight gate) and signals completion so tests can
// synchronize on the async probe goroutine.
type recordingProber struct {
	mu    sync.Mutex
	calls int
	err   error

	block chan struct{} // when non-nil, Ping blocks until closed
	done  chan struct{} // receives one token per completed Ping
}

func newRecordingProber(err error) *recordingProber {
	return &recordingProber{err: err, done: make(chan struct{}, 8)}
}

func (p *recordingProber) Ping(_ context.Context) error {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()

	if p.block != nil {
		<-p.block
	}
	if p.done != nil {
		select {
		case p.done <- struct{}{}:
		default:
		}
	}
	return p.err
}

func (p *recordingProber) pingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *recordingProber) waitForPing(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the async liveness probe to run")
	}
}

// newTestClientForCallTool builds a Ready managed client with a fake tool
// invoker wired in (mirrors newTestClientForHealth in health_flap_test.go).
func newTestClientForCallTool(t *testing.T, callErr error) (*Client, *fakeToolCaller) {
	t.Helper()
	mc := newTestClientForHealth(t)
	fake := &fakeToolCaller{err: callErr}
	mc.toolInvoker = fake
	return mc, fake
}

// wrappedCanceled reproduces the real core-client shape for a cancellation that
// reaches the managed layer: internal/upstream/core/client.go:475 wraps the
// transport error with %w, so errors.Is(err, context.Canceled) is true AND the
// string contains "context canceled".
func wrappedCanceled() error {
	return fmt.Errorf("CallTool failed for 'search': transport error: %w", context.Canceled)
}

// TestCallTool_CallerCanceledContextDoesNotEvictServer is the GH #965 core
// regression: an HTTP client that hangs up cancels the request context, the
// upstream call returns "context canceled", and the whole server used to be
// evicted for every other client. A caller-scoped cancellation must never
// touch server state.
func TestCallTool_CallerCanceledContextDoesNotEvictServer(t *testing.T) {
	mc, fake := newTestClientForCallTool(t, wrappedCanceled())
	prober := newRecordingProber(nil)
	mc.healthProbe = prober

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller went away before the call returned

	_, err := mc.CallTool(ctx, "search", nil)

	require.Error(t, err)
	assert.Equal(t, 1, fake.callCount())
	assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
		"a caller-canceled tool call must not evict the upstream")
	assert.Equal(t, 0, mc.StateManager.GetConnectionInfo().RetryCount,
		"a caller-canceled tool call must not burn the reconnect budget")

	// No probe is needed when the caller itself canceled.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, prober.pingCount(), "caller cancellation must not fire a liveness probe")
}

// TestCallTool_AmbiguousCancellationProbesAndKeepsReadyWhenAlive covers a
// cancellation surfacing from inside the transport while the caller context is
// still live. That is ambiguous, so the server is probed rather than evicted —
// and a healthy probe leaves it Ready.
func TestCallTool_AmbiguousCancellationProbesAndKeepsReadyWhenAlive(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, wrappedCanceled())
	prober := newRecordingProber(nil)
	mc.healthProbe = prober

	_, err := mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)

	prober.waitForPing(t)

	assert.Equal(t, 1, prober.pingCount(), "an ambiguous cancellation must fire exactly one liveness probe")
	assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
		"a live server must stay Ready after an ambiguous call cancellation")
	assert.Equal(t, 0, mc.StateManager.GetConnectionInfo().RetryCount)
}

// TestCallTool_AmbiguousCancellationEvictsWhenProbeFails verifies the probe
// still catches a genuinely dead server: the deadline-exceeded call error is
// ambiguous, but the follow-up ping proves the transport is gone.
func TestCallTool_AmbiguousCancellationEvictsWhenProbeFails(t *testing.T) {
	callErr := fmt.Errorf(`CallTool failed for 'search': transport error: failed to send request: Post "https://hf.co/mcp": %w`, context.DeadlineExceeded)
	mc, _ := newTestClientForCallTool(t, callErr)
	prober := newRecordingProber(errors.New("dial tcp 127.0.0.1:443: connect: connection refused"))
	mc.healthProbe = prober

	_, err := mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)

	require.Eventually(t, func() bool {
		return mc.StateManager.GetState() == types.StateError
	}, 2*time.Second, 10*time.Millisecond,
		"a failed liveness probe must flip the server to Error")
	assert.Equal(t, 1, prober.pingCount())
}

// TestCallTool_AmbiguousCancellation_TransientProbeFailureDoesNotEvict is the
// Codex round-2 finding: a busy upstream is exactly what produces slow calls and
// ambiguous cancellations, so it must not be evicted just because it also missed
// the 5s liveness ping. Transient probe failures are handed back to the
// background health loop, which owns the consecutive-failure threshold.
func TestCallTool_AmbiguousCancellation_TransientProbeFailureDoesNotEvict(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, wrappedCanceled())
	prober := newRecordingProber(errors.New("context deadline exceeded"))
	mc.healthProbe = prober

	_, err := mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)

	prober.waitForPing(t)

	require.Never(t, func() bool {
		return mc.StateManager.GetState() != types.StateReady
	}, 300*time.Millisecond, 10*time.Millisecond,
		"a transient probe failure must be left to the background health loop, not evict the server")
	assert.Equal(t, 1, prober.pingCount())
}

// TestCallTool_AmbiguousCancellation_NonConnectionProbeErrorDoesNotEvict covers
// a probe that fails for a reason that is not transport evidence at all (e.g.
// an upstream that does not implement MCP `ping`). That is not proof the server
// is gone, so the state machine is left alone.
func TestCallTool_AmbiguousCancellation_NonConnectionProbeErrorDoesNotEvict(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, wrappedCanceled())
	prober := newRecordingProber(errors.New("ping not supported"))
	mc.healthProbe = prober

	_, err := mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)

	prober.waitForPing(t)

	require.Never(t, func() bool {
		return mc.StateManager.GetState() != types.StateReady
	}, 300*time.Millisecond, 10*time.Millisecond,
		"a non-connection probe error must not evict the upstream")
	assert.Equal(t, 1, prober.pingCount())
}

// TestCallTool_StaleProbeAfterReconnectDoesNotEvict covers the detached-goroutine
// race: the server disconnects and reconnects while the probe is still in
// flight, so the probe's verdict describes a session that no longer exists.
// IsConnected() alone cannot tell (the new session is Ready too) — the
// connection epoch is what makes the stale result detectable.
func TestCallTool_StaleProbeAfterReconnectDoesNotEvict(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, wrappedCanceled())
	prober := newRecordingProber(errors.New("dial tcp 127.0.0.1:443: connect: connection refused"))
	prober.block = make(chan struct{})
	mc.healthProbe = prober

	_, err := mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)

	// Wait until the probe is genuinely in flight, then simulate a full
	// disconnect/reconnect cycle completing underneath it.
	require.Eventually(t, func() bool {
		return prober.pingCount() == 1
	}, 2*time.Second, 5*time.Millisecond)
	mc.connectionEpoch.Add(1)

	close(prober.block)
	prober.waitForPing(t)

	require.Never(t, func() bool {
		return mc.StateManager.GetState() != types.StateReady
	}, 300*time.Millisecond, 10*time.Millisecond,
		"a probe failure from a superseded connection generation must not evict the new session")
}

// TestCallTool_StaleProbeAfterDisconnectDoesNotResurrectError covers the
// Disconnect side of the same detached-goroutine race (GH #965 review, round
// 4): the client is deliberately disconnected while the probe is in flight.
// Disconnect bumps the connection epoch before resetting the state machine, so
// the stale hard-failure verdict must be dropped — it must not flip the fresh
// Disconnected state back to Error (which would also burn a retry and emit a
// bogus notification).
func TestCallTool_StaleProbeAfterDisconnectDoesNotResurrectError(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, wrappedCanceled())
	prober := newRecordingProber(errors.New("dial tcp 127.0.0.1:443: connect: connection refused"))
	prober.block = make(chan struct{})
	mc.healthProbe = prober

	_, err := mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)

	require.Eventually(t, func() bool {
		return prober.pingCount() == 1
	}, 2*time.Second, 5*time.Millisecond)

	// Simulate managed Disconnect's teardown ordering (the real method needs a
	// live core client): close the generation under epochMu, then reset state.
	mc.epochMu.Lock()
	mc.connectionEpoch.Add(1)
	mc.epochMu.Unlock()
	mc.StateManager.Reset()

	close(prober.block)
	prober.waitForPing(t)

	require.Never(t, func() bool {
		return mc.StateManager.GetState() == types.StateError
	}, 300*time.Millisecond, 10*time.Millisecond,
		"a stale probe verdict must not resurrect Error on a disconnected client")
}

// TestCallTool_HardConnectionErrorStillEvictsImmediately keeps the existing
// behavior for hard evidence: connection refused is not ambiguous, so the
// server is marked Error right away with no probe round-trip.
func TestCallTool_HardConnectionErrorStillEvictsImmediately(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, errors.New("dial tcp 127.0.0.1:443: connect: connection refused"))
	prober := newRecordingProber(nil)
	mc.healthProbe = prober

	_, err := mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)

	assert.Equal(t, types.StateError, mc.StateManager.GetState(),
		"hard connection evidence must evict immediately")
	assert.Equal(t, 0, prober.pingCount(), "hard evidence needs no liveness probe")
}

// TestCallTool_StrippedCallTimeoutStringDoesNotEvict documents the existing
// call_tool_timeout shape (core/client.go:462 strips the error chain and says
// "timed out", which isConnectionError does not match) — it must keep escaping
// the eviction path.
func TestCallTool_StrippedCallTimeoutStringDoesNotEvict(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, errors.New("CallTool 'search' timed out after 2m0s"))
	prober := newRecordingProber(nil)
	mc.healthProbe = prober

	_, err := mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)

	assert.Equal(t, types.StateReady, mc.StateManager.GetState(),
		"a per-call timeout must not evict the upstream")
	assert.Equal(t, 0, prober.pingCount())
}

// TestCallTool_AmbiguousProbeIsGatedToOneInFlight verifies a burst of canceled
// calls does not stampede the upstream with liveness probes.
func TestCallTool_AmbiguousProbeIsGatedToOneInFlight(t *testing.T) {
	mc, _ := newTestClientForCallTool(t, wrappedCanceled())
	prober := newRecordingProber(nil)
	prober.block = make(chan struct{})
	mc.healthProbe = prober

	_, err := mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)

	// Wait until the first probe is actually in flight before firing the second.
	require.Eventually(t, func() bool {
		return prober.pingCount() == 1
	}, 2*time.Second, 5*time.Millisecond)

	_, err = mc.CallTool(context.Background(), "search", nil)
	require.Error(t, err)

	close(prober.block)
	prober.waitForPing(t)

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, prober.pingCount(),
		"concurrent ambiguous failures must share a single in-flight probe")
	assert.Equal(t, types.StateReady, mc.StateManager.GetState())
}

// TestIsAmbiguousCancellationError covers the classifier that decides between
// "probe first" and "evict now".
func TestIsAmbiguousCancellationError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		ambiguous bool
	}{
		{"nil", nil, false},
		{"wrapped context.Canceled", wrappedCanceled(), true},
		{"wrapped context.DeadlineExceeded", fmt.Errorf("transport error: %w", context.DeadlineExceeded), true},
		{"bare context canceled text", errors.New("CallTool failed: transport error: context canceled"), true},
		{"british context cancelled text", errors.New("failed to send request: context cancelled"), true},
		{"real-world deadline shape", errors.New(`failed to call tool: transport error: failed to send request: Post "https://hf.co/mcp": context deadline exceeded`), true},
		{"http client timeout wrapper", errors.New(`Post "https://hf.co/mcp": net/http: request canceled (Client.Timeout exceeded while awaiting headers): context deadline exceeded`), true},
		{"connection refused", errors.New("dial tcp 127.0.0.1:443: connect: connection refused"), false},
		{"connection reset", errors.New("read tcp: connection reset by peer"), false},
		{"broken pipe", errors.New("write: broken pipe"), false},
		{"stripped call timeout", errors.New("CallTool 'search' timed out after 2m0s"), false},
		{"dial i/o timeout", errors.New("dial tcp 10.0.0.1:443: i/o timeout"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.ambiguous, isAmbiguousCancellationError(tc.err))
		})
	}
}
