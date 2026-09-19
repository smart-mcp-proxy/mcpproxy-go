package oauth

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateOAuthConfig_HonorsCustomCallbackPath covers issue #1304: some
// authorization servers (e.g. a self-hosted GitLab instance) publish a single
// shared OAuth application whose registered redirect path an operator cannot
// change - "/callback" instead of mcpproxy's own "/oauth/callback". Before the
// fix, ParseLoopbackRedirectURI rejected any oauth.redirect_uri whose path was
// not exactly "/oauth/callback", so such a server could never connect at all.
func TestCreateOAuthConfig_HonorsCustomCallbackPath(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	port := reserveLoopbackPort(t)
	pinned := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	serverName := "custom-path-server"
	stopCallbackServer(t, serverName)

	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name: serverName,
		URL:  upstream.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			ClientID:    "static-client",
			RedirectURI: pinned,
		},
	}, store)
	require.NotNil(t, oauthConfig, "a pinned redirect_uri with a non-default callback path must be honored")
	assert.Equal(t, pinned, oauthConfig.RedirectURI,
		"the configured redirect_uri must be sent to the provider verbatim, path included")

	callbackServer, ok := GetCallbackServer(serverName)
	require.True(t, ok, "callback server should be running")
	assert.Equal(t, port, callbackServer.Port)
	assert.Equal(t, "/callback", callbackServer.Path)

	// Prove the HTTP mux actually routes the custom path to the callback
	// handler - not just that config creation accepted the value - by
	// simulating the browser redirect the authorization server would send.
	state := "custom-path-state"
	ch := callbackServer.RegisterState(state)
	defer callbackServer.UnregisterState(state)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?state=%s&code=test-code", port, state))
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case params := <-ch:
		assert.Equal(t, "test-code", params["code"])
	default:
		t.Fatal("the callback handler did not deliver params to the registered waiter")
	}
}

// TestParseLoopbackRedirectURI_DefaultsPathWhenAbsent guards a pin with no path
// at all (e.g. "http://127.0.0.1:8080"): it must default to "/" - what an HTTP
// client actually requests for a path-less URL - not to mcpproxy's own
// "/oauth/callback", so the listener matches what the provider will really
// send.
func TestParseLoopbackRedirectURI_DefaultsPathWhenAbsent(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	port := reserveLoopbackPort(t)
	pinned := fmt.Sprintf("http://127.0.0.1:%d", port)

	serverName := "no-path-pinned-server"
	stopCallbackServer(t, serverName)

	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name: serverName,
		URL:  upstream.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			ClientID:    "static-client",
			RedirectURI: pinned,
		},
	}, store)
	require.NotNil(t, oauthConfig)

	callbackServer, ok := GetCallbackServer(serverName)
	require.True(t, ok)
	assert.Equal(t, "/", callbackServer.Path)
}

// TestPinnedHostSpellingChangeClearsDCRCredentials covers a round-3
// cross-model review finding: the DCR hygiene check compared only the decoded
// port and path, so a pin that changes ONLY host spelling ("127.0.0.1" ->
// "localhost", same port and path) was never detected as stale even though
// the provider registered a client_id against the OLD exact string. Now that
// the exact registered string is persisted (round-2 fix), the hygiene check
// compares it directly against the new pin instead of the decoded
// approximation.
func TestPinnedHostSpellingChangeClearsDCRCredentials(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "host-spelling-change-under-pin"
	stopCallbackServer(t, serverName)
	serverURL := upstream.URL + "/mcp"
	serverKey := GenerateServerKey(serverName, serverURL)

	port := reserveLoopbackPort(t)
	oldPin := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	newPin := fmt.Sprintf("http://localhost:%d/callback", port) // resolves to the same bind host/port/path

	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "dcr-registered-for-127001", "secret", port, oldPin))

	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name: serverName,
		URL:  serverURL,
		OAuth: &config.OAuthConfig{
			RedirectURI: newPin,
		},
	}, store)
	require.NotNil(t, oauthConfig)

	assert.Empty(t, oauthConfig.ClientID,
		"a DCR client registered for the old host spelling must not be shipped with a pin using a different spelling")
	storedClientID, _, _, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Empty(t, storedClientID, "the stale DCR record (old host spelling) must be cleared so the next login re-registers")
}

