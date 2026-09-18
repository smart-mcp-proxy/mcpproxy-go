//go:build server

package auth

// Spec 107 T048 (PR-B, Phase B.3) — the handler-level half of the front door:
// the post-login `redirect_uri` matrix of FR-028 / US2.7 driven through the
// production HandleLogin → fake IdP → HandleCallback path.
//
// Every row of the matrix is asserted at THREE points, because each one can
// regress on its own:
//
//  1. `RedirectRejected` on the pending state HandleLogin stores (the flag is
//     decided at login time, before the IdP is ever contacted);
//  2. the browser lands on `/ui/` (rejected) or on the requested path
//     (honoured) after the callback;
//  3. the terminal LoginResult carries `redirect_rejected` in Flags exactly
//     when the value was replaced (FR-017 — the flag rides on the attempt's
//     auth_event line in PR-D, never on the page).
//
// The percent-encoded forms exist because `r.URL.Query().Get` percent-decodes
// before the sanitiser runs, so `/%5Cevil.example` reaches it as `/\evil.example`
// and `/ok%0D%0ASet-Cookie:x` as a CRLF injection; the literal tab and CRLF
// rows cannot be sent through a Go HTTP server (the request line is refused
// with 400 before any handler runs), so they are delivered as raw query bytes
// on the request object — exactly what a non-Go ingress that forwards the
// request target verbatim would hand the handler.
//
// The harness-level half (public_url, Secure cookie, trusted proxies, the
// scheme-disagreement warning) lives in internal/serveredition/front_door_test.go
// because the wiring harness of T046 is in that package.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// redirectMatrixRow is one FR-028 fixture: the raw query value as the request
// target carries it, whether the sanitiser must replace it, and where the
// browser must land after the callback.
type redirectMatrixRow struct {
	name       string
	rawQuery   string // bytes placed after `redirect_uri=` in the raw query
	rejected   bool
	wantTarget string
}

// redirectMatrix is the US2.7 + FR-028 matrix from tasks.md T048. The
// `wantTarget` of an honoured row is the value the sanitiser admits (a
// same-origin path, query kept); every rejected row lands on /ui/.
var redirectMatrix = []redirectMatrixRow{
	{name: "absolute https origin", rawQuery: "https://evil.example/", rejected: true, wantTarget: "/ui/"},
	{name: "protocol-relative", rawQuery: "//evil.example", rejected: true, wantTarget: "/ui/"},
	{name: "backslash after slash", rawQuery: `/\evil.example`, rejected: true, wantTarget: "/ui/"},
	{name: "javascript scheme", rawQuery: "javascript:alert(1)", rejected: true, wantTarget: "/ui/"},
	{name: "encoded CRLF header injection", rawQuery: "/ok%0D%0ASet-Cookie:x", rejected: true, wantTarget: "/ui/"},
	{name: "encoded NUL", rawQuery: "/ok%00", rejected: true, wantTarget: "/ui/"},
	{name: "encoded backslash", rawQuery: "/%5Cevil.example", rejected: true, wantTarget: "/ui/"},
	{name: "encoded double slash", rawQuery: "/%2F%2Fevil.example", rejected: true, wantTarget: "/ui/"},
	{name: "literal tab", rawQuery: "/ok\t", rejected: true, wantTarget: "/ui/"},
	{name: "literal CRLF", rawQuery: "/ok\r\n", rejected: true, wantTarget: "/ui/"},
	{name: "same-origin path with query", rawQuery: "/my/tokens?x=1", rejected: false, wantTarget: "/my/tokens?x=1"},
	{name: "same-origin path with encoded query", rawQuery: "/my/tokens%3Fx%3D1", rejected: false, wantTarget: "/my/tokens?x=1"},
	{name: "root", rawQuery: "/", rejected: false, wantTarget: "/"},
	{name: "empty falls back to /ui/ without a flag", rawQuery: "", rejected: false, wantTarget: "/ui/"},
}

