package launcher

import (
	"context"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitForURL_ImmediatelyBound(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	url := "http://" + ln.Addr().String() + "/mcp"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err = WaitForURL(ctx, url, time.Second)
	assert.NoError(t, err)
	assert.Less(t, time.Since(start), 500*time.Millisecond, "should return quickly when listener is already bound")
}

// refusingDial returns a dial func that fails with ECONNREFUSED-style errors
// until open() reports true, then dials addr for real. It counts attempts.
//
// The tests below use it instead of "reserve a port, close it, rebind later":
// a released ephemeral port can be grabbed by any other process or parallel
// test before the late bind, which made those tests flaky under
// `go test -race ./...`. Here the listener stays bound for the whole test, so
// the port can never be stolen, and "not listening yet" is simulated at the
// dial seam that WaitForURLWithDial exposes.
func refusingDial(addr string, open func() bool, attempts *atomic.Int32) func(ctx context.Context, network, a string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		attempts.Add(1)
		if !open() {
			return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
		}
		return (&net.Dialer{Timeout: defaultDialPerAttempt}).DialContext(ctx, network, addr)
	}
}

func TestWaitForURL_BoundLate(t *testing.T) {
	// The listener is held from the start (so the port cannot be stolen) but
	// dials are refused until 400ms in, i.e. "not listening -> listening".
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	addr := ln.Addr().String()
	url := "http://" + addr + "/mcp"

	openAt := time.Now().Add(400 * time.Millisecond)
	var attempts atomic.Int32
	dial := refusingDial(addr, func() bool { return !time.Now().Before(openAt) }, &attempts)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err = WaitForURLWithDial(ctx, url, 2*time.Second, dial)
	assert.NoError(t, err, "should succeed once the listener binds")
	assert.GreaterOrEqual(t, attempts.Load(), int32(2), "should have retried while the listener was not yet accepting")
}

func TestWaitForURL_NeverBound(t *testing.T) {
	// Dials are refused for the whole test; no real port is involved, so a
	// stray process binding some ephemeral port cannot make this succeed.
	var attempts atomic.Int32
	dial := refusingDial("127.0.0.1:1", func() bool { return false }, &attempts)

	ctx := context.Background()
	start := time.Now()
	err := WaitForURLWithDial(ctx, "http://127.0.0.1:1/mcp", 300*time.Millisecond, dial)
	elapsed := time.Since(start)
	assert.Error(t, err)
	assert.GreaterOrEqual(t, elapsed, 250*time.Millisecond, "should poll for at least the timeout duration")
	assert.Less(t, elapsed, 1500*time.Millisecond, "should give up promptly after timeout")
	assert.GreaterOrEqual(t, attempts.Load(), int32(2), "should keep polling until the timeout")
}

func TestWaitForURL_ContextCanceled(t *testing.T) {
	var attempts atomic.Int32
	dial := refusingDial("127.0.0.1:1", func() bool { return false }, &attempts)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := WaitForURLWithDial(ctx, "http://127.0.0.1:1/mcp", 5*time.Second, dial)
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 1*time.Second, "should observe ctx cancel quickly")
}

func TestWaitForURL_BadURL(t *testing.T) {
	// Inputs WaitForURL rejects at parse time (before it ever dials).
	// A URL with an explicit port + unknown scheme is fine — the user
	// took responsibility for naming the port — so we don't include
	// e.g. ftp://host:21 here.
	tests := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"no host", "http:///mcp"},
		{"unknown scheme without port", "ftp://example.com/foo"},
		{"no scheme no port", "example.com/foo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := WaitForURL(context.Background(), tc.url, time.Second)
			assert.Error(t, err)
		})
	}
}

func TestWaitForURL_InfersDefaultPort(t *testing.T) {
	// We can't actually bind on 80/443 in a test, but we can confirm
	// that an http:// URL without a port is parsed (rather than rejected)
	// by checking it produces a "not reachable" error rather than a
	// parse error within the short timeout window.
	//
	// Use TEST-NET-1 (RFC 5737, 192.0.2.0/24) as the host — it's reserved
	// for documentation and guaranteed non-routable, so the dial fails
	// consistently across OSes. 127.0.0.1:80 is bound on Windows GitHub
	// runners (IIS/WinNAT), which would make this test pass spuriously.
	ctx := context.Background()
	err := WaitForURL(ctx, "http://192.0.2.1/", 100*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable", "default-port inference should reach the polling loop, not fail at parse")
}
