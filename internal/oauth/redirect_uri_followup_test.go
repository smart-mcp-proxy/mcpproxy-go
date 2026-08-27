package oauth

import (
	"fmt"
	"net"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// observedLogger returns a real logger plus the records it captured, and
// restores the global callback manager's logger afterwards (it is process-wide).
func observedLogger(t *testing.T) (*zap.Logger, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	mgr := GetGlobalCallbackManager()
	mgr.mu.RLock()
	previous := mgr.logger
	mgr.mu.RUnlock()
	t.Cleanup(func() {
		mgr.mu.Lock()
		mgr.logger = previous
		mgr.mu.Unlock()
	})
	return logger, logs
}

func reserveIPv6LoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("host has no usable IPv6 loopback: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

// --- D1: an ::1 pin must actually be bound on ::1 -------------------------

// TestPinnedIPv6RedirectURI_IsActuallyBound covers D1: ParsePinnedRedirectURI
// accepts "::1" and the URI is sent to the provider verbatim, but the callback
// listener used to be hard-coded to 127.0.0.1. The authorize URL then named a
// host nothing was listening on and the login hung to the callback timeout.
func TestPinnedIPv6RedirectURI_IsActuallyBound(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	port := reserveIPv6LoopbackPort(t)
	pinned := fmt.Sprintf("http://[::1]:%d/oauth/callback", port)

	serverName := "ipv6-pinned-server"
	stopCallbackServer(t, serverName)

	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name:  serverName,
		URL:   upstream.URL + "/mcp",
		OAuth: &config.OAuthConfig{ClientID: "static-client", RedirectURI: pinned},
	}, store)
	require.NotNil(t, oauthConfig)
	assert.Equal(t, pinned, oauthConfig.RedirectURI)

	cb, ok := GetCallbackServer(serverName)
	require.True(t, ok)
	assert.Equal(t, config.LoopbackIPv6Host, cb.BindHost, "an ::1 pin must bind ::1")
	assert.Equal(t, port, cb.Port)

	conn, err := net.Dial("tcp", fmt.Sprintf("[::1]:%d", port))
	require.NoError(t, err, "the pinned ::1 endpoint must accept connections")
	require.NoError(t, conn.Close())
}

// TestDefaultCallbackBinding_StaysIPv4 guards the unpinned default: no config
// change should move the callback server off 127.0.0.1.
func TestDefaultCallbackBinding_StaysIPv4(t *testing.T) {
	serverName := "default-binding-server"
	stopCallbackServer(t, serverName)

	cb, err := GetGlobalCallbackManager().StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	assert.Equal(t, config.LoopbackIPv4Host, cb.BindHost)
	assert.Contains(t, cb.RedirectURI, "http://127.0.0.1:")
}

// --- Blocking #1 (D2): the callback surface must reach a real logger ------

// TestCallbackManagerLogsThroughCallerLogger covers the half of D2 that the
// first pass missed: createOAuthConfigInternal got a real logger, but the
// global callback manager kept zap.L() — the NO-OP logger, since
// zap.ReplaceGlobals is never called in this binary. Every record describing
// the callback server was therefore discarded at all levels, including the
// tear-down of a live listener, a dropped waiter and a failed bind on a pinned
// port.
func TestCallbackManagerLogsThroughCallerLogger(t *testing.T) {
	logger, logs := observedLogger(t)

	serverName := "logged-callback-server"
	stopCallbackServer(t, serverName)
	mgr := GetGlobalCallbackManager()

	// Occupy a port, then ask for it: the fallback must be recorded.
	squatter, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer squatter.Close()
	occupied := squatter.Addr().(*net.TCPAddr).Port

	cb, err := mgr.StartCallbackServerOnHost(serverName, CallbackBinding{
		Port:   occupied,
		Pinned: false,
		Logger: logger,
	})
	require.NoError(t, err)
	require.NotEqual(t, occupied, cb.Port, "the occupied port cannot be bound")

	assert.NotEmpty(t, logs.FilterMessage("Preferred port unavailable, falling back to dynamic allocation").All(),
		"the only record of WHY a preferred port could not be bound must be emitted")
	assert.NotEmpty(t, logs.FilterMessage("OAuth callback server started successfully").All(),
		"the started/redirect_uri record must be emitted")

	// A tear-down that drops a parked waiter must be recorded too.
	cb.RegisterState("state-being-dropped")
	require.NoError(t, mgr.StopCallbackServerWithLogger(serverName, logger))
	assert.NotEmpty(t, logs.FilterMessage("Stopped OAuth callback server while flows were still waiting").All(),
		"dropping a live waiter must be visible in the logs")
}

