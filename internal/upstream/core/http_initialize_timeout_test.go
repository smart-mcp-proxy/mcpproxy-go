package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// GH #1929: an HTTP upstream that does not answer `initialize` before the
// deadline (a laptop waking with half a network, a stalled route) used to fail
// with the stdio-only text "server did not respond to MCP initialize within 30s
// and produced no stderr output (check that the command starts an MCP server
// and not a help banner)". The rewrite also dropped the typed deadline, so the
// failure was not recognised as a network failure and nothing could tell a
// stalled network apart from a broken server.
func TestHTTPInitializeTimeout_StaysANetworkFailureWithHTTPWording(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true") // the anonymous probe is what is under test

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // never answer initialize
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs first (LIFO): unblock handlers before Close waits on them

	cfg := &config.ServerConfig{Name: "stalled-http", URL: srv.URL + "/mcp", Protocol: "http", Enabled: true}
	c, err := NewClient(cfg.Name, cfg, zap.NewNop(), nil, &config.Config{}, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Disconnect() })

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err = c.Connect(ctx)
	require.Error(t, err)

	assert.True(t, errors.Is(err, context.DeadlineExceeded), "the typed deadline must survive: %v", err)
	assert.True(t, IsNetworkError(err), "a stalled HTTP initialize is a network failure: %v", err)
	msg := err.Error()
	assert.NotContains(t, msg, "stderr", "an HTTP upstream has no stderr to blame")
	assert.NotContains(t, msg, "help banner")
	assert.Contains(t, msg, strings.TrimPrefix(srv.URL, "http://"), "the message names the upstream host")
}
