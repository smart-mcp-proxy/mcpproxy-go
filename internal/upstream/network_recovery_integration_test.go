package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// GH #1929 end to end. Real managed clients reach the repo's OAuth/MCP test
// server through a TCP relay that the test switches off and on, standing in
// for the network going away and coming back (a laptop asleep overnight). Down
// closes the relay's listener and every relayed connection, so dials are
// refused — a network failure — exactly as when the route is gone.

// toggleRelay relays TCP from a fixed loopback address to target.
type toggleRelay struct {
	t      *testing.T
	addr   string
	target string

	mu    sync.Mutex
	ln    net.Listener
	conns map[net.Conn]struct{}
}

func startToggleRelay(t *testing.T, target string) *toggleRelay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	r := &toggleRelay{t: t, addr: ln.Addr().String(), target: target, conns: map[net.Conn]struct{}{}}
	r.serve(ln)
	t.Cleanup(r.Down)
	return r
}

func (r *toggleRelay) serve(ln net.Listener) {
	r.mu.Lock()
	r.ln = ln
	r.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go r.relay(c)
		}
	}()
}

func (r *toggleRelay) track(c net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ln == nil {
		return false
	}
	r.conns[c] = struct{}{}
	return true
}

func (r *toggleRelay) relay(in net.Conn) {
	out, err := net.Dial("tcp", r.target)
	if err != nil {
		_ = in.Close()
		return
	}
	if !r.track(in) || !r.track(out) {
		_ = in.Close()
		_ = out.Close()
		return
	}
	go func() { _, _ = io.Copy(out, in); _ = out.Close() }()
	_, _ = io.Copy(in, out)
	_ = in.Close()
}

// Down: the network is gone. New dials are refused and open connections die.
func (r *toggleRelay) Down() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ln != nil {
		_ = r.ln.Close()
		r.ln = nil
	}
	for c := range r.conns {
		_ = c.Close()
	}
	r.conns = map[net.Conn]struct{}{}
}

// Up: the network is back, on the same address.
func (r *toggleRelay) Up() {
	r.t.Helper()
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ {
		if ln, err = net.Listen("tcp", r.addr); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(r.t, err, "re-listen on %s", r.addr)
	r.serve(ln)
}

func (r *toggleRelay) mcpURL(srv *oauthserver.ServerResult) string {
	u, err := url.Parse(srv.MCPURL)
	require.NoError(r.t, err)
	u.Host = r.addr
	return u.String()
}

// mintAccessToken obtains a bearer the test server's /mcp accepts.
func mintAccessToken(t *testing.T, srv *oauthserver.ServerResult) string {
	t.Helper()
	params := url.Values{}
	params.Set("grant_type", "client_credentials")
	params.Set("client_id", srv.ClientID)
	params.Set("client_secret", srv.ClientSecret)
	params.Set("scope", "read")
	resp, err := http.PostForm(srv.TokenEndpoint, params)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var tok oauthserver.TokenResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tok))
	require.NotEmpty(t, tok.AccessToken)
	return tok.AccessToken
}

type recoveryFixture struct {
	srv   *oauthserver.ServerResult
	relay *toggleRelay
	db    *storage.BoltDB
	m     *Manager
}