// TestManagerLoggerIsNotNoOpAfterConfigCreation asserts the manager stops being
// blind once a real flow has run through createOAuthConfigInternal.
func TestManagerLoggerIsNotNoOpAfterConfigCreation(t *testing.T) {
	logger, _ := observedLogger(t)
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "manager-logger-server"
	stopCallbackServer(t, serverName)

	_, err := CreateOAuthConfigWithLogger(&config.ServerConfig{
		Name:  serverName,
		URL:   upstream.URL + "/mcp",
		OAuth: &config.OAuthConfig{ClientID: "static-client"},
	}, store, logger)
	require.NoError(t, err)

	mgr := GetGlobalCallbackManager()
	mgr.mu.RLock()
	got := mgr.logger
	mgr.mu.RUnlock()
	require.NotNil(t, got)
	assert.NotEqual(t, zap.NewNop().Core(), got.Core(),
		"the callback manager must not still be writing into the no-op logger")
}

// TestOAuthLoggerNameAppliedOnce guards against the "oauth.oauth" logger name,
// which breaks every filter written against the documented name.
func TestOAuthLoggerNameAppliedOnce(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "logger-name-server"
	stopCallbackServer(t, serverName)
	_, restore := swapManagerLogger(t)
	defer restore()

	_, err := CreateOAuthConfigWithLogger(&config.ServerConfig{
		Name:  serverName,
		URL:   upstream.URL + "/mcp",
		OAuth: &config.OAuthConfig{ClientID: "static-client"},
	}, store, zap.New(core))
	require.NoError(t, err)

	entries := logs.All()
	require.NotEmpty(t, entries)
	// Only two names are legitimate: the package logger and the callback
	// sub-logger derived from it. "oauth.oauth" means namedOAuthLogger ran twice.
	allowed := map[string]bool{
		oauthLoggerName: true,
		oauthLoggerName + "." + oauthCallbackLoggerName: true,
	}
	for _, e := range entries {
		assert.True(t, allowed[e.LoggerName],
			"unexpected logger name %q — namedOAuthLogger must be idempotent", e.LoggerName)
	}
}

func swapManagerLogger(t *testing.T) (*zap.Logger, func()) {
	t.Helper()
	mgr := GetGlobalCallbackManager()
	mgr.mu.RLock()
	previous := mgr.logger
	mgr.mu.RUnlock()
	return previous, func() {
		mgr.mu.Lock()
		mgr.logger = previous
		mgr.mu.Unlock()
	}
}

// --- Blocking #2: a live waiter must survive a binding mismatch -----------

// TestCachedCallbackServerWithLiveWaiterIsNotReplaced covers the regression the
// first pass introduced: an unconditional "replace on binding mismatch" guard
// tore down a listener a flow was parked on. Reachable with a static client_id
// whose stored callback port is occupied by another process — attempt 1 falls
// back to a dynamic port, attempt 2 still prefers the stored port, and the
// server could never finish a login while that port stayed occupied.
func TestCachedCallbackServerWithLiveWaiterIsNotReplaced(t *testing.T) {
	logger, _ := observedLogger(t)
	serverName := "live-waiter-server"
	stopCallbackServer(t, serverName)
	mgr := GetGlobalCallbackManager()

	first, err := mgr.StartCallbackServerOnHost(serverName, CallbackBinding{Logger: logger})
	require.NoError(t, err)
	portA := first.Port
	first.RegisterState("live-state-123")

	portB := reserveLoopbackPort(t)
	require.NotEqual(t, portA, portB)

	second, err := mgr.StartCallbackServerOnHost(serverName, CallbackBinding{
		Port:   portB,
		Pinned: false,
		Logger: logger,
	})
	require.NoError(t, err)

	assert.Same(t, first, second, "the cached server must be reused, not replaced")
	assert.True(t, first.HasState("live-state-123"), "the parked waiter must survive")

	conn, dialErr := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", portA))
	require.NoError(t, dialErr, "the live listener must still accept connections")
	require.NoError(t, conn.Close())
}

// TestStaleCallbackServerWithoutWaitersIsReplaced covers the case the guard was
// written for (D4): a timed-out flow leaves a cached server on the old port, and
// a later pinned reconnect must get the pinned port rather than the stale one.
func TestStaleCallbackServerWithoutWaitersIsReplaced(t *testing.T) {
	logger, _ := observedLogger(t)
	serverName := "stale-server"
	stopCallbackServer(t, serverName)
	mgr := GetGlobalCallbackManager()

	stale, err := mgr.StartCallbackServerOnHost(serverName, CallbackBinding{Logger: logger})
	require.NoError(t, err)
	require.Zero(t, stale.waiterCount())

	pinnedPort := reserveLoopbackPort(t)
	require.NotEqual(t, stale.Port, pinnedPort)

	fresh, err := mgr.StartCallbackServerOnHost(serverName, CallbackBinding{
		Port:   pinnedPort,
		Pinned: true,
		Logger: logger,
	})
	require.NoError(t, err)
	assert.Equal(t, pinnedPort, fresh.Port, "a pinned reconnect must not inherit the stale port")
	assert.NotSame(t, stale, fresh)
}