// loginWithRawRedirect calls HandleLogin with the raw query bytes and returns
// the authorization URL the handler redirected to and the state it minted.
func (r *refusalRig) loginWithRawRedirect(rid, rawQuery string) (*url.URL, string) {
	r.t.Helper()
	req := withRequestID(httptest.NewRequest(http.MethodGet, "http://"+refusalHost+"/api/v1/auth/login", nil), rid)
	req.URL.RawQuery = "redirect_uri=" + rawQuery
	w := httptest.NewRecorder()
	r.handler.HandleLogin(w, req)
	require.Equal(r.t, http.StatusFound, w.Code, "login must redirect to the IdP; body=%s", w.Body.String())
	loc, err := url.Parse(w.Header().Get("Location"))
	require.NoError(r.t, err)
	state := loc.Query().Get("state")
	require.NotEmpty(r.t, state, "the authorization URL must carry the pending state")
	return loc, state
}

// pendingRedirectRejected reads the RedirectRejected flag of a pending state.
func (r *refusalRig) pendingRedirectRejected(state string) (rejected bool, stored string) {
	r.handler.statesMu.Lock()
	defer r.handler.statesMu.Unlock()
	pending, ok := r.handler.pendingStates[state]
	require.True(r.t, ok, "state %q must be pending after login", state)
	return pending.RedirectRejected, pending.RedirectURI
}

// TestFrontDoor_RedirectURIMatrix drives every FR-028 row end to end.
func TestFrontDoor_RedirectURIMatrix(t *testing.T) {
	for _, row := range redirectMatrix {
		t.Run(row.name, func(t *testing.T) {
			rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
			rid := "rid-" + row.name

			authURL, state := rig.loginWithRawRedirect(rid, row.rawQuery)

			// 1. Decided at login, carried on the pending state.
			rejected, stored := rig.pendingRedirectRejected(state)
			assert.Equal(t, row.rejected, rejected, "RedirectRejected on the pending state for %q", row.rawQuery)
			assert.Equal(t, row.wantTarget, stored, "the pending state stores the value the callback will redirect to")

			// The rejected value never reaches the IdP either: the authorization
			// URL carries no trace of it.
			assert.NotContains(t, authURL.String(), "evil.example")
			assert.NotContains(t, authURL.String(), "javascript")

			// 2. The browser lands where the sanitiser decided.
			cb := rig.authorizeForm(authURL, "approve")
			w := rig.callback(rid, cb.String())
			require.Equal(t, http.StatusFound, w.Code, "login must succeed; body=%s", w.Body.String())
			assert.Equal(t, row.wantTarget, w.Header().Get("Location"))

			// 3. The terminal result carries the flag exactly when replaced.
			res := rig.results.exactlyOne(t)
			assert.Equal(t, LoginOK, res.Reason)
			assert.Equal(t, rid, res.RequestID)
			if row.rejected {
				assert.Contains(t, flagsOf(res), string(FlagRedirectRejected))
			} else {
				assert.NotContains(t, flagsOf(res), string(FlagRedirectRejected))
			}
		})
	}
}

// TestFrontDoor_RejectedRedirectIsNeverEchoed: the replacement is silent —
// no error status, no reflection of the offending value on the login redirect
// or on the callback response (FR-028: "never an error, never echoed").
func TestFrontDoor_RejectedRedirectIsNeverEchoed(t *testing.T) {
	rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
	const rid = "rid-echo"
	const evil = "https://evil.example/pwn"

	req := withRequestID(httptest.NewRequest(http.MethodGet, "http://"+refusalHost+"/api/v1/auth/login", nil), rid)
	req.URL.RawQuery = "redirect_uri=" + url.QueryEscape(evil)
	w := httptest.NewRecorder()
	rig.handler.HandleLogin(w, req)
	require.Equal(t, http.StatusFound, w.Code)
	for k, vs := range w.Header() {
		for _, v := range vs {
			assert.NotContains(t, v, "evil.example", "header %s must not echo the rejected value", k)
		}
	}
	assert.NotContains(t, w.Body.String(), "evil.example")

	authURL, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	cb := rig.authorizeForm(authURL, "approve")
	cw := rig.callback(rid, cb.String())
	require.Equal(t, http.StatusFound, cw.Code)
	assert.Equal(t, "/ui/", cw.Header().Get("Location"))
	assert.NotContains(t, cw.Body.String(), "evil.example")
}