// TestRemovingPinClearsDCRCredentialsRegisteredForCustomPath covers a round-4
// cross-model review finding: the hygiene check only ran in the
// `pinnedPort > 0` branch, so REMOVING a pin (or changing host family) left a
// stale DCR client_id untouched. A client registered for a pinned custom path
// at port P must not be reused once the pin is removed and mcpproxy falls
// back to binding the historical default path ("/oauth/callback") on that
// same port — that is a different registered redirect_uri and the provider
// would reject it.
func TestRemovingPinClearsDCRCredentialsRegisteredForCustomPath(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "pin-removed-server"
	stopCallbackServer(t, serverName)
	serverURL := upstream.URL + "/mcp"
	serverKey := GenerateServerKey(serverName, serverURL)

	port := reserveLoopbackPort(t)
	registeredPin := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "dcr-registered-for-custom-path", "secret", port, registeredPin))

	// No OAuth.RedirectURI this time: the pin was removed from config, but the
	// stored port is still reused as a preference (Spec 022).
	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name:  serverName,
		URL:   serverURL,
		OAuth: &config.OAuthConfig{},
	}, store)
	require.NotNil(t, oauthConfig)

	assert.Empty(t, oauthConfig.ClientID,
		"a DCR client registered for a pinned custom path must not be shipped once the pin is removed and the default path is used instead")
	storedClientID, _, _, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Empty(t, storedClientID, "the stale DCR record (pin removed) must be cleared so the next login re-registers")
}

// TestLegacyRecordUnderNewPinClearsDCRCredentials covers the other round-4
// finding: a legacy record with no persisted RedirectURI (predates the
// round-2 fix, or was never pinned before) cannot be verified to match a
// newly added pin from port and path alone — the exact string it was
// registered with is unknown. It must always be cleared once a pin is
// present, even when the port happens to match, rather than risk a silent
// redirect_uri_mismatch.
func TestLegacyRecordUnderNewPinClearsDCRCredentials(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "legacy-record-new-pin-server"
	stopCallbackServer(t, serverName)
	serverURL := upstream.URL + "/mcp"
	serverKey := GenerateServerKey(serverName, serverURL)

	port := reserveLoopbackPort(t)
	// Legacy record: port persisted, but no exact RedirectURI (empty string
	// simulates either a pre-round-2-fix record or one that predates any pin).
	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "dcr-legacy-record", "secret", port, ""))

	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name: serverName,
		URL:  serverURL,
		OAuth: &config.OAuthConfig{
			RedirectURI: fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", port),
		},
	}, store)
	require.NotNil(t, oauthConfig)

	assert.Empty(t, oauthConfig.ClientID,
		"a legacy record with no persisted redirect_uri must not be reused under a new pin, even if the port matches")
	storedClientID, _, _, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Empty(t, storedClientID, "the legacy record must be cleared once used under a pin")
}

// TestStartCallbackServerOnHost_RedirectURISpellingChangeReplacesCachedServer
// covers the other half of the same round-3 finding: the callback-server
// reuse check (matchesBinding) only compared resolved bind host/port/path, so
// a pin with the same resolved identity but different literal spelling
// reused the CACHED server object - leaving CallbackServer.RedirectURI (and
// anything that persists it) holding the OLD spelling instead of the one
// actually being sent to the provider for this attempt.
func TestStartCallbackServerOnHost_RedirectURISpellingChangeReplacesCachedServer(t *testing.T) {
	serverName := "redirect-uri-spelling-change-server"
	stopCallbackServer(t, serverName)
	port := reserveLoopbackPort(t)

	oldPin := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	first, err := GetGlobalCallbackManager().StartCallbackServerOnHost(serverName, CallbackBinding{
		Port: port, Path: "/callback", RedirectURI: oldPin, Pinned: true,
	})
	require.NoError(t, err)
	assert.Equal(t, oldPin, first.RedirectURI)

	newPin := fmt.Sprintf("http://localhost:%d/callback", port)
	second, err := GetGlobalCallbackManager().StartCallbackServerOnHost(serverName, CallbackBinding{
		Port: port, Path: "/callback", RedirectURI: newPin, Pinned: true,
	})
	require.NoError(t, err)
	assert.Equal(t, newPin, second.RedirectURI,
		"a spelling-only change in the pin must not be served by a cached server still recording the old spelling")
}

// TestStartCallbackServerOnHost_UnpinningReplacesCachedPinnedServer covers a
// round-5 cross-model review finding: matchesBinding used to skip the
// RedirectURI comparison entirely whenever the NEW request was unpinned
// (binding.RedirectURI == ""), reasoning that an unpinned request can never
// have spelling drift. That missed the other half of the problem: the CACHED
// server can be the one with non-default spelling, left over from an earlier
// pinned attempt on the same port (e.g. a config hot-reload that removed the
// pin, all within one running process). Reusing it would silently hand out
// the OLD pinned string instead of the canonical reconstructed one.
func TestStartCallbackServerOnHost_UnpinningReplacesCachedPinnedServer(t *testing.T) {
	serverName := "unpin-replaces-cached-server"
	stopCallbackServer(t, serverName)
	port := reserveLoopbackPort(t)

	pinned := fmt.Sprintf("http://localhost:%d/oauth/callback", port)
	first, err := GetGlobalCallbackManager().StartCallbackServerOnHost(serverName, CallbackBinding{
		Port: port, RedirectURI: pinned, Pinned: true,
	})
	require.NoError(t, err)
	assert.Equal(t, pinned, first.RedirectURI)

	// Same resolved bind host, port and path (unpinned always resolves to
	// 127.0.0.1 + DefaultRedirectPath, matching "localhost"'s resolved bind
	// host and this pin's default path) — but no RedirectURI this time.
	second, err := GetGlobalCallbackManager().StartCallbackServerOnHost(serverName, CallbackBinding{
		Port: port,
	})
	require.NoError(t, err)
	expected := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", port)
	assert.Equal(t, expected, second.RedirectURI,
		"an unpinned request must not be served by a cached server still recording an old pin's spelling")
}

