package managed

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// closedLocalURL returns an http URL on a loopback port nothing listens on, so
// a dial fails with "connection refused" — a network failure.
func closedLocalURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return "http://" + addr + "/mcp"
}

func newNetworkRecoveryClient(t *testing.T, rawURL string) *Client {
	t.Helper()
	cfg := &config.ServerConfig{Name: "net-recovery", URL: rawURL, Protocol: "http", Enabled: true}
	mc, err := NewClient(cfg.Name, cfg, zap.NewNop(), nil, &config.Config{}, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mc.Disconnect() })
	return mc
}

// GH #1929: a connect that fails before any upstream answered is recorded as a
// network failure, which is what makes the server eligible for a reconnect
// when connectivity returns.
func TestConnect_RefusedDialIsRecordedAsANetworkFailure(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	mc := newNetworkRecoveryClient(t, closedLocalURL(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.Error(t, mc.Connect(ctx))

	info := mc.GetConnectionInfo()
	require.Equal(t, types.StateError, info.State)
	assert.True(t, info.NetworkFailure, "a refused dial is a network failure: %v", info.LastError)
	assert.True(t, mc.NetworkRecoveryEligible(time.Now()))
}

func TestNetworkRecoveryEligible_OnlyForAPlainNetworkFailure(t *testing.T) {
	netErr := errors.New("dial tcp 203.0.113.7:443: connect: connection refused")
	cases := []struct {
		name  string
		setup func(mc *Client)
		want  bool
	}{
		{"network failure", func(mc *Client) { mc.StateManager.SetNetworkError(netErr) }, true},
		{"gave up on network failures", func(mc *Client) {
			for i := 0; i < types.MaxConnectionRetries+3; i++ {
				mc.StateManager.SetNetworkError(netErr)
			}
		}, true},
		{"non-network failure", func(mc *Client) { mc.StateManager.SetError(errors.New("unexpected status 500")) }, false},
		{"oauth failure", func(mc *Client) { mc.StateManager.SetOAuthError(errors.New("invalid_grant")) }, false},
		{"awaiting sign-in", func(mc *Client) { mc.StateManager.SetPendingAuth(errors.New("sign in")) }, false},
		{"permanent failure", func(mc *Client) { mc.StateManager.SetTerminalError(errors.New("x"), "MCPX_X") }, false},
		{"user logged out", func(mc *Client) {
			mc.StateManager.SetNetworkError(netErr)
			mc.StateManager.SetUserLoggedOut(true)
		}, false},
		{"rate limited", func(mc *Client) {
			mc.StateManager.SetRetryAfter(time.Now().Add(time.Hour))
			mc.StateManager.SetNetworkError(netErr)
		}, false},
		{"retired instance", func(mc *Client) {
			mc.StateManager.SetNetworkError(netErr)
			mc.retired.Store(true)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mc := newNetworkRecoveryClient(t, "https://mcp.example.com/mcp")
			tc.setup(mc)
			assert.Equal(t, tc.want, mc.NetworkRecoveryEligible(time.Now()))
			if !tc.want {
				assert.False(t, mc.RecoverFromNetworkOutage(true, "test"), "an ineligible server is never reconnected")
			}
		})
	}
}

// The reconnect a restored network triggers: with resetBackoff the give-up
// counter is cleared, so a server that gave up during a long outage is retried
// on the short ladder again; without it, one attempt and the backoff resumes.
func TestRecoverFromNetworkOutage_ReconnectsAndResetsTheBackoffOnlyWhenAsked(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	netErr := errors.New("dial tcp: lookup mcp.example: no such host")

	for _, reset := range []bool{true, false} {
		mc := newNetworkRecoveryClient(t, closedLocalURL(t)) // still down: the attempt fails again
		for i := 0; i < types.MaxConnectionRetries+3; i++ {
			mc.StateManager.SetNetworkError(netErr)
		}
		before := mc.GetConnectionInfo().RetryCount

		require.True(t, mc.RecoverFromNetworkOutage(reset, "test"))
		require.Eventually(t, func() bool {
			info := mc.GetConnectionInfo()
			return info.State == types.StateError && !mc.IsConnecting() && info.LastError != netErr
		}, 15*time.Second, 20*time.Millisecond, "the reconnect attempt must run")

		info := mc.GetConnectionInfo()
		assert.True(t, info.NetworkFailure)
		if reset {
			assert.Equal(t, 1, info.RetryCount, "reset: the failed attempt starts the ladder over")
			assert.False(t, info.GaveUp)
		} else {
			assert.Equal(t, before+1, info.RetryCount, "no reset: one more attempt on the same ladder")
			assert.True(t, info.GaveUp)
		}
	}
}

func TestProbeAddressFor(t *testing.T) {
	noProxy := func(*http.Request) (*url.URL, error) { return nil, nil }
	viaProxy := func(*http.Request) (*url.URL, error) { return url.Parse("http://proxy.corp:3128") }

	cases := []struct {
		raw   string
		proxy func(*http.Request) (*url.URL, error)
		want  string
		ok    bool
	}{
		{"https://mcp.atlassian.com/v2/mcp?tools=all", noProxy, "mcp.atlassian.com:443", true},
		{"http://127.0.0.1:8931/mcp", noProxy, "127.0.0.1:8931", true},
		{"http://[::1]/sse", noProxy, "[::1]:80", true},
		{"https://user:secret@mcp.example.com:8443/mcp", noProxy, "mcp.example.com:8443", true},
		{"https://api.githubcopilot.com/mcp/", viaProxy, "proxy.corp:3128", true},
		{"", noProxy, "", false},
		{"mcp.example.com", noProxy, "", false},
		{"ftp://mcp.example.com", noProxy, "", false},
	}
	for _, tc := range cases {
		got, ok := probeAddressFor(tc.raw, tc.proxy)
		assert.Equal(t, tc.ok, ok, tc.raw)
		assert.Equal(t, tc.want, got, tc.raw)
	}
}

func TestNetworkRecoveryProbeAddress_OnlyRemoteServers(t *testing.T) {
	remote := newNetworkRecoveryClient(t, "https://mcp.example.com/mcp")
	addr, ok := remote.NetworkRecoveryProbeAddress()
	assert.True(t, ok)
	assert.Equal(t, "mcp.example.com:443", addr)

	stdioCfg := &config.ServerConfig{Name: "local", Command: "npx", Args: []string{"some-mcp"}, Protocol: "stdio", Enabled: true}
	stdio, err := NewClient(stdioCfg.Name, stdioCfg, zap.NewNop(), nil, &config.Config{}, nil, nil)
	require.NoError(t, err)
	_, ok = stdio.NetworkRecoveryProbeAddress()
	assert.False(t, ok, "a stdio server has no network path to probe")
}
