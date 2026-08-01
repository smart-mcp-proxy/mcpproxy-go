package managed

import (
	"runtime"
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

// settledGoroutines polls runtime.NumGoroutine until it stops changing, so the
// assertions below compare stable counts rather than racing the scheduler.
func settledGoroutines() int {
	prev := -1
	for i := 0; i < 100; i++ {
		runtime.Gosched()
		time.Sleep(2 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
	}
	return runtime.NumGoroutine()
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

	base := settledGoroutines()

	// Simulates Connect() → reconnect → reconnect on one managed client.
	mc.startBackgroundMonitoring()
	mc.startBackgroundMonitoring()
	mc.startBackgroundMonitoring()

	assert.Equal(t, 1, settledGoroutines()-base,
		"repeated startBackgroundMonitoring must run exactly one health-check goroutine")

	mc.stopBackgroundMonitoring()

	assert.Equal(t, base, settledGoroutines(),
		"stopBackgroundMonitoring must leave no health-check goroutine behind")
}
