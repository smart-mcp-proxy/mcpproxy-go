package managed

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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