// TestCreateOAuthConfig_PersistsVerbatimPinnedRedirectURI covers a round-2
// cross-model review finding: the callback server used to always RECONSTRUCT
// its stored RedirectURI from the resolved bind host, port and decoded path
// (fmt.Sprintf("http://%s%s", listenAddr, path)), even when pinned. That loses
// the operator's exact spelling - "localhost" resolves to a 127.0.0.1 bind
// address - so the value persisted for later DCR-hygiene comparison was never
// actually the string sent to the provider. A pin must be persisted
// byte-for-byte.
func TestCreateOAuthConfig_PersistsVerbatimPinnedRedirectURI(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	port := reserveLoopbackPort(t)
	pinned := fmt.Sprintf("http://localhost:%d/callback", port)

	serverName := "verbatim-pin-server"
	stopCallbackServer(t, serverName)

	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name: serverName,
		URL:  upstream.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			ClientID:    "static-client",
			RedirectURI: pinned,
		},
	}, store)
	require.NotNil(t, oauthConfig)
	assert.Equal(t, pinned, oauthConfig.RedirectURI)

	callbackServer, ok := GetCallbackServer(serverName)
	require.True(t, ok)
	assert.Equal(t, pinned, callbackServer.RedirectURI,
		"the callback server must record the EXACT pinned string (host spelling included), not a reconstruction")
}

// TestCreateOAuthConfig_PercentEncodedPathSurvivesRestart covers the other
// half of the same review finding: a pinned path containing a percent-encoded
// reserved character (decodes to a literal "?") must not corrupt on a
// simulated restart. The bug was re-deriving the stored path by re-parsing a
// RECONSTRUCTED redirect URI string: embedding the already-decoded path
// (containing a literal "?") back into a fresh URL string, then parsing that
// string again, made url.Parse treat the "?" as introducing a query string -
// silently truncating the path on every restart and clearing DCR credentials
// that never actually changed.
func TestCreateOAuthConfig_PercentEncodedPathSurvivesRestart(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	port := reserveLoopbackPort(t)
	pinned := fmt.Sprintf("http://127.0.0.1:%d/cb%%3Fv", port) // decodes to path "/cb?v"

	serverName := "percent-encoded-path-server"
	stopCallbackServer(t, serverName)

	serverConfig := &config.ServerConfig{
		Name: serverName,
		URL:  upstream.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			RedirectURI: pinned, // no ClientID: exercises the DCR hygiene path, not the static-client bypass
		},
	}
	serverKey := GenerateServerKey(serverName, serverConfig.URL)

	// Simulate a DCR-registered client from a prior run, persisted against
	// this exact pin (matching what the round-2 fix now actually persists).
	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "dcr-for-percent-path", "secret", port, pinned))

	// A "restart" — the same unchanged pin must not trigger the
	// "stale registration" clearing path.
	oauthConfig := CreateOAuthConfig(serverConfig, store)
	require.NotNil(t, oauthConfig)
	assert.Equal(t, "dcr-for-percent-path", oauthConfig.ClientID,
		"the persisted DCR client_id must be reused for this login")

	storedClientID, _, _, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Equal(t, "dcr-for-percent-path", storedClientID,
		"an unchanged pin (even with a percent-encoded path) must not clear DCR credentials on restart")
}

