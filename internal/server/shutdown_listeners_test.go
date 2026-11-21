package server

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// serverListenerFrames are the goroutines NewServer starts for the lifetime of
// the server. Shutdown must stop both: a server that is shut down and replaced
// in the same process (tests, an in-process restart) otherwise keeps two idle
// goroutines per server for the rest of the process.
var serverListenerFrames = []string{
	"(*Server).forwardRuntimeStatus(",
	"(*Server).listenForRoutingModeRefresh(",
}

// countGoroutinesWithFrame returns how many goroutines have frame on their
// stack.
func countGoroutinesWithFrame(frame string) int {
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
		if strings.Contains(g, frame) {
			count++
		}
	}
	return count
}

func countServerListeners() map[string]int {
	counts := make(map[string]int, len(serverListenerFrames))
	for _, frame := range serverListenerFrames {
		counts[frame] = countGoroutinesWithFrame(frame)
	}
	return counts
}

// TestShutdown_StopsServerListenerGoroutines: Shutdown stops the runtime
// status forwarder and the routing-mode event listener NewServer started, and
// a second Shutdown is still a no-op.
//
// Not parallel: it counts goroutines process-wide, so it runs while no other
// test is creating servers. Servers leaked by earlier tests only ever lower
// the count, so the check compares against the count taken before NewServer.
func TestShutdown_StopsServerListenerGoroutines(t *testing.T) {
	before := countServerListeners()

	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		running := countServerListeners()
		for _, frame := range serverListenerFrames {
			if running[frame] <= before[frame] {
				return false
			}
		}
		return true
	}, 10*time.Second, 20*time.Millisecond, "NewServer starts both listener goroutines")

	require.NoError(t, srv.Shutdown())

	deadline := time.Now().Add(10 * time.Second)
	for {
		left := countServerListeners()
		stopped := true
		for _, frame := range serverListenerFrames {
			if left[frame] > before[frame] {
				stopped = false
			}
		}
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Shutdown left server listener goroutines running: before NewServer %v, 10s after Shutdown %v", before, left)
		}
		time.Sleep(20 * time.Millisecond)
	}

	require.NoError(t, srv.Shutdown(), "a second Shutdown is a no-op")
}
