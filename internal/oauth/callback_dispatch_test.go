package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Issue #975: OAuth callback params must be dispatched to the flow that minted
// the `state`, and a callback nobody is waiting for must NOT render the
// "Authorization Successful" page.
// =============================================================================

const (
	testCallbackUser = "testuser"
	testCallbackPass = "testpass"
)

// startTestProvider boots the shared OAuth 2.1 test provider (tests/oauthserver)
// with a public client whose redirect URI is this callback server's.
func startTestProvider(t *testing.T, redirectURI, clientID string) *oauthserver.ServerResult {
	t.Helper()
	provider := oauthserver.Start(t, oauthserver.Options{
		Clients: []oauthserver.ClientConfig{{
			ClientID:      clientID,
			RedirectURIs:  []string{redirectURI},
			GrantTypes:    []string{"authorization_code", "refresh_token"},
			ResponseTypes: []string{"code"},
			Scopes:        []string{"read"},
			ClientName:    "callback-dispatch-test",
		}},
	})
	t.Cleanup(func() { _ = provider.Shutdown() })
	return provider
}

func pkcePair(verifier string) (codeVerifier, codeChallenge string) {
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// approveAuthorization drives the provider's login/consent form for the given
// state and follows the redirect all the way into mcpproxy's callback server,
// exactly as a browser would. Returns the final page the user sees.
func approveAuthorization(t *testing.T, provider *oauthserver.ServerResult, clientID, redirectURI, state, codeChallenge string) (status int, body string) {
	t.Helper()

	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {"read"},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"username":              {testCallbackUser},
		"password":              {testCallbackPass},
		"consent":               {"on"},
		"action":                {"approve"},
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.PostForm(provider.AuthorizationEndpoint, form)
	require.NoError(t, err, "authorization POST should reach the provider")
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(raw)
}

// exchangeCode performs the /token request the way a real flow would, proving
// the delivered code is redeemable (the reported bug was that no /token request
// ever happened).
func exchangeCode(t *testing.T, provider *oauthserver.ServerResult, clientID, redirectURI, code, verifier string) map[string]interface{} {
	t.Helper()

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.PostForm(provider.TokenEndpoint, form)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "token exchange failed: %s", string(raw))

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload
}

func recvParams(ch <-chan map[string]string, wait time.Duration) (map[string]string, bool) {
	select {
	case params := <-ch:
		return params, true
	case <-time.After(wait):
		return nil, false
	}
}

// TestCallbackDispatch_TwoConcurrentFlows is the decisive regression test for
// issue #975: a manual login mints state A, a background reconnect later mints
// state B on the SAME callback server, and the provider redirects with state A.
// The code must reach flow A. Before the fix a single shared channel handed the
// params to whichever receiver the runtime picked, and flow B discarded them.
func TestCallbackDispatch_TwoConcurrentFlows(t *testing.T) {
	manager := GetGlobalCallbackManager()
	serverName := "test-two-concurrent-flows"

	callbackServer, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	clientID := "two-flows-client"
	provider := startTestProvider(t, callbackServer.RedirectURI, clientID)

	// Repeat the race several times, alternating which flow registers first.
	// Dispatch by state must win every single round; the pre-fix shared channel
	// handed the params to whichever receiver the runtime happened to pick.
	const rounds = 5
	for round := 0; round < rounds; round++ {
		stateA := fmt.Sprintf("state-manual-login-A-%d", round)
		stateB := fmt.Sprintf("state-background-reconnect-B-%d", round)
		verifierA, challengeA := pkcePair(fmt.Sprintf("verifier-for-manual-login-flow-round-%d-aaaaaaaaaa", round))

		var chA, chB <-chan map[string]string
		if round%2 == 0 {
			// Flow A (the user's manual login) parks first, the background
			// reconnect joins a moment later.
			chA = callbackServer.RegisterState(stateA)
			chB = callbackServer.RegisterState(stateB)
		} else {
			// ...and the other way round.
			chB = callbackServer.RegisterState(stateB)
			chA = callbackServer.RegisterState(stateA)
		}

		status, page := approveAuthorization(t, provider, clientID, callbackServer.RedirectURI, stateA, challengeA)
		assert.Equal(t, http.StatusOK, status, "round %d: callback page should be served with 200 on success", round)
		assert.Contains(t, page, "Authorization Successful", "round %d: the user completed a real authorization", round)

		paramsA, gotA := recvParams(chA, 3*time.Second)
		require.True(t, gotA, "round %d: the flow that minted state A must receive the callback params", round)
		assert.Equal(t, stateA, paramsA["state"], "round %d", round)
		require.NotEmpty(t, paramsA["code"], "round %d: flow A must receive the authorization code", round)

		_, gotB := recvParams(chB, 200*time.Millisecond)
		assert.False(t, gotB, "round %d: the background reconnect (state B) must NOT consume flow A's callback", round)
		assert.True(t, callbackServer.HasState(stateB),
			"round %d: the background reconnect's waiter must still be registered", round)

		// The code flow A received is redeemable: a /token request actually happens.
		token := exchangeCode(t, provider, clientID, callbackServer.RedirectURI, paramsA["code"], verifierA)
		assert.NotEmpty(t, token["access_token"], "round %d: flow A's code must exchange for a token", round)

		callbackServer.UnregisterState(stateA)
		callbackServer.UnregisterState(stateB)
	}

	assert.Len(t, provider.Server.GetIssuedTokens(), rounds,
		"the provider must have received one /token request per completed flow")
}