// newRecoveryFixture starts the test upstream, the relay in front of it and a
// manager whose health loop runs every 5s (the shortest allowed), so a lost
// connection is noticed quickly. watcher replaces the watcher's tuning when
// non-nil; nil keeps the production defaults.
func newRecoveryFixture(t *testing.T, watcher *networkRecoveryConfig) *recoveryFixture {
	t.Helper()
	t.Setenv("HEADLESS", "1")
	t.Setenv("CI", "")
	t.Setenv(networkRecoveryEnv, "")

	srv := oauthserver.Start(t, oauthserver.Options{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	target, err := url.Parse(srv.MCPURL)
	require.NoError(t, err)
	relay := startToggleRelay(t, target.Host)

	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	health := config.Duration(5 * time.Second)
	m := NewManager(zap.NewNop(), &config.Config{HealthCheckInterval: &health}, db, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })
	t.Cleanup(func() { _ = m.DisconnectAll() }) // before the upstream and relay go away (LIFO)
	require.NotNil(t, m.networkRecovery, "the watcher runs by default")
	if watcher != nil {
		m.networkRecovery.setConfig(*watcher)
	}
	return &recoveryFixture{srv: srv, relay: relay, db: db, m: m}
}

// addStaticHeaderServer: Authorization header, no oauth block.
func (f *recoveryFixture) addStaticHeaderServer(t *testing.T, name, bearer string) *managed.Client {
	t.Helper()
	require.NoError(t, f.m.AddServerConfig(name, &config.ServerConfig{
		Name: name, URL: f.relay.mcpURL(f.srv), Protocol: "http", Enabled: true,
		Headers: map[string]string{"Authorization": "Bearer " + bearer},
	}))
	c, ok := f.m.GetClient(name)
	require.True(t, ok)
	return c
}

// addOAuthServer: an oauth block and a stored token, as after a completed sign-in.
func (f *recoveryFixture) addOAuthServer(t *testing.T, name, accessToken string) *managed.Client {
	t.Helper()
	serverURL := f.relay.mcpURL(f.srv)
	require.NoError(t, f.db.SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName:  oauth.GenerateServerKey(name, serverURL),
		DisplayName: name,
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(time.Hour),
		Scopes:      []string{"read"},
		Created:     time.Now(),
	}))
	require.NoError(t, f.m.AddServerConfig(name, &config.ServerConfig{
		Name: name, URL: serverURL, Protocol: "http", Enabled: true,
		OAuth: &config.OAuthConfig{ClientID: f.srv.ClientID, ClientSecret: f.srv.ClientSecret, Scopes: []string{"read"}},
	}))
	c, ok := f.m.GetClient(name)
	require.True(t, ok)
	t.Cleanup(func() {
		if mgr := oauth.GetGlobalCallbackManager(); mgr != nil {
			_ = mgr.StopCallbackServer(name)
		}
	})
	return c
}

func connectAll(t *testing.T, m *Manager, clients ...*managed.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, m.ConnectAll(ctx))
	for _, c := range clients {
		require.Eventually(t, c.IsConnected, 30*time.Second, 50*time.Millisecond,
			"%s must connect first: %+v", c.GetConfig().Name, c.GetConnectionInfo())
	}
}

// waitNetworkFailure waits until the client noticed the outage and failed a
// reconnect on the network (health ping, then a refused dial).
func waitNetworkFailure(t *testing.T, c *managed.Client) {
	t.Helper()
	require.Eventually(t, func() bool {
		info := c.GetConnectionInfo()
		return info.State == types.StateError && info.NetworkFailure
	}, 60*time.Second, 50*time.Millisecond, "%s must fail on the network: %+v", c.GetConfig().Name, c.GetConnectionInfo())
}

// giveUp replays the failures of a long outage — the issue's servers reached
// retry_count 23 and 38 overnight — so the client has given up and its only
// remaining automatic retry is the 30-minute probe. Nothing but the watcher
// can bring it back inside this test (there is no supervisor here).
func giveUp(t *testing.T, c *managed.Client) {
	t.Helper()
	last := c.GetConnectionInfo().LastError
	if last == nil {
		last = errors.New("dial tcp: connect: connection refused")
	}
	for c.GetConnectionInfo().RetryCount < types.MaxConnectionRetries+3 {
		c.StateManager.SetNetworkError(last)
	}
	info := c.GetConnectionInfo()
	require.True(t, info.GaveUp)
	require.False(t, c.ShouldRetry(), "the normal ladder will not retry it")
}

// waitRecovered waits for Ready and returns how long it took from since.
func waitRecovered(t *testing.T, c *managed.Client, since time.Time, within time.Duration) time.Duration {
	t.Helper()
	require.Eventually(t, c.IsConnected, within, 25*time.Millisecond,
		"%s must recover without manual intervention: %+v", c.GetConfig().Name, c.GetConnectionInfo())
	return time.Since(since)
}

