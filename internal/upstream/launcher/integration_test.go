package launcher

import (
	"bytes"
	"context"
	"net"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestSpawnAndWaitForURL_Integration exercises the full flow that
// connection.go relies on: spawn a child that binds late, then poll the
// configured URL until it accepts a TCP connection.
//
// Uses a `sh` one-liner with a here-doc that exec's a Go program built into
// the test binary at run time — that's brittle. Simpler: shell-side TCP
// trickery via /dev/tcp isn't a listener. We rely on `python3 -c` if
// available (covers Linux/macOS development environments) and skip
// otherwise. The point is to prove Spawn + WaitForURL agree on a real
// listening child.
func TestSpawnAndWaitForURL_Integration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("integration relies on POSIX shell + python")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available in PATH")
	}

	// Pick a free port we'll tell python to bind.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	// Tiny python "server": bind, accept once, hold open. Includes a
	// 250ms pre-bind sleep so WaitForURL has to actually poll, not
	// succeed on its first dial.
	script := `
import socket, time, sys
time.sleep(0.25)
s = socket.socket()
s.bind(('127.0.0.1', ` + itoa(port) + `))
s.listen(8)
try:
    while True:
        c, _ = s.accept()
        c.close()
except KeyboardInterrupt:
    pass
`
	cmd := exec.Command("python3", "-c", script)
	var sink bytes.Buffer

	h, err := Spawn(context.Background(), &Spec{
		Cmd:       cmd,
		LogSink:   &sink,
		Name:      "python-listener",
		StopGrace: 1 * time.Second,
	}, zap.NewNop())
	require.NoError(t, err)
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = h.Stop(stopCtx)
	}()

	url := "http://127.0.0.1:" + itoa(port) + "/mcp"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err = WaitForURL(ctx, url, 3*time.Second)
	elapsed := time.Since(start)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "should have polled while python warmed up")
}

// itoa avoids strconv import for a one-liner test helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