// TestCallbackDispatch_UnknownStateShowsFailurePage verifies (b): a callback
// whose state nobody registered gets an explicit failure page — never
// "Authorization Successful" — and does not steal a live flow's slot.
func TestCallbackDispatch_UnknownStateShowsFailurePage(t *testing.T) {
	manager := GetGlobalCallbackManager()
	serverName := "test-unknown-state"

	callbackServer, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	liveState := "state-live-flow"
	chLive := callbackServer.RegisterState(liveState)
	t.Cleanup(func() { callbackServer.UnregisterState(liveState) })

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(callbackServer.RedirectURI + "?code=orphan-code&state=nobody-is-waiting-for-this")
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	page := string(raw)

	assert.NotContains(t, page, "Authorization Successful",
		"an orphaned code must never be reported as a successful sign-in")
	assert.Contains(t, page, "Authorization Failed", "the browser must show an explicit failure")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "unknown state is a client error")

	_, gotLive := recvParams(chLive, 300*time.Millisecond)
	assert.False(t, gotLive, "an unknown state must not consume another flow's params")

	// The live flow still works afterwards.
	clientID := "unknown-state-client"
	provider := startTestProvider(t, callbackServer.RedirectURI, clientID)
	_, challenge := pkcePair("verifier-for-the-still-live-flow-bbbbbbbbbbbbbbbb")
	status, successPage := approveAuthorization(t, provider, clientID, callbackServer.RedirectURI, liveState, challenge)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, successPage, "Authorization Successful")

	params, got := recvParams(chLive, 3*time.Second)
	require.True(t, got, "the live flow must still receive its own callback")
	assert.Equal(t, liveState, params["state"])
}

// TestCallbackDispatch_ProviderErrorShowsFailurePage verifies that a provider
// error (access_denied) is reported as a failure in the browser even though a
// flow is waiting for that state, and that the waiting flow still sees it.
func TestCallbackDispatch_ProviderErrorShowsFailurePage(t *testing.T) {
	manager := GetGlobalCallbackManager()
	serverName := "test-provider-error"

	callbackServer, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	state := "state-denied-flow"
	ch := callbackServer.RegisterState(state)
	t.Cleanup(func() { callbackServer.UnregisterState(state) })

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(fmt.Sprintf("%s?error=access_denied&error_description=User+denied&state=%s",
		callbackServer.RedirectURI, url.QueryEscape(state)))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	page := string(raw)

	assert.NotContains(t, page, "Authorization Successful")
	assert.Contains(t, page, "Authorization Failed")
	assert.True(t, strings.Contains(page, "access_denied"), "the provider error should be shown to the user")

	params, got := recvParams(ch, 3*time.Second)
	require.True(t, got, "the waiting flow must still be told the provider denied the request")
	assert.Equal(t, "access_denied", params["error"])
}

// TestCallbackDispatch_UnregisteredStateIsRejected verifies that a completed or
// cancelled flow's state cannot be reused: after UnregisterState the callback
// server treats it as unknown.
func TestCallbackDispatch_UnregisteredStateIsRejected(t *testing.T) {
	manager := GetGlobalCallbackManager()
	serverName := "test-unregistered-state"

	callbackServer, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	state := "state-finished-flow"
	callbackServer.RegisterState(state)
	callbackServer.UnregisterState(state)

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(fmt.Sprintf("%s?code=late-code&state=%s", callbackServer.RedirectURI, url.QueryEscape(state)))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, string(raw), "Authorization Failed")
}

// TestCallbackDispatch_ReuseKeepsWaiters replaces the old "drain stale params"
// behaviour: reusing a callback server for a retry must not cancel or drain a
// waiter that is already parked on it (root cause #3 in the report).
func TestCallbackDispatch_ReuseKeepsWaiters(t *testing.T) {
	manager := GetGlobalCallbackManager()
	serverName := "test-reuse-keeps-waiters"

	first, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	state := "state-parked-before-reuse"
	ch := first.RegisterState(state)
	t.Cleanup(func() { first.UnregisterState(state) })

	second, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	assert.Same(t, first, second, "callback server should be reused")

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(fmt.Sprintf("%s?code=kept-code&state=%s", second.RedirectURI, url.QueryEscape(state)))
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	params, got := recvParams(ch, 3*time.Second)
	require.True(t, got, "a waiter parked before the reuse must still receive its callback")
	assert.Equal(t, "kept-code", params["code"])
}

// TestCallbackDispatch_RecordsFailureForUndeliverableCallback verifies (d): an
// undeliverable callback is reported through the OAuth failure hook so the
// operator sees it on the server's status instead of only in the log.
func TestCallbackDispatch_RecordsFailureForUndeliverableCallback(t *testing.T) {
	manager := GetGlobalCallbackManager()
	serverName := "test-failure-hook"

	callbackServer, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	type failure struct {
		server string
		err    error
	}
	failures := make(chan failure, 4)
	tm := GetTokenStoreManager()
	tm.SetOAuthFailureCallback(func(server string, err error) {
		failures <- failure{server: server, err: err}
	})
	t.Cleanup(func() { tm.SetOAuthFailureCallback(nil) })

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(callbackServer.RedirectURI + "?code=orphan&state=unknown-state-xyz")
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	select {
	case f := <-failures:
		assert.Equal(t, serverName, f.server)
		require.Error(t, f.err)
		assert.Contains(t, strings.ToLower(f.err.Error()), "state")
	case <-time.After(3 * time.Second):
		t.Fatal("undeliverable OAuth callback did not surface through the failure hook")
	}
}