func callGetTime(t *testing.T, c *managed.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, "get_time", nil)
	require.NoError(t, err, "%s: a tool call must work after recovery", c.GetConfig().Name)
	require.NotNil(t, res)
	assert.False(t, res.IsError)
}

// The issue's scenario with the production watcher settings: both a
// static-header and an OAuth upstream lose the network, give up during the
// long outage, and must be Ready again within 60s of the network returning —
// with no restart, no sign-in and no supervisor.
func TestNetworkRecovery_StaticHeaderAndOAuthRecoverWithin60s(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the production probe cadence (~30s)")
	}
	f := newRecoveryFixture(t, nil)
	tok := mintAccessToken(t, f.srv)
	static := f.addStaticHeaderServer(t, "static-header", tok)
	oauthC := f.addOAuthServer(t, "oauth-upstream", mintAccessToken(t, f.srv))
	connectAll(t, f.m, static, oauthC)

	f.relay.Down()
	waitNetworkFailure(t, static)
	waitNetworkFailure(t, oauthC)
	giveUp(t, static)
	giveUp(t, oauthC)
	// Let the watcher see the outage (two failed probes at the 10s cadence).
	time.Sleep(25 * time.Second)

	restored := time.Now()
	f.relay.Up()
	dStatic := waitRecovered(t, static, restored, 60*time.Second)
	dOAuth := waitRecovered(t, oauthC, restored, 60*time.Second)
	t.Logf("recovered after the network returned: static-header %s, oauth %s", dStatic.Round(time.Millisecond), dOAuth.Round(time.Millisecond))
	assert.Less(t, dStatic, 60*time.Second)
	assert.Less(t, dOAuth, 60*time.Second)

	callGetTime(t, static)
	callGetTime(t, oauthC)
	for _, c := range []*managed.Client{static, oauthC} {
		info := c.GetConnectionInfo()
		assert.Zero(t, info.RetryCount, "%s: the ladder starts over", c.GetConfig().Name)
		assert.False(t, info.GaveUp)
	}
}

func fastRecoveryConfig() networkRecoveryConfig {
	return networkRecoveryConfig{
		Interval:          250 * time.Millisecond,
		SlowInterval:      time.Second,
		SlowAfter:         40,
		ProbeTimeout:      time.Second,
		WakeSlack:         time.Hour, // no wake detection under a loaded test runner
		OutageProbes:      2,
		MinTriggerSpacing: time.Second,
	}
}

// Repeated network changes: the network drops and returns several times. Every
// return brings both servers back, the watcher reconnects each server exactly
// once per return, and it starts nothing while the network is down.
func TestNetworkRecovery_RepeatedNetworkChanges(t *testing.T) {
	cfg := fastRecoveryConfig()
	f := newRecoveryFixture(t, &cfg)
	static := f.addStaticHeaderServer(t, "static-header", mintAccessToken(t, f.srv))
	oauthC := f.addOAuthServer(t, "oauth-upstream", mintAccessToken(t, f.srv))
	connectAll(t, f.m, static, oauthC)

	const cycles = 3
	for cycle := 1; cycle <= cycles; cycle++ {
		f.relay.Down()
		waitNetworkFailure(t, static)
		waitNetworkFailure(t, oauthC)
		if cycle%2 == 0 {
			giveUp(t, static) // alternate a short outage with an overnight one
			giveUp(t, oauthC)
		}
		time.Sleep(4 * cfg.Interval) // probes fail
		before := f.m.networkRecovery.triggers()

		restored := time.Now()
		f.relay.Up()
		dStatic := waitRecovered(t, static, restored, 60*time.Second)
		dOAuth := waitRecovered(t, oauthC, restored, 60*time.Second)
		t.Logf("cycle %d: static-header %s, oauth %s", cycle, dStatic.Round(time.Millisecond), dOAuth.Round(time.Millisecond))

		after := f.m.networkRecovery.triggers()
		assert.LessOrEqual(t, after-before, 2, "cycle %d: at most one watcher reconnect per server per return", cycle)
		callGetTime(t, static)
		callGetTime(t, oauthC)
	}

	// A steady network after the last return: the watcher starts nothing more.
	settled := f.m.networkRecovery.triggers()
	time.Sleep(10 * cfg.Interval)
	assert.Equal(t, settled, f.m.networkRecovery.triggers())
}

