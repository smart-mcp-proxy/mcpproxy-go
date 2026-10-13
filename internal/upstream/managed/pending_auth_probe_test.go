package managed

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// GH #1537: a no-auth streamable-HTTP upstream was parked in Pending Auth for
// good after a network outage. These tests run the full ladder (no
// MCPPROXY_DISABLE_OAUTH) through managed.Client.Connect.

func newLadderClient(t *testing.T, url string) *Client {
	t.Helper()
	cfg := &config.ServerConfig{Name: "gh1537", URL: url, Protocol: "streamable-http", Enabled: true, Created: time.Now()}
	mc, err := NewClient(cfg.Name, cfg, zap.NewNop(), nil, &config.Config{}, nil, secret.NewResolver())
	require.NoError(t, err)
	return mc
}

// refusedAddrWith401 returns a closed loopback address whose port contains
// "401", the digits the substring classifiers misread as an HTTP 401.
func refusedAddrWith401(t *testing.T) string {
	t.Helper()
	for p := 14010; p < 14020; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			continue
		}
		addr := ln.Addr().String()
		require.NoError(t, ln.Close())
		return addr
	}
	t.Skip("no free port in 14010-14019")
	return ""
}

func TestConnect_NetworkErrorWith401InAddressIsPlainError(t *testing.T) {
	addr := refusedAddrWith401(t)
	require.True(t, strings.Contains(addr, "401"))
	mc := newLadderClient(t, "http://"+addr+"/mcp")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.Error(t, mc.Connect(ctx))

	info := mc.StateManager.GetConnectionInfo()
	assert.Equal(t, types.StateError, info.State, "a refused dial is a retryable error, not a sign-in: %v", info.LastError)
	assert.False(t, info.IsOAuthError, "must not ride the 5m-24h OAuth backoff ladder")
	assert.Equal(t, 1, info.RetryCount)
}

// jsonRPCUnauthorized answers initialize with HTTP 200 and a JSON-RPC error
// whose text says "unauthorized": auth-sounding, but not an HTTP 401.
func jsonRPCUnauthorized(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":0,"error":{"code":-32001,"message":"unauthorized"}}`))
}

func TestConnect_UnconfirmedOAuthParkIsProbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(jsonRPCUnauthorized))
	defer srv.Close()
	mc := newLadderClient(t, srv.URL+"/mcp")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.Error(t, mc.Connect(ctx))

	info := mc.StateManager.GetConnectionInfo()
	require.Equal(t, types.StatePendingAuth, info.State, "last error: %v", info.LastError)
	assert.True(t, info.PendingAuthProbe, "no oauth block, no token, no 401: the park is a guess")
	assert.False(t, info.ShouldAutoReconnect(time.Now()), "not re-dialed on the next 30s tick")
	assert.True(t, info.ShouldAutoReconnect(time.Now().Add(types.GaveUpProbeInterval+time.Second)),
		"re-probed once the probe interval elapses")
}

func TestConnect_Real401ParkStaysParked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	mc := newLadderClient(t, srv.URL+"/mcp")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.Error(t, mc.Connect(ctx))

	info := mc.StateManager.GetConnectionInfo()
	require.Equal(t, types.StatePendingAuth, info.State, "last error: %v", info.LastError)
	assert.False(t, info.PendingAuthProbe, "a real 401 confirms the sign-in requirement")
	assert.False(t, info.ShouldAutoReconnect(time.Now().Add(24*time.Hour)), "#1013: confirmed parks are never auto-redialed")
}
