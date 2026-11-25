package core

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// A client's roots/list_changed can arrive while the upstream session is being
// re-initialized, which rewrites mcp-go's client state. Telling the upstream
// must wait for the re-initialize instead of reading that state concurrently
// (run with -race).
func TestRelay_RootsChangedDuringSessionReinit(t *testing.T) {
	disableOAuthForTest(t)
	srv := httptest.NewServer(mcpserver.NewStreamableHTTPServer(relayUpstream()))
	t.Cleanup(srv.Close)
	cfg := &config.ServerConfig{Name: "relay-reinit", Protocol: "streamable-http", URL: srv.URL, Enabled: true, ClientFeatures: []string{config.ClientFeatureRoots}}
	c, err := NewClient("relay-reinit", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() { defer wg.Done(); c.notifyRootsChanged() }()
		go func() { defer wg.Done(); _ = c.ReinitializeSession(ctx) }()
	}
	wg.Wait()

	// The notifications still reach the upstream, which asks for the roots
	// again each time ("rootsseen" counts how often).
	require.Eventually(t, func() bool { return rootsFetches(c) > 0 }, 10*time.Second, 50*time.Millisecond,
		"roots/list_changed reached the upstream")
}

// A notification that waits behind a session re-initialize is sent once it is
// done: nothing reaches the upstream while the gate is held, and it does after.
func TestRelay_RootsChangedWaitsForSessionReinit(t *testing.T) {
	disableOAuthForTest(t)
	var notified atomic.Int32
	up := mcpserver.NewStreamableHTTPServer(relayUpstream())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte(mcp.MethodNotificationRootsListChanged)) {
				notified.Add(1)
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		up.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg := &config.ServerConfig{Name: "relay-wait", Protocol: "streamable-http", URL: srv.URL, Enabled: true, ClientFeatures: []string{config.ClientFeatureRoots}}
	c, err := NewClient("relay-wait", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	// Shorter than the hold below: the wait for the gate must not count
	// against the time allowed to send.
	c.rootsChangedSendTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })
	require.Zero(t, notified.Load())

	// Hold the gate as a re-initialize does.
	release, err := c.sessionGate.acquireExclusive(ctx)
	require.NoError(t, err)
	c.notifyRootsChanged()
	time.Sleep(300 * time.Millisecond)
	held := notified.Load()
	release()
	require.Zero(t, held, "nothing is sent while the session is re-initialized")
	require.Eventually(t, func() bool { return notified.Load() > 0 }, 10*time.Second, 20*time.Millisecond,
		"the notification is sent after the re-initialize")
	require.Eventually(t, func() bool { return rootsFetches(c) > 0 }, 10*time.Second, 50*time.Millisecond,
		"and the upstream asks for the roots again")
}

// rootsFetches returns how often the upstream asked for roots after a
// roots/list_changed, or -1 when the probe fails. It is safe to call from
// require.Eventually's goroutine.
func rootsFetches(c *Client) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, "rootsseen", nil)
	if err != nil || len(res.Content) != 1 {
		return -1
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(strings.SplitN(tc.Text, ":", 2)[0])
	if err != nil {
		return -1
	}
	return n
}