// Invalid credentials keep their own handling. A static header the upstream
// rejects is not a network failure: after the network returns the watcher
// reconnects it once, the upstream answers 401 again, and the server is left
// on its normal path — awaiting sign-in here, since the upstream offers OAuth —
// with no further watcher reconnects.
func TestNetworkRecovery_InvalidCredentialIsNotRetriedAsANetworkFailure(t *testing.T) {
	cfg := fastRecoveryConfig()
	f := newRecoveryFixture(t, &cfg)
	bad := f.addStaticHeaderServer(t, "bad-static-header", "not-a-valid-token")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = f.m.ConnectAll(ctx)
	require.Eventually(t, func() bool { return bad.GetConnectionInfo().State == types.StatePendingAuth },
		30*time.Second, 50*time.Millisecond, "a rejected header parks awaiting sign-in: %+v", bad.GetConnectionInfo())

	// The network drops and returns: a parked server is not the watcher's.
	f.relay.Down()
	time.Sleep(8 * cfg.Interval)
	f.relay.Up()
	time.Sleep(8 * cfg.Interval)
	assert.Zero(t, f.m.networkRecovery.triggers(), "a server awaiting sign-in is never reconnected by the watcher")
	assert.Equal(t, types.StatePendingAuth, bad.GetConnectionInfo().State)

	// Now the same server after a network failure: it was retried on the
	// network (as after a restart during an outage), so it is eligible.
	f.relay.Down()
	bad.StateManager.SetNetworkError(errors.New("dial tcp: connect: connection refused"))
	time.Sleep(4 * cfg.Interval)
	f.relay.Up()
	require.Eventually(t, func() bool { return f.m.networkRecovery.triggers() == 1 }, 15*time.Second, 25*time.Millisecond,
		"the return of the network reconnects it once")
	require.Eventually(t, func() bool {
		info := bad.GetConnectionInfo()
		return info.State == types.StatePendingAuth && !info.NetworkFailure
	}, 30*time.Second, 25*time.Millisecond, "the upstream rejects the header again: back to awaiting sign-in, %+v", bad.GetConnectionInfo())

	time.Sleep(12 * cfg.Interval)
	assert.Equal(t, 1, f.m.networkRecovery.triggers(), "no reconnect loop on a rejected credential")
}

// The OAuth flavour: a stored token the upstream rejects (revoked). After the
// network returns the watcher reconnects the server once, the rejection sends
// it down the OAuth path (sign-in or the OAuth backoff), and the watcher leaves
// it there.
func TestNetworkRecovery_RejectedOAuthTokenIsNotRetriedAsANetworkFailure(t *testing.T) {
	cfg := fastRecoveryConfig()
	f := newRecoveryFixture(t, &cfg)
	revoked := f.addOAuthServer(t, "revoked-oauth", "revoked-access-token")

	revoked.StateManager.SetNetworkError(errors.New("dial tcp: lookup mcp.example: no such host"))
	f.relay.Down()
	time.Sleep(4 * cfg.Interval)
	f.relay.Up()
	require.Eventually(t, func() bool { return f.m.networkRecovery.triggers() == 1 }, 15*time.Second, 25*time.Millisecond)
	require.Eventually(t, func() bool {
		info := revoked.GetConnectionInfo()
		return !revoked.IsConnecting() && !info.NetworkFailure &&
			(info.State == types.StatePendingAuth || (info.State == types.StateError && info.IsOAuthError))
	}, 30*time.Second, 25*time.Millisecond, "a rejected token takes the OAuth path: %+v", revoked.GetConnectionInfo())
	assert.False(t, revoked.IsConnected())

	time.Sleep(12 * cfg.Interval)
	assert.Equal(t, 1, f.m.networkRecovery.triggers(), "a credential failure is not retried by the watcher")
}
