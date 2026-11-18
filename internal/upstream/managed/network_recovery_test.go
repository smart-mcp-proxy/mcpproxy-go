package managed

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
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

func newNetworkRecoveryClient(t *testing.T, rawURL string, edit ...func(*config.ServerConfig)) *Client {
	t.Helper()
	cfg := &config.ServerConfig{Name: "net-recovery", URL: rawURL, Protocol: "http", Enabled: true}
	for _, e := range edit {
		e(cfg)
	}
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
		cfg   func(*config.ServerConfig)
	}{
		{"network failure", func(mc *Client) { mc.StateManager.SetNetworkError(netErr) }, true, nil},
		{"gave up on network failures", func(mc *Client) {
			for i := 0; i < types.MaxConnectionRetries+3; i++ {
				mc.StateManager.SetNetworkError(netErr)
			}
		}, true, nil},
		{"non-network failure", func(mc *Client) { mc.StateManager.SetError(errors.New("unexpected status 500")) }, false, nil},
		{"oauth failure", func(mc *Client) { mc.StateManager.SetOAuthError(errors.New("invalid_grant")) }, false, nil},
		{"awaiting sign-in", func(mc *Client) { mc.StateManager.SetPendingAuth(errors.New("sign in")) }, false, nil},
		{"permanent failure", func(mc *Client) { mc.StateManager.SetTerminalError(errors.New("x"), "MCPX_X") }, false, nil},
		{"user logged out", func(mc *Client) {
			mc.StateManager.SetNetworkError(netErr)
			mc.StateManager.SetUserLoggedOut(true)
		}, false, nil},
		{"rate limited", func(mc *Client) {
			mc.StateManager.SetRetryAfter(time.Now().Add(time.Hour))
			mc.StateManager.SetNetworkError(netErr)
		}, false, nil},
		{"retired instance", func(mc *Client) {
			mc.StateManager.SetNetworkError(netErr)
			mc.retired.Store(true)
		}, false, nil},
		{"disabled server", func(mc *Client) { mc.StateManager.SetNetworkError(netErr) }, false,
			func(c *config.ServerConfig) { c.Enabled = false }},
		// e.g. it connected under an inspection exemption that has since expired
		{"quarantined server", func(mc *Client) { mc.StateManager.SetNetworkError(netErr) }, false,
			func(c *config.ServerConfig) { c.Quarantined = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var edits []func(*config.ServerConfig)
			if tc.cfg != nil {
				edits = append(edits, tc.cfg)
			}
			mc := newNetworkRecoveryClient(t, "https://mcp.example.com/mcp", edits...)
			tc.setup(mc)
			assert.Equal(t, tc.want, mc.NetworkRecoveryEligible(time.Now()))
			if !tc.want {
				assert.False(t, mc.BeginNetworkRecovery(true, "test"), "an ineligible server is never reconnected")
			}
		})
	}
}