// TestStartCallbackServerOnHost_MessyPathIsNotRedirectedByServeMux covers the
// third round-2 finding: http.ServeMux unconditionally 301-redirects any
// request whose path is not already "clean" (e.g. a double slash, or "."/".."
// segments) to the cleaned path BEFORE any handler runs — regardless of which
// patterns are registered. A pinned path that is not already in clean form
// (unusual, but not rejected by validation) would then never reach the exact
// comparison at all: the provider's callback request gets redirected instead
// of delivered, and the login hangs. The fix uses a plain http.HandlerFunc
// instead of http.ServeMux, which performs no such cleaning.
func TestStartCallbackServerOnHost_MessyPathIsNotRedirectedByServeMux(t *testing.T) {
	serverName := "messy-path-server"
	stopCallbackServer(t, serverName)

	callbackServer, err := GetGlobalCallbackManager().StartCallbackServerOnHost(serverName, CallbackBinding{
		Path: "/oauth//callback", // double slash: http.ServeMux would clean this to /oauth/callback
	})
	require.NoError(t, err)
	require.Equal(t, "/oauth//callback", callbackServer.Path)

	state := "messy-path-state"
	ch := callbackServer.RegisterState(state)
	defer callbackServer.UnregisterState(state)

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/oauth//callback?state=%s&code=test-code", callbackServer.Port, state))
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"a messy-but-pinned path must be served directly, not 301-redirected to its cleaned form")

	select {
	case params := <-ch:
		assert.Equal(t, "test-code", params["code"])
	default:
		t.Fatal("the callback handler did not deliver params to the registered waiter")
	}
}

// TestStartCallbackServerOnHost_PathWithMuxWildcardCharsDoesNotPanic covers a
// must-fix from cross-model review: Go 1.22+'s http.ServeMux treats "{" / "}"
// and a trailing "..." segment as wildcard pattern syntax, not literal path
// characters. Registering an operator-controlled path (from oauth.redirect_uri,
// issue #1304) directly as a mux pattern could panic on a malformed one at
// callback-server startup. The fix routes every path through a single "/"
// catch-all with an exact string comparison instead of a per-path
// registration, so a path containing "{" must start the server without
// panicking and must still be reachable at that literal path.
func TestStartCallbackServerOnHost_PathWithMuxWildcardCharsDoesNotPanic(t *testing.T) {
	serverName := "wildcard-char-path-server"
	stopCallbackServer(t, serverName)

	require.NotPanics(t, func() {
		callbackServer, err := GetGlobalCallbackManager().StartCallbackServerOnHost(serverName, CallbackBinding{
			Path: "/{oops",
		})
		require.NoError(t, err)
		require.Equal(t, "/{oops", callbackServer.Path)
	})
}

// TestPinnedPathChangeClearsDCRCredentialsForSamePort covers a gap the
// original path-pinning fix left open (found in cross-model review): the Spec
// 022 hygiene check only ever compared the stored PORT against the pin. A DCR
// client_id registered for "/oauth/callback" at port P is paired with the
// provider under that exact redirect_uri; pinning a *different path* at the
// *same* port produces a redirect_uri the provider never registered, and
// comparing port alone missed it, so the stale client_id kept being reused
// and the provider kept rejecting it with a permanent redirect_uri_mismatch.
func TestPinnedPathChangeClearsDCRCredentialsForSamePort(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "path-change-under-pin"
	stopCallbackServer(t, serverName)
	serverURL := upstream.URL + "/mcp"
	serverKey := GenerateServerKey(serverName, serverURL)

	port := reserveLoopbackPort(t)
	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "dcr-registered-for-old-path", "secret", port,
		fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", port)))

	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name: serverName,
		URL:  serverURL,
		OAuth: &config.OAuthConfig{
			RedirectURI: fmt.Sprintf("http://127.0.0.1:%d/callback", port),
		},
	}, store)
	require.NotNil(t, oauthConfig)

	assert.Empty(t, oauthConfig.ClientID,
		"a DCR client registered for the old callback path must not be shipped with a redirect_uri pinning a new path at the same port")
	storedClientID, _, _, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Empty(t, storedClientID, "the stale DCR record (old path, same port) must be cleared so the next login re-registers")
}

// TestPinnedSamePathAndPortKeepsDCRCredentials is the regression guard for the
// fix above: a pin that matches BOTH the stored port and path must NOT clear
// credentials on every restart (Spec 022's whole point is stability).
func TestPinnedSamePathAndPortKeepsDCRCredentials(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "stable-path-under-pin"
	stopCallbackServer(t, serverName)
	serverURL := upstream.URL + "/mcp"
	serverKey := GenerateServerKey(serverName, serverURL)

	port := reserveLoopbackPort(t)
	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "dcr-registered-for-callback", "secret", port,
		fmt.Sprintf("http://127.0.0.1:%d/callback", port)))

	oauthConfig := CreateOAuthConfig(&config.ServerConfig{
		Name: serverName,
		URL:  serverURL,
		OAuth: &config.OAuthConfig{
			RedirectURI: fmt.Sprintf("http://127.0.0.1:%d/callback", port),
		},
	}, store)
	require.NotNil(t, oauthConfig)

	assert.Equal(t, "dcr-registered-for-callback", oauthConfig.ClientID,
		"a DCR client registered for the SAME port and path as the pin must be reused, not re-registered")
	storedClientID, _, _, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Equal(t, "dcr-registered-for-callback", storedClientID)
}
