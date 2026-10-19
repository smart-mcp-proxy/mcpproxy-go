package managed

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	servertest "github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// blockingProber is a livenessProber whose Ping blocks until release is
// closed, so a test can hold the health loop inside a probe the way a slow
// upstream (or a reconnect waiting on mc.mu) does in production.
type blockingProber struct {
	entered chan struct{} // receives one token when a Ping starts
	release chan struct{} // Ping returns once this is closed
}

func newBlockingProber() *blockingProber {
	return &blockingProber{entered: make(chan struct{}, 64), release: make(chan struct{})}
}

func (p *blockingProber) Ping(_ context.Context) error {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-p.release
	return nil
}

// healthLoopGoroutines counts goroutines currently running the health loop.
// It reads the stacks instead of runtime.NumGoroutine, so helper goroutines
// (the stop-wait waiter, timers) do not blur the count.
func healthLoopGoroutines() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, ").backgroundHealthCheck(") {
			count++
		}
	}
	return count
}

// settledHealthLoops polls healthLoopGoroutines until the count stops changing.
func settledHealthLoops() int {
	prev := -1
	for i := 0; i < 100; i++ {
		time.Sleep(5 * time.Millisecond)
		n := healthLoopGoroutines()
		if n == prev {
			return n
		}
		prev = n
	}
	return healthLoopGoroutines()
}

// TestBackgroundMonitoring_LoopStuckInProbeDoesNotSurviveStop pins the second
// leak path next to #947. stopBackgroundMonitoring waits at most 1s for the
// loop, then swaps mc.stopMonitoring for a fresh channel. The loop used to
// re-read that field on every iteration, so a loop that was still inside a
// probe (or inside tryReconnect waiting for mc.mu, which Disconnect holds)
// when the wait timed out never saw its own stop: it came back, selected on
// the NEW channel and kept probing next to the loop the next Connect started.
func TestBackgroundMonitoring_LoopStuckInProbeDoesNotSurviveStop(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.stopMonitoring = make(chan struct{})
	prober := newBlockingProber()
	mc.healthProbe = prober
	mc.SetGlobalConfig(&config.Config{})
	cfg := mc.GetConfig()
	d := config.Duration(10 * time.Millisecond)
	cfg.HealthCheckInterval = &d
	mc.SetConfig(cfg)

	base := settledHealthLoops()

	mc.startBackgroundMonitoring()
	select {
	case <-prober.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("health loop never probed")
	}

	// Disconnect: the loop is stuck in Ping, so the 1s stop wait times out.
	mc.stopBackgroundMonitoring()
	// A later Connect starts monitoring again.
	mc.startBackgroundMonitoring()
	// The slow probe finally returns.
	close(prober.release)

	assert.Equal(t, 1, settledHealthLoops()-base,
		"a loop whose stop timed out must exit once its probe returns, not run next to the new loop")

	mc.stopBackgroundMonitoring()
	assert.Equal(t, 0, settledHealthLoops()-base, "stop must leave no health loop behind")
}

// TestHealthCheckFailureCounter_ConcurrentUse pins that the consecutive
// failure counter is safe for concurrent use. The health loop records
// failures while Connect's resetHealthCheckFailures can run on another
// goroutine (ForceReconnect, TryReconnectSync); with a plain int that is a
// data race (go test -race) and increments can be lost.
func TestHealthCheckFailureCounter_ConcurrentUse(t *testing.T) {
	mc := newTestClientForHealth(t)
	transient := errors.New("ping: context deadline exceeded")

	const perWorker = 2000
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				mc.recordHealthCheckFailure(transient)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 4*perWorker, int(mc.consecutiveHealthFailures.Load()),
		"concurrent failures must all be counted")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < perWorker; i++ {
			mc.resetHealthCheckFailures()
		}
	}()
	for i := 0; i < perWorker; i++ {
		mc.recordHealthCheckFailure(transient)
	}
	<-done

	mc.resetHealthCheckFailures()
	for i := 1; i <= healthCheckFailureThreshold; i++ {
		assert.Equal(t, i == healthCheckFailureThreshold, mc.recordHealthCheckFailure(transient),
			"threshold must trip exactly on failure #%d after a reset", healthCheckFailureThreshold)
	}
}

// newStoppedLoopClient returns a connected client whose health loop has been
// stopped by a Disconnect, plus the stop channel that loop captured. The
// caller then runs what the loop would still run if it had been blocked on
// mc.mu while the Disconnect held it.
func newStoppedLoopClient(t *testing.T, name string) (*Client, <-chan struct{}) {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	upstream := mcpserver.NewMCPServer("stopped-loop", "0.0.1", mcpserver.WithToolCapabilities(true))
	ts := servertest.NewTestStreamableHTTPServer(upstream)
	t.Cleanup(ts.Close)

	cfg := &config.ServerConfig{Name: name, Protocol: "streamable-http", URL: ts.URL, Enabled: true}
	mc, err := NewClient(name, cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mc.Disconnect() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, mc.Connect(ctx))

	mc.mu.Lock()
	mc.startBackgroundMonitoring()
	stop := mc.stopMonitoring
	mc.mu.Unlock()
	return mc, stop
}