// --- Blocking #3 / D5: DCR hygiene must not be skipped by a pin -----------

// TestPinnedPortClearsDCRCredentialsRegisteredForAnotherPort covers the
// feature's most likely upgrade path: log in unpinned (DCR registers port A),
// then add oauth.redirect_uri with port B because the provider rejected the
// moving port. Reusing the port-A registration under a port-B redirect_uri is a
// permanent redirect_uri_mismatch that no retry clears.
func TestPinnedPortClearsDCRCredentialsRegisteredForAnotherPort(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "stale-dcr-under-pin"
	stopCallbackServer(t, serverName)
	serverURL := upstream.URL + "/mcp"
	serverKey := GenerateServerKey(serverName, serverURL)

	storedPort := reserveLoopbackPort(t)
	pinnedPort := reserveLoopbackPort(t)
	require.NotEqual(t, storedPort, pinnedPort)
	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "dcr-registered-for-storedPort", "secret", storedPort))

	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name: serverName,
		URL:  serverURL,
		OAuth: &config.OAuthConfig{
			RedirectURI: fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", pinnedPort),
		},
	}, store)
	require.NotNil(t, oauthConfig)

	assert.Empty(t, oauthConfig.ClientID,
		"a DCR client registered for another port must not be shipped with the new pinned redirect_uri")
	storedClientID, _, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Empty(t, storedClientID, "the stale DCR record must be cleared so the next login re-registers")
}

// TestPinnedPortClearsLegacyDCRCredentials covers D5 proper: the pinned branch
// used to short-circuit the storage read entirely, so the Spec 022 legacy
// cleanup never ran for a pinned server.
func TestPinnedPortClearsLegacyDCRCredentials(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "legacy-dcr-under-pin"
	stopCallbackServer(t, serverName)
	serverURL := upstream.URL + "/mcp"
	serverKey := GenerateServerKey(serverName, serverURL)

	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "dcr-old", "secret", 0))

	pinnedPort := reserveLoopbackPort(t)
	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name: serverName,
		URL:  serverURL,
		OAuth: &config.OAuthConfig{
			RedirectURI: fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", pinnedPort),
		},
	}, store)
	require.NotNil(t, oauthConfig)

	storedClientID, _, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Empty(t, storedClientID, "legacy DCR credentials must be cleared even when a port is pinned")
}

// TestPinnedPortKeepsStaticCredentials guards the other side: a static
// client_id is operator-supplied and can never be re-registered, so a pin that
// disagrees with the stored port must not wipe it.
func TestPinnedPortKeepsStaticCredentials(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "static-under-pin"
	stopCallbackServer(t, serverName)
	serverURL := upstream.URL + "/mcp"
	serverKey := GenerateServerKey(serverName, serverURL)

	storedPort := reserveLoopbackPort(t)
	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "static-client", "static-secret", storedPort))

	pinnedPort := reserveLoopbackPort(t)
	require.NotEqual(t, storedPort, pinnedPort)

	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name: serverName,
		URL:  serverURL,
		OAuth: &config.OAuthConfig{
			ClientID:    "static-client",
			RedirectURI: fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", pinnedPort),
		},
	}, store)
	require.NotNil(t, oauthConfig)
	assert.Equal(t, "static-client", oauthConfig.ClientID)

	storedClientID, _, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Equal(t, "static-client", storedClientID, "static credentials must never be cleared")
}

// --- D3: the failure names redirect_uri ------------------------------------

// TestMalformedPinReturnsActionableError covers D3: the caller's message
// ("failed to create OAuth config - server may not support OAuth") never
// mentioned redirect_uri, which made a typo a permanent undiagnosable failure.
func TestMalformedPinReturnsActionableError(t *testing.T) {
	logger, logs := observedLogger(t)
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "bad-pin-server"
	stopCallbackServer(t, serverName)

	cfg, err := CreateOAuthConfigWithLogger(&config.ServerConfig{
		Name:  serverName,
		URL:   upstream.URL + "/mcp",
		OAuth: &config.OAuthConfig{ClientID: "static", RedirectURI: "https://evil.example.com/nope"},
	}, store, logger)

	assert.Nil(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oauth.redirect_uri")
	assert.Contains(t, err.Error(), serverName)

	assert.NotEmpty(t, logs.FilterMessageSnippet("Invalid oauth.redirect_uri").All(),
		"the diagnostic must reach a real logger, not zap.L()")

	_, ok := GetCallbackServer(serverName)
	assert.False(t, ok, "no callback server should be started for a malformed pin")
}