// The reconnect a restored network triggers: with resetBackoff the give-up
// counter is cleared, so a server that gave up during a long outage is retried
// on the short ladder again; without it, one attempt and the backoff resumes.
func TestNetworkRecovery_ReconnectsAndResetsTheBackoffOnlyWhenAsked(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	netErr := errors.New("dial tcp: lookup mcp.example: no such host")

	for _, reset := range []bool{true, false} {
		mc := newNetworkRecoveryClient(t, closedLocalURL(t)) // still down: the attempt fails again
		for i := 0; i < types.MaxConnectionRetries+3; i++ {
			mc.StateManager.SetNetworkError(netErr)
		}
		before := mc.GetConnectionInfo().RetryCount

		require.True(t, mc.BeginNetworkRecovery(reset, "test"))
		mc.ReconnectAfterNetworkRecovery(context.Background()) // blocks until the attempt ends

		info := mc.GetConnectionInfo()
		require.Equal(t, types.StateError, info.State)
		require.NotEqual(t, netErr, info.LastError, "the reconnect attempt ran")
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

// Opus r1 MINOR: the backoff clear is atomic with the eligibility check, so a
// connect that started or failed differently in between is never overwritten.
func TestBeginNetworkRecovery_DoesNotOverwriteAStateThatChanged(t *testing.T) {
	mc := newNetworkRecoveryClient(t, "https://mcp.example.com/mcp")
	mc.StateManager.SetNetworkError(errors.New("i/o timeout"))
	mc.StateManager.SetOAuthError(errors.New("invalid_grant")) // landed after the watcher looked
	assert.False(t, mc.StateManager.ClearNetworkBackoff(time.Now()))
	assert.False(t, mc.BeginNetworkRecovery(true, "test"))
	assert.True(t, mc.GetConnectionInfo().IsOAuthError, "the OAuth failure is kept")
}

// A reconnect admitted before the manager shut down does nothing once its
// context is cancelled; one admitted before a Disconnect does nothing after it.
func TestReconnectAfterNetworkRecovery_HonoursShutdownAndDisconnect(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	netErr := errors.New("dial tcp: connect: connection refused")

	mc := newNetworkRecoveryClient(t, closedLocalURL(t))
	mc.StateManager.SetNetworkError(netErr)
	require.True(t, mc.BeginNetworkRecovery(true, "test"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mc.ReconnectAfterNetworkRecovery(ctx)
	assert.Equal(t, netErr, mc.GetConnectionInfo().LastError, "shut down: no attempt")

	mc2 := newNetworkRecoveryClient(t, closedLocalURL(t))
	mc2.StateManager.SetNetworkError(netErr)
	require.True(t, mc2.BeginNetworkRecovery(true, "test"))
	require.NoError(t, mc2.Disconnect())
	mc2.ReconnectAfterNetworkRecovery(context.Background())
	assert.NotEqual(t, types.StateError, mc2.GetConnectionInfo().State, "a disconnected server is not reconnected")
	assert.False(t, mc2.IsConnecting())
}

// Opus r1 MAJOR 1: refreshes that failed while the machine was offline leave a
// transient cooldown on the server's credential; the recovery lifts it so the
// reconnect's token refresh is sent at once.
func TestBeginNetworkRecovery_LiftsTheRefreshCooldown(t *testing.T) {
	mc := newNetworkRecoveryClient(t, "https://mcp.example.com/mcp")
	key := oauth.GenerateServerKey(mc.GetConfig().Name, mc.GetConfig().URL)
	rec := &storage.OAuthTokenRecord{ServerName: key, AccessToken: "expired", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Hour)}
	load := func() (*storage.OAuthTokenRecord, error) { cp := *rec; return &cp, nil }
	var calls atomic.Int32
	req := oauth.RefreshRequest{Key: key, Binding: mc.CredentialBinding(), ServerName: mc.GetConfig().Name, ObservedRefreshToken: "rt",
		Trigger: oauth.RefreshTriggerReactive, Load: load,
		Refresh: func(context.Context, *storage.OAuthTokenRecord) (*mcpclient.Token, error) {
			calls.Add(1)
			return nil, &net.DNSError{Err: "no such host", Name: "auth.example", IsNotFound: true}
		}}
	coord := oauth.DefaultRefreshCoordinator()
	t.Cleanup(func() { coord.Forget(key, mc.CredentialBinding()) })
	_, _, _ = coord.Do(context.Background(), req) // offline: cooldown set
	_, _, err := coord.Do(context.Background(), req)
	require.ErrorIs(t, err, oauth.ErrTokenRefreshTransient)
	require.Equal(t, int32(1), calls.Load(), "the cooldown answers without a request")

	mc.StateManager.SetNetworkError(errors.New("dial tcp: lookup mcp.example.com: no such host"))
	require.True(t, mc.BeginNetworkRecovery(true, "test"))
	_, _, _ = coord.Do(context.Background(), req)
	assert.Equal(t, int32(2), calls.Load(), "after the recovery the refresh is sent again")
}

func TestProbeFor(t *testing.T) {
	noProxy := func(*http.Request) (*url.URL, error) { return nil, nil }
	viaProxy := func(*http.Request) (*url.URL, error) { return url.Parse("http://proxy.corp:3128") }
	viaAuthProxy := func(*http.Request) (*url.URL, error) { return url.Parse("http://alice:s3cret@proxy.corp:3128") }

	cases := []struct {
		raw   string
		proxy func(*http.Request) (*url.URL, error)
		want  NetworkProbe
		ok    bool
	}{
		{"https://mcp.atlassian.com/v2/mcp?tools=all", noProxy, NetworkProbe{Addr: "mcp.atlassian.com:443"}, true},
		{"http://127.0.0.1:8931/mcp", noProxy, NetworkProbe{Addr: "127.0.0.1:8931"}, true},
		{"http://[::1]/sse", noProxy, NetworkProbe{Addr: "[::1]:80"}, true},
		{"https://user:secret@mcp.example.com:8443/mcp", noProxy, NetworkProbe{Addr: "mcp.example.com:8443"}, true},
		// Through a proxy an https upstream is probed with CONNECT, so a proxy
		// that stays up while the network behind it is down hides nothing.
		{"https://api.githubcopilot.com/mcp/", viaProxy, NetworkProbe{Addr: "proxy.corp:3128", ConnectTo: "api.githubcopilot.com:443"}, true},
		{"https://api.githubcopilot.com/mcp/", viaAuthProxy, NetworkProbe{Addr: "proxy.corp:3128", ConnectTo: "api.githubcopilot.com:443",
			ProxyAuth: "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))}, true},
		{"http://plain.example.com/mcp", viaProxy, NetworkProbe{Addr: "proxy.corp:3128"}, true},
		{"", noProxy, NetworkProbe{}, false},
		{"mcp.example.com", noProxy, NetworkProbe{}, false},
		{"ftp://mcp.example.com", noProxy, NetworkProbe{}, false},
	}
	for _, tc := range cases {
		got, ok := probeFor(tc.raw, tc.proxy)
		assert.Equal(t, tc.ok, ok, tc.raw)
		assert.Equal(t, tc.want, got, tc.raw)
	}
}

func TestNetworkRecoveryProbe_OnlyRemoteServers(t *testing.T) {
	remote := newNetworkRecoveryClient(t, "https://mcp.example.com/mcp")
	probe, ok := remote.NetworkRecoveryProbe()
	assert.True(t, ok)
	assert.Equal(t, "mcp.example.com:443", probe.Addr)

	stdioCfg := &config.ServerConfig{Name: "local", Command: "npx", Args: []string{"some-mcp"}, Protocol: "stdio", Enabled: true}
	stdio, err := NewClient(stdioCfg.Name, stdioCfg, zap.NewNop(), nil, &config.Config{}, nil, nil)
	require.NoError(t, err)
	_, ok = stdio.NetworkRecoveryProbe()
	assert.False(t, ok, "a stdio server has no network path to probe")
}

// A CONNECTED server whose OAuth refresh failed transiently qualifies for a
// refresh-only recovery; a revoked grant, a quarantined or disabled server, or
// a healthy one does not (QA run2 cooldown-503).
func TestNetworkRecovery_ConnectedServerWithStalledRefresh(t *testing.T) {
	failWith := func(mc *Client, err error) {
		key := oauth.GenerateServerKey(mc.GetConfig().Name, mc.GetConfig().URL)
		rec := &storage.OAuthTokenRecord{ServerName: key, AccessToken: "expired", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Hour)}
		coord := oauth.DefaultRefreshCoordinator()
		t.Cleanup(func() { coord.Forget(key, mc.CredentialBinding()) })
		_, _, _ = coord.Do(context.Background(), oauth.RefreshRequest{Key: key, Binding: mc.CredentialBinding(), ServerName: mc.GetConfig().Name,
			ObservedRefreshToken: "rt", Trigger: oauth.RefreshTriggerProactive,
			Load:    func() (*storage.OAuthTokenRecord, error) { cp := *rec; return &cp, nil },
			Refresh: func(context.Context, *storage.OAuthTokenRecord) (*mcpclient.Token, error) { return nil, err }})
	}
	transient := errors.New("token endpoint answered 503")
	revoked := &oauth.RefreshFailure{Class: oauth.RefreshClassInvalidGrant, Message: "revoked", Err: oauth.ErrRefreshFailed}

	ready := func(mc *Client) { mc.StateManager.TransitionTo(types.StateReady) }

	mc := newNetworkRecoveryClient(t, "https://stalled.example.com/mcp")
	ready(mc)
	assert.False(t, mc.NetworkRecoveryEligible(time.Now()), "a healthy connected server has nothing to recover")
	assert.True(t, mc.NetworkRecoveryHealthy())
	failWith(mc, transient)
	assert.True(t, mc.NetworkRecoveryEligible(time.Now()), "connected, refresh failing transiently")
	assert.False(t, mc.NetworkRecoveryHealthy(), "not healthy while the token cannot be refreshed")
	require.True(t, mc.BeginNetworkRecovery(true, "test"))
	assert.Equal(t, types.StateReady, mc.GetConnectionInfo().State, "a refresh recovery leaves the connection alone")

	latched := newNetworkRecoveryClient(t, "https://revoked.example.com/mcp")
	ready(latched)
	failWith(latched, revoked)
	assert.False(t, latched.NetworkRecoveryEligible(time.Now()), "a revoked grant is not retried")

	quarantined := newNetworkRecoveryClient(t, "https://q.example.com/mcp", func(c *config.ServerConfig) { c.Quarantined = true })
	ready(quarantined)
	failWith(quarantined, transient)
	assert.False(t, quarantined.NetworkRecoveryEligible(time.Now()))

	disabled := newNetworkRecoveryClient(t, "https://d.example.com/mcp", func(c *config.ServerConfig) { c.Enabled = false })
	ready(disabled)
	failWith(disabled, transient)
	assert.False(t, disabled.NetworkRecoveryEligible(time.Now()))
}

// Review r4: a live connection that fails on the network (ping, call, list)
// is recorded as a network failure, so the watcher keeps following it.
func TestSetErrorIfCurrentConnection_FlagsNetworkFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"typed dial error", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}, true},
		{"flattened in a token error", errors.New("transport error: token refresh in cooldown (retry after …): dial tcp 10.0.0.1:443: connect: connection refused"), true},
		{"dns", errors.New("dial tcp: lookup mcp.example.com: no such host"), true},
		{"server error", errors.New("unexpected status 500"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mc := newNetworkRecoveryClient(t, "https://mcp.example.com/mcp")
			mc.StateManager.TransitionTo(types.StateReady)
			require.True(t, mc.setErrorIfCurrentConnection(tc.err, mc.connectionEpoch.Load()))
			assert.Equal(t, tc.want, mc.GetConnectionInfo().NetworkFailure)
		})
	}

	stdioCfg := &config.ServerConfig{Name: "local", Command: "npx", Args: []string{"x"}, Protocol: "stdio", Enabled: true}
	stdio, err := NewClient(stdioCfg.Name, stdioCfg, zap.NewNop(), nil, &config.Config{}, nil, nil)
	require.NoError(t, err)
	stdio.StateManager.TransitionTo(types.StateReady)
	require.True(t, stdio.setErrorIfCurrentConnection(errors.New("connection refused"), stdio.connectionEpoch.Load()))
	assert.False(t, stdio.GetConnectionInfo().NetworkFailure, "a stdio server has no network path")
}