// TestTryReconnect_StoppedLoopDoesNotReconnectAfterDisconnect pins that a
// health loop already inside tryReconnect when the user disconnects the
// server does nothing more once it gets mc.mu. It used to wait for mc.mu
// (Disconnect holds it), then tear down and Connect again, bringing back a
// server the user had just disconnected.
func TestTryReconnect_StoppedLoopDoesNotReconnectAfterDisconnect(t *testing.T) {
	mc, stop := newStoppedLoopClient(t, "stopped-loop-teardown")

	// The loop decided to reconnect; the Disconnect wins mc.mu first.
	require.NoError(t, mc.Disconnect())
	mc.tryReconnectFor(stop)

	assert.Equal(t, types.StateDisconnected, mc.GetState(),
		"a reconnect from a stopped health loop must not bring the server back")
	assert.False(t, mc.IsConnected())
}

// TestTryReconnect_DisconnectBetweenTeardownAndConnect covers the second
// window: the Disconnect lands after the loop's teardown and before its
// Connect is admitted. The Connect must see the loop was stopped.
func TestTryReconnect_DisconnectBetweenTeardownAndConnect(t *testing.T) {
	mc, stop := newStoppedLoopClient(t, "stopped-loop-connect")

	fired := false
	hook := func() {
		if fired {
			return
		}
		fired = true
		require.NoError(t, mc.Disconnect())
	}
	mc.reconnectAfterTeardown.Store(&hook)
	t.Cleanup(func() { mc.reconnectAfterTeardown.Store(nil) })

	mc.tryReconnectFor(stop)

	require.True(t, fired, "seam did not run")
	assert.Equal(t, types.StateDisconnected, mc.GetState(),
		"a Connect from a stopped health loop must not bring the server back")
}

// stopWaiterGoroutines counts goroutines still waiting for a stopped health
// loop on behalf of stopBackgroundMonitoring.
func stopWaiterGoroutines() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, ").stopBackgroundMonitoring.func") {
			count++
		}
	}
	return count
}

// TestBackgroundMonitoring_StopWaitIsPerLoop pins that a stop whose wait
// timed out waits only for its own loop. All loops used to share one
// WaitGroup: the timed-out waiter then also waited for the loop the next
// Connect started (leaking for that loop's whole life), and a later
// startBackgroundMonitoring's Add(1) racing that waiter's Wait could panic
// with "WaitGroup is reused before previous Wait has returned".
func TestBackgroundMonitoring_StopWaitIsPerLoop(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.stopMonitoring = make(chan struct{})
	prober := newBlockingProber()
	mc.healthProbe = prober
	mc.SetGlobalConfig(&config.Config{})
	cfg := mc.GetConfig()
	d := config.Duration(10 * time.Millisecond)
	cfg.HealthCheckInterval = &d
	mc.SetConfig(cfg)

	baseWaiters := stopWaiterGoroutines()

	mc.startBackgroundMonitoring()
	select {
	case <-prober.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("health loop never probed")
	}
	mc.stopBackgroundMonitoring() // times out: the loop is stuck in Ping

	// The next loop gets a probe that never returns until the test ends.
	next := newBlockingProber()
	mc.healthProbe = next
	mc.startBackgroundMonitoring()
	t.Cleanup(func() {
		close(next.release)
		mc.stopBackgroundMonitoring()
	})
	close(prober.release) // the first loop exits

	select {
	case <-next.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("second health loop never probed")
	}
	deadline := time.Now().Add(2 * time.Second)
	for stopWaiterGoroutines() > baseWaiters && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, baseWaiters, stopWaiterGoroutines(),
		"the timed-out stop must stop waiting once its own loop exits, not wait for the next loop")
}

// epochSwappingProber simulates a Connect that lands while a health probe is
// in flight: it opens a new connection generation and resets the failure
// counter, then the old probe fails.
type epochSwappingProber struct{ mc *Client }

func (p epochSwappingProber) Ping(_ context.Context) error {
	p.mc.epochMu.Lock()
	p.mc.connectionEpoch.Store(nextConnectionEpoch())
	p.mc.resetHealthCheckFailures()
	p.mc.epochMu.Unlock()
	return errors.New("ping: context deadline exceeded")
}

// TestHealthCheck_StaleProbeFailureDoesNotCountOnNewConnection pins that a
// probe from the previous connection that fails after Connect's reset adds
// no failure debt to the new connection.
func TestHealthCheck_StaleProbeFailureDoesNotCountOnNewConnection(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.connectionEpoch.Store(nextConnectionEpoch())
	mc.healthProbe = epochSwappingProber{mc: mc}

	mc.performHealthCheck()

	assert.Equal(t, int32(0), mc.consecutiveHealthFailures.Load(),
		"a failure of a probe on the previous connection must not count against the new one")
	assert.True(t, mc.IsConnected())
}
