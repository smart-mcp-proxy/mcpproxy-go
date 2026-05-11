package launcher

import (
	"context"
	"net"
	"sync"
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

func TestWaitForURL_BoundLate(t *testing.T) {
	// Reserve a port, close it, then re-bind after a delay so we can
	// give WaitForURL a stable address to poll while it isn't yet
	// listening.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := probe.Addr().String()
	probe.Close()

	url := "http://" + addr + "/mcp"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(400 * time.Millisecond)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Errorf("late bind failed: %v", err)
			return
		}
		defer ln.Close()
		// Hold the listener briefly so WaitForURL has a chance to dial it.
		time.Sleep(500 * time.Millisecond)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err = WaitForURL(ctx, url, 2*time.Second)
	assert.NoError(t, err, "should succeed once the listener binds")

	wg.Wait()
}

func TestWaitForURL_NeverBound(t *testing.T) {
	// Reserve and release a port so we know nothing is listening on it.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := probe.Addr().String()
	probe.Close()

	url := "http://" + addr + "/mcp"

	ctx := context.Background()
	start := time.Now()
	err = WaitForURL(ctx, url, 300*time.Millisecond)
	elapsed := time.Since(start)
	assert.Error(t, err)
	assert.GreaterOrEqual(t, elapsed, 250*time.Millisecond, "should poll for at least the timeout duration")
	assert.Less(t, elapsed, 1500*time.Millisecond, "should give up promptly after timeout")
}

func TestWaitForURL_ContextCanceled(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := probe.Addr().String()
	probe.Close()

	url := "http://" + addr + "/mcp"

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err = WaitForURL(ctx, url, 5*time.Second)
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
	ctx := context.Background()
	err := WaitForURL(ctx, "http://127.0.0.1/", 100*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable", "default-port inference should reach the polling loop, not fail at parse")
}
