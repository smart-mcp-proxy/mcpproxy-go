package managed

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func newTestClientForMonitoring(t *testing.T) *Client {
	t.Helper()
	mc := &Client{
		logger:         zap.NewNop(),
		stopMonitoring: make(chan struct{}),
	}
	mc.SetConfig(&config.ServerConfig{Name: "monitor-server"})
	return mc
}

// healthChecksOf counts the backgroundHealthCheck goroutines running for mc,
// read from the goroutine stacks (the receiver is the first printed argument).
// Counting only this client's goroutines keeps the check independent of
// goroutines other tests leave behind or end while this one measures, which a
// plain runtime.NumGoroutine comparison was not under -shuffle.
func healthChecksOf(mc *Client) int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	return strings.Count(string(buf), fmt.Sprintf("(*Client).backgroundHealthCheck(%p", mc))
}

// settledHealthChecks polls healthChecksOf until it reaches want or the wait
// runs out, so the assertions compare a settled count rather than racing the
// scheduler.
func settledHealthChecks(mc *Client, want int) int {
	n := healthChecksOf(mc)
	for i := 0; i < 250 && n != want; i++ {
		time.Sleep(2 * time.Millisecond)
		n = healthChecksOf(mc)
	}
	return n
}

// TestStartBackgroundMonitoring_IsIdempotent pins the fix for a goroutine leak
// that made an upstream server's health check fire N times per interval.
//
// startBackgroundMonitoring is reached from Connect(), and tryReconnect()
// re-enters Connect() after tearing down only the *core* client — it never
// calls the managed Client's Disconnect(), which is the sole caller of
// stopBackgroundMonitoring(). So every reconnect used to add another
// backgroundHealthCheck goroutine that lived until the process exited.
//
// Each of those goroutines probes liveness with ListTools(), so a server whose
// tool catalog is large turns the leak into sustained traffic: an installation
// observed here had ~16 concurrent tickers for a single configured upstream,
// pushing ~80 kB per probe and accumulating 72 GB of audit rows downstream.
func TestStartBackgroundMonitoring_IsIdempotent(t *testing.T) {
	mc := newTestClientForMonitoring(t)

	assert.Equal(t, 0, healthChecksOf(mc), "no health check before the first start")

	// Simulates Connect() → reconnect → reconnect on one managed client.
	mc.startBackgroundMonitoring()
	mc.startBackgroundMonitoring()
	mc.startBackgroundMonitoring()

	assert.Equal(t, 1, settledHealthChecks(mc, 1),
		"repeated startBackgroundMonitoring must run exactly one health-check goroutine")
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 1, healthChecksOf(mc), "no second health check appears later")

	mc.stopBackgroundMonitoring()

	assert.Equal(t, 0, settledHealthChecks(mc, 0),
		"stopBackgroundMonitoring must leave no health-check goroutine behind")
}
