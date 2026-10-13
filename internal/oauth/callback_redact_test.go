package oauth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Issue #1158, review round 2, finding B1.
//
// The OAuth CALLBACK handler logged, at Info, on every single login:
//
//	zap.String("query", r.URL.RawQuery)   // the whole callback query
//	zap.String("code", params["code"])    // the authorization code itself
//
// The authorization code is a single-use credential: presented at the token
// endpoint with the PKCE verifier it yields an access token. It landed in
// ~/.mcpproxy/logs/main.log — a file that outlives the process, is readable by
// anything with disk access, and is served over REST for stdio servers — on
// every OAuth login, with no debug flag involved.
//
// The oracle is the one this issue keeps needing: no RUN of the secret's bytes
// may appear anywhere in the rendered line. A whole-string containment check
// passes against a half-mask.
func assertNoRun(t *testing.T, rendered, secret string, minRun int) {
	t.Helper()
	for i := 0; i+minRun <= len(secret); i++ {
		assert.NotContains(t, rendered, secret[i:i+minRun],
			"a %d-byte run of the credential survived into %q", minRun, rendered)
	}
}

// newObservedCallbackServer builds a CallbackServer wired to an in-memory log
// sink, with one flow parked on `state` so the delivery path is exercised.
func newObservedCallbackServer(t *testing.T, state string) (*CallbackServer, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	cs := &CallbackServer{
		ServerName: "alpha",
		logger:     zap.New(core),
		waiters:    make(map[string]chan map[string]string),
	}
	if state != "" {
		cs.RegisterState(state)
	}
	return cs, logs
}

func renderedLines(logs *observer.ObservedLogs) string {
	var b strings.Builder
	for _, entry := range logs.All() {
		b.WriteString(entry.Message)
		for k, v := range entry.ContextMap() {
			fmt.Fprintf(&b, " %s=%v", k, v)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestCallbackHandlerNeverLogsTheAuthorizationCode(t *testing.T) {
	const code = "AUTHCODEwqYnFsUD0Rk4pQ3v7XcJ2m"
	const state = "STATENONCE9fbc41aa7d2e"

	cs, logs := newObservedCallbackServer(t, state)

	req := httptest.NewRequest(http.MethodGet, DefaultRedirectPath+
		"?code="+code+"&state="+state+"&scope=read%20write", nil)
	rec := httptest.NewRecorder()
	cs.handleCallback(rec, req)

	out := renderedLines(logs)
	require.NotEmpty(t, out, "the handler must still log — a fix that deletes the line is not the fix")

	assertNoRun(t, out, code, 6)
	assertNoRun(t, out, state, 6)

	// The diagnostics the lines exist for must survive.
	assert.Contains(t, out, "code_present=true",
		"an operator still needs to know whether the provider returned a code at all")
	assert.Contains(t, out, "scope=read write",
		"non-credential callback parameters must stay readable")
	assert.Contains(t, out, StateFingerprint(state),
		"the state fingerprint is what correlates the callback with the flow that minted it")
}

// A callback that carries no code (the provider reported an error instead) must
// still be diagnosable, and the provider's own free text must be scrubbed.
func TestCallbackHandlerScrubsProviderErrorText(t *testing.T) {
	cs, logs := newObservedCallbackServer(t, "S-unknown-flow")

	req := httptest.NewRequest(http.MethodGet, DefaultRedirectPath+
		"?error=invalid_request&error_description=bad+redirect+for+token%3Dghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789&state=S-unknown-flow", nil)
	rec := httptest.NewRecorder()
	cs.handleCallback(rec, req)

	out := renderedLines(logs)
	assert.NotContains(t, out, "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789",
		"provider-authored error text is upstream free text and gets the free-text rule")
	assert.Contains(t, out, "invalid_request",
		"the provider's error CODE is the diagnostic and must survive")
}

// The unknown-state path builds an operator-visible error message. It quoted
// the raw state, and that message is stored and surfaced by
// RecordOAuthFailure.
func TestUnknownStateCallbackMessageCarriesNoNonce(t *testing.T) {
	const state = "STATENONCEbb17c9d4ef05"
	cs, logs := newObservedCallbackServer(t, "") // nobody waiting

	req := httptest.NewRequest(http.MethodGet, DefaultRedirectPath+"?code=X&state="+state, nil)
	rec := httptest.NewRecorder()
	cs.handleCallback(rec, req)

	out := renderedLines(logs)
	assertNoRun(t, out, state, 6)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLogSafeCallbackQuery(t *testing.T) {
	const code = "AUTHCODEqq81mVzZlPd0"
	got := LogSafeCallbackQuery("code=" + code + "&state=abc123def456&scope=read&error_description=nope")

	assertNoRun(t, got, code, 6)
	assert.Contains(t, got, "scope=read", "a benign parameter stays readable")
	assert.Contains(t, got, "code=", "the parameter NAME survives; only the value goes")
	assert.NotContains(t, got, "state=abc123def456")

	// A query mcpproxy cannot parse still must not be published verbatim.
	assert.NotContains(t, LogSafeCallbackQuery("%zz&access_token=ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"),
		"ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789")

	assert.Equal(t, "", LogSafeCallbackQuery(""))
}

func TestStateFingerprintCorrelatesWithoutDisclosing(t *testing.T) {
	const state = "STATENONCE-3f7a91cc"
	fp := StateFingerprint(state)

	assertNoRun(t, fp, state, 4)
	assert.Equal(t, fp, StateFingerprint(state), "the same flow must render the same handle on every line")
	assert.NotEqual(t, fp, StateFingerprint(state+"x"), "two live flows must stay distinguishable")
	assert.Equal(t, "(none)", StateFingerprint(""))
}
