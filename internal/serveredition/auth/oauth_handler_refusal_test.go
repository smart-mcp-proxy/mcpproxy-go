//go:build server

package auth

// Spec 107 T039 (PR-B, compile-red until T044): every login refusal renders
// the ONE generic 403 page keyed by a request id, every unavailability renders
// the ONE 503 page, the typed terminal LoginResult is reported exactly once
// through the handler's observer hook, and the user store is untouched on
// every refusal. No audit line is asserted here (T106/T107 in PR-D re-drive
// these fixtures against the auth_event emitter installed on the same hook).
//
// Contract exercised here (tasks.md T042/T044, data-model §1/§7, FR-013,
// FR-017, FR-021, FR-024, contracts/rest-endpoints.md §10):
//
//	// T042 — the provider factory takes the OAuth block and is resolved ONCE
//	// in NewOAuthHandler (h.provider); a registry swap must precede construction.
//	var providerRegistry map[string]func(*config.ServerEditionOAuthConfig) *OAuthProvider
//
//	// T044 — the handler reads the LIVE server-edition block (role from the
//	// current admin_emails, never the boot pointer).
//	func NewOAuthHandler(*users.UserStore, *SessionManager, ServerEditionConfigProvider, []byte, *zap.SugaredLogger) *OAuthHandler
//
//	// T044 — closed reason vocabulary (FR-013 auth_event `reason`), string-kinded.
//	type LoginRefusal string
//	type LoginResult struct {
//	    RequestID string
//	    Surface   string       // "login" | "logout"
//	    Reason    LoginRefusal // ok | authorization_denied | … | internal_error
//	    UserID    string       // set when the attempt reached the store and a record exists
//	    EmailHash string       // hex SHA-256 of the normalised email, only for a verified email
//	    Flags     []string     // provider_rebound | redirect_rejected | groups_claim_missing
//	}
//	// nil = no-op; called exactly once per terminal attempt (FR-017).
//	OAuthHandler.LoginResultObserver func(LoginResult)
//
//	// T044 — fault-injection seams, defaulted to the real implementations.
//	OAuthHandler.loginStore     loginStore     // UpdateUserLogin(ctx, users.LoginClaims) (users.LoginOutcome, error); GetUserByEmail(string) (*users.User, error)
//	OAuthHandler.sessionCreator sessionCreator // CreateSession(userID string, r *http.Request) (*users.Session, error)
//	OAuthHandler.bearerSigner   func(hmacKey []byte, userID, email, displayName, role, provider string, ttl time.Duration) (string, error)
//
//	// T044 — pendingStates bounded at 10,000 entries, oldest CreatedAt evicted on insert.
//	// T041 — config: OAuth.IssuerURL, OAuth.AllowInsecureIssuer, OAuth.Scopes,
//	//        OAuth.GroupsClaim, OAuth.EmailVerifiedPolicy.
//	// T044 — users.User.Groups []string, users.User.GroupsUpdatedAt time.Time.
//
// The 403 page carries "Sign-in was not permitted (ref <request id>)" and the
// 503 page "Sign-in is temporarily unavailable (ref <request id>)"
// (contracts/rest-endpoints.md §10). Neither ever carries the closed reason,
// the IdP's `error`/`error_description`, a claim value or a token.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

const (
	refusalUser        = "alice@example.com"
	refusalPass        = "pass"
	refusalSub         = "alice-sub-1"
	refusalHost        = "127.0.0.1"
	refusalCallbackURI = "http://" + refusalHost + "/api/v1/auth/callback"

	refusalPage403     = "Sign-in was not permitted"
	refusalPage503     = "Sign-in is temporarily unavailable"
	pendingStatesCap   = 10000
	refusalIdPErrorMsg = "Injected error"
	refusalDenyMsg     = "User denied the authorization request"
)

var refusalAliceClaims = map[string]any{
	"sub":            refusalSub,
	"email":          refusalUser,
	"email_verified": true,
	"name":           "Alice Example",
	"groups":         []any{"eng", "sre"},
}

// FR-013 auth_event reasons that render the generic 403 page. Every entry in
// this table is refused with the same page and status (FR-024).
var refusal403Reasons = []string{
	"authorization_denied", "id_token_invalid", "nonce_mismatch", "audience_mismatch",
	"issuer_mismatch", "token_expired", "email_missing", "email_unverified",
	"domain_not_allowed", "subject_mismatch", "userinfo_subject_mismatch",
	"user_disabled", "state_invalid",
}

// ---------------------------------------------------------------------------
// Observer recorder
// ---------------------------------------------------------------------------

type loginResultRecorder struct {
	mu      sync.Mutex
	results []LoginResult
}

func (r *loginResultRecorder) observe(res LoginResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, res)
}

func (r *loginResultRecorder) all() []LoginResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]LoginResult, len(r.results))
	copy(out, r.results)
	return out
}

func (r *loginResultRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = nil
}

// exactlyOne asserts the FR-017 invariant — one terminal result per attempt —
// and returns it.
func (r *loginResultRecorder) exactlyOne(t *testing.T) LoginResult {
	t.Helper()
	got := r.all()
	require.Len(t, got, 1, "exactly one LoginResult per terminal attempt, got %+v", got)
	return got[0]
}

// ---------------------------------------------------------------------------
// Live config holder (ServerEditionConfigProvider)
// ---------------------------------------------------------------------------

type refusalLiveConfig struct {
	mu      sync.Mutex
	current *config.ServerEditionConfig
}

func (l *refusalLiveConfig) get() *config.ServerEditionConfig {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.current
}

func (l *refusalLiveConfig) swap(cfg *config.ServerEditionConfig) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.current = cfg
}

// ---------------------------------------------------------------------------
// Rig
// ---------------------------------------------------------------------------

type refusalRig struct {
	t       *testing.T
	handler *OAuthHandler
	store   *users.UserStore
	live    *refusalLiveConfig
	fake    *oauthserver.ServerResult // nil for the legacy-provider rigs
	results *loginResultRecorder
	hmacKey []byte
	client  *http.Client // drives the fake IdP; never follows redirects
}

func refusalServerEditionConfig(oauthCfg *config.ServerEditionOAuthConfig) *config.ServerEditionConfig {
	return &config.ServerEditionConfig{
		Enabled:        true,
		AdminEmails:    []string{"admin@example.com"},
		OAuth:          oauthCfg,
		SessionTTL:     config.Duration(time.Hour),
		BearerTokenTTL: config.Duration(time.Hour),
	}
}

// newRefusalRig builds an OAuthHandler over a real BBolt store with the
// observer installed. The provider is resolved once at construction (T042),
// so any registry swap must happen before this call.
func newRefusalRig(t *testing.T, oauthCfg *config.ServerEditionOAuthConfig) *refusalRig {
	t.Helper()

	tmpFile := filepath.Join(t.TempDir(), "refusal.db")
	db, err := bbolt.Open(tmpFile, 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	sessionMgr := NewSessionManager(store, time.Hour, false)
	live := &refusalLiveConfig{current: refusalServerEditionConfig(oauthCfg)}
	hmacKey := []byte("test-hmac-key-for-jwt-signing-32b")

	handler := NewOAuthHandler(store, sessionMgr, ServerEditionConfigProvider(live.get), hmacKey, zap.NewNop().Sugar())
	rec := &loginResultRecorder{}
	handler.LoginResultObserver = rec.observe

	return &refusalRig{
		t:       t,
		handler: handler,
		store:   store,
		live:    live,
		results: rec,
		hmacKey: hmacKey,
		client: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func refusalFakeOptions(mode oauthserver.ErrorMode) oauthserver.Options {
	return oauthserver.Options{
		OIDC:               true,
		ValidUsers:         map[string]string{refusalUser: refusalPass},
		UserClaims:         map[string]map[string]any{refusalUser: refusalAliceClaims},
		ClientRedirectURIs: []string{refusalCallbackURI},
		GroupsClaim:        "groups",
		ErrorMode:          mode,
	}
}

// newOIDCRefusalRig starts the in-process fake OIDC IdP with the given tamper
// mode and a handler configured with `provider: oidc` against it. mutate may
// adjust the OAuth block before the handler is constructed.
func newOIDCRefusalRig(t *testing.T, mode oauthserver.ErrorMode, mutate func(*config.ServerEditionOAuthConfig)) *refusalRig {
	t.Helper()
	fake := oauthserver.Start(t, refusalFakeOptions(mode))
	t.Cleanup(func() { _ = fake.Shutdown() })

	oauthCfg := &config.ServerEditionOAuthConfig{
		Provider:            "oidc",
		ClientID:            fake.ClientID,
		ClientSecret:        fake.ClientSecret,
		IssuerURL:           fake.IssuerURL, // http://127.0.0.1:<port> — loopback
		AllowInsecureIssuer: true,
		Scopes:              []string{"openid", "profile", "email"},
		GroupsClaim:         "groups",
		EmailVerifiedPolicy: "refuse_false",
	}
	if mutate != nil {
		mutate(oauthCfg)
	}
	rig := newRefusalRig(t, oauthCfg)
	rig.fake = fake
	return rig
}

// withRequestID stamps the request id the RequestIDMiddleware would have put
// on the context, so the page and the LoginResult can be keyed by it.
func withRequestID(req *http.Request, rid string) *http.Request {
	return req.WithContext(reqcontext.WithRequestID(req.Context(), rid))
}

// login calls HandleLogin with the given request id and returns the recorder
// and the parsed Location (nil when the response is not a redirect).
func (r *refusalRig) login(rid string) (*httptest.ResponseRecorder, *url.URL) {
	r.t.Helper()
	req := withRequestID(httptest.NewRequest(http.MethodGet, "http://"+refusalHost+"/api/v1/auth/login", nil), rid)
	w := httptest.NewRecorder()
	r.handler.HandleLogin(w, req)
	if w.Code != http.StatusFound {
		return w, nil
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	require.NoError(r.t, err)
	return w, loc
}

// mustLogin is login with the redirect required.
func (r *refusalRig) mustLogin(rid string) *url.URL {
	r.t.Helper()
	w, loc := r.login(rid)
	require.Equal(r.t, http.StatusFound, w.Code, "login must redirect to the IdP; body=%s", w.Body.String())
	require.NotNil(r.t, loc)
	return loc
}

// idpRedirect returns the Location of a 302 answered by the fake IdP.
func (r *refusalRig) idpRedirect(resp *http.Response) *url.URL {
	r.t.Helper()
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	require.Equal(r.t, http.StatusFound, resp.StatusCode, "fake IdP must redirect back to the callback")
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(r.t, err)
	require.Equal(r.t, "/api/v1/auth/callback", loc.Path, "IdP redirect must target the callback: %s", loc)
	return loc
}

// authorizeGET follows the handler's redirect with a plain GET, for the knobs
// that answer the authorization request itself with an error redirect.
func (r *refusalRig) authorizeGET(authURL *url.URL) *url.URL {
	r.t.Helper()
	resp, err := r.client.Get(authURL.String())
	require.NoError(r.t, err)
	return r.idpRedirect(resp)
}

// authorizeForm submits the fake's headless login form with every parameter
// the handler put on the authorization URL (state, nonce, PKCE, redirect_uri)
// and the given consent action.
func (r *refusalRig) authorizeForm(authURL *url.URL, action string) *url.URL {
	r.t.Helper()
	form := authURL.Query()
	form.Set("username", refusalUser)
	form.Set("password", refusalPass)
	form.Set("action", action)
	if action == "approve" {
		form.Set("consent", "on")
	}
	resp, err := r.client.PostForm(r.fake.AuthorizationEndpoint, form)
	require.NoError(r.t, err)
	return r.idpRedirect(resp)
}

// callback calls HandleCallback with the given full callback URL.
func (r *refusalRig) callback(rid string, callbackURL string) *httptest.ResponseRecorder {
	r.t.Helper()
	req := withRequestID(httptest.NewRequest(http.MethodGet, callbackURL, nil), rid)
	req.Host = refusalHost
	w := httptest.NewRecorder()
	r.handler.HandleCallback(w, req)
	return w
}

// approveFlow drives login → form approve → callback and returns the callback
// recorder.
func (r *refusalRig) approveFlow(rid string) *httptest.ResponseRecorder {
	r.t.Helper()
	authURL := r.mustLogin(rid)
	cb := r.authorizeForm(authURL, "approve")
	return r.callback(rid, cb.String())
}

func (r *refusalRig) pendingLen() int {
	r.handler.statesMu.Lock()
	defer r.handler.statesMu.Unlock()
	return len(r.handler.pendingStates)
}

func (r *refusalRig) hasPending(state string) bool {
	r.handler.statesMu.Lock()
	defer r.handler.statesMu.Unlock()
	_, ok := r.handler.pendingStates[state]
	return ok
}

// usersSnapshot serialises every user record so "store untouched" is a byte
// comparison, not a field-by-field guess.
func (r *refusalRig) usersSnapshot() map[string]string {
	r.t.Helper()
	all, err := r.store.ListUsers()
	require.NoError(r.t, err)
	out := make(map[string]string, len(all))
	for _, u := range all {
		b, err := json.Marshal(u)
		require.NoError(r.t, err)
		out[u.ID] = string(b)
	}
	return out
}

func (r *refusalRig) sessionCount() int {
	r.t.Helper()
	all, err := r.store.ListSessions()
	require.NoError(r.t, err)
	return len(all)
}

func emailHashOf(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Page assertions
// ---------------------------------------------------------------------------

// assertGenericRefusalPage asserts the FR-024 403 page and returns the body
// with the request id replaced by a placeholder so pages of different
// attempts can be compared byte-for-byte.
func assertGenericRefusalPage(t *testing.T, w *httptest.ResponseRecorder, rid string) string {
	t.Helper()
	body := w.Body.String()
	assert.Equal(t, http.StatusForbidden, w.Code, "every refusal is 403; body=%s", body)
	assert.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), "text/html"), "the refusal is a page, got Content-Type %q", w.Header().Get("Content-Type"))
	assert.Contains(t, body, refusalPage403)
	assert.Contains(t, body, rid, "the page must carry the request id")
	assert.NotContains(t, body, refusalPage503)
	assertNoRefusalLeak(t, body)
	assert.Empty(t, w.Result().Cookies(), "a refusal never sets a cookie")
	return strings.ReplaceAll(body, rid, "<RID>")
}

// assertUnavailablePage asserts the FR-024 503 page.
func assertUnavailablePage(t *testing.T, w *httptest.ResponseRecorder, rid string) string {
	t.Helper()
	body := w.Body.String()
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "unavailability is 503; body=%s", body)
	assert.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), "text/html"), "the unavailability response is a page, got Content-Type %q", w.Header().Get("Content-Type"))
	assert.Contains(t, body, refusalPage503)
	assert.Contains(t, body, rid, "the page must carry the request id")
	assert.NotContains(t, body, refusalPage403)
	assertNoRefusalLeak(t, body)
	assert.Empty(t, w.Result().Cookies(), "an unavailability never sets a cookie")
	return strings.ReplaceAll(body, rid, "<RID>")
}

// assertNoRefusalLeak: the closed reason, the IdP's error vocabulary and the
// claim values reach only the server log, never the page.
func assertNoRefusalLeak(t *testing.T, body string) {
	t.Helper()
	for _, reason := range refusal403Reasons {
		assert.NotContains(t, body, reason, "the closed reason must not be rendered")
	}
	for _, s := range []string{"provider_error", "discovery_failed", "internal_error",
		"access_denied", "error_description", refusalIdPErrorMsg, refusalDenyMsg,
		refusalUser, refusalSub, "Alice Example", "eyJ"} {
		assert.NotContains(t, body, s, "page must not disclose %q", s)
	}
}

// ---------------------------------------------------------------------------
// authorization_denied — the IdP answered the authorization request with an
// error (FR-013, rest-endpoints §10). Today the callback returns a distinct
// 400 "missing code parameter" before touching the state
// (oauth_handler.go:153-157).
// ---------------------------------------------------------------------------

func TestHandleCallback_Refusal_AuthorizationDenied(t *testing.T) {
	assertDenied := func(t *testing.T, rig *refusalRig, rid, state string, w *httptest.ResponseRecorder) {
		t.Helper()
		assertGenericRefusalPage(t, w, rid)
		res := rig.results.exactlyOne(t)
		assert.EqualValues(t, "authorization_denied", res.Reason)
		assert.Equal(t, rid, res.RequestID)
		assert.EqualValues(t, "login", res.Surface)
		assert.Empty(t, res.UserID, "no identity is established on an authorization error")
		assert.Empty(t, res.EmailHash, "no identity is established on an authorization error")
		assert.False(t, rig.hasPending(state), "the pending state is consumed on the error callback")
		assert.Empty(t, rig.usersSnapshot(), "store untouched")
		assert.Zero(t, rig.sessionCount())
	}

	t.Run("error param with error_description on a live state", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		const rid = "req-denied-direct"
		authURL := rig.mustLogin(rid)
		state := authURL.Query().Get("state")
		require.NotEmpty(t, state)
		rig.results.reset()

		q := url.Values{}
		q.Set("error", "access_denied")
		q.Set("error_description", refusalIdPErrorMsg)
		q.Set("state", state)
		w := rig.callback(rid, refusalCallbackURI+"?"+q.Encode())
		assertDenied(t, rig, rid, state, w)
	})

	t.Run("fake AuthAccessDenied knob", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{AuthAccessDenied: true}, nil)
		const rid = "req-denied-knob"
		authURL := rig.mustLogin(rid)
		state := authURL.Query().Get("state")
		rig.results.reset()

		cb := rig.authorizeGET(authURL)
		require.Equal(t, "access_denied", cb.Query().Get("error"))
		require.Equal(t, state, cb.Query().Get("state"))
		w := rig.callback(rid, cb.String())
		assertDenied(t, rig, rid, state, w)
	})

	t.Run("user declines consent on the fake's form", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		const rid = "req-denied-consent"
		authURL := rig.mustLogin(rid)
		state := authURL.Query().Get("state")
		rig.results.reset()

		cb := rig.authorizeForm(authURL, "deny")
		require.Equal(t, "access_denied", cb.Query().Get("error"))
		require.Equal(t, state, cb.Query().Get("state"))
		w := rig.callback(rid, cb.String())
		assertDenied(t, rig, rid, state, w)
	})

	// Any other RFC 6749 §4.1.2.1 code on a valid state is the same refusal.
	for i, code := range []string{"invalid_request", "unauthorized_client", "unsupported_response_type",
		"invalid_scope", "server_error", "temporarily_unavailable"} {
		i, code := i, code
		t.Run("rfc6749 error="+code, func(t *testing.T) {
			rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
			rid := fmt.Sprintf("req-denied-rfc-%d", i)
			authURL := rig.mustLogin(rid)
			state := authURL.Query().Get("state")
			rig.results.reset()

			q := url.Values{}
			q.Set("error", code)
			q.Set("error_description", refusalIdPErrorMsg)
			q.Set("state", state)
			w := rig.callback(rid, refusalCallbackURI+"?"+q.Encode())
			assertDenied(t, rig, rid, state, w)
		})
	}
}

// The pending state is consumed BEFORE the response is classified: an error
// (or anything else) arriving without a live state is state_invalid, and a
// state consumed by an error callback cannot be replayed with a code.
func TestHandleCallback_Refusal_StateConsumedBeforeClassification(t *testing.T) {
	assertStateInvalid := func(t *testing.T, rig *refusalRig, rid string, w *httptest.ResponseRecorder) {
		t.Helper()
		assertGenericRefusalPage(t, w, rid)
		res := rig.results.exactlyOne(t)
		assert.EqualValues(t, "state_invalid", res.Reason)
		assert.Equal(t, rid, res.RequestID)
		assert.EqualValues(t, "login", res.Surface)
		assert.Empty(t, res.UserID)
		assert.Empty(t, res.EmailHash)
		assert.Empty(t, rig.usersSnapshot(), "store untouched")
	}

	t.Run("error with an unknown state is state_invalid, not authorization_denied", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		const rid = "req-state-unknown-error"
		q := url.Values{}
		q.Set("error", "access_denied")
		q.Set("error_description", refusalIdPErrorMsg)
		q.Set("state", "never-issued-state")
		w := rig.callback(rid, refusalCallbackURI+"?"+q.Encode())
		assertStateInvalid(t, rig, rid, w)
	})

	t.Run("error without any state is state_invalid (today: 400 missing code)", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		const rid = "req-state-missing-error"
		w := rig.callback(rid, refusalCallbackURI+"?error=access_denied")
		assertStateInvalid(t, rig, rid, w)
	})

	t.Run("code without any state is state_invalid (today: 400 missing state)", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		const rid = "req-state-missing-code"
		w := rig.callback(rid, refusalCallbackURI+"?code=some-code")
		assertStateInvalid(t, rig, rid, w)
	})

	t.Run("code with an unknown state is state_invalid (today: 400 JSON)", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		const rid = "req-state-unknown-code"
		w := rig.callback(rid, refusalCallbackURI+"?code=some-code&state=never-issued-state")
		assertStateInvalid(t, rig, rid, w)
	})

	t.Run("a state consumed by an error callback cannot be replayed with a valid code", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		const rid1 = "req-replay-1"
		const rid2 = "req-replay-2"

		// A real authorization for that state, so the code is genuinely valid.
		authURL := rig.mustLogin(rid1)
		state := authURL.Query().Get("state")
		cb := rig.authorizeForm(authURL, "approve")
		require.NotEmpty(t, cb.Query().Get("code"))
		require.Equal(t, state, cb.Query().Get("state"))
		rig.results.reset()

		// The error arrives first and consumes the state.
		q := url.Values{}
		q.Set("error", "access_denied")
		q.Set("state", state)
		w1 := rig.callback(rid1, refusalCallbackURI+"?"+q.Encode())
		assertGenericRefusalPage(t, w1, rid1)
		assert.EqualValues(t, "authorization_denied", rig.results.exactlyOne(t).Reason)
		assert.False(t, rig.hasPending(state))
		rig.results.reset()

		// The valid code on the consumed state is a replay.
		w2 := rig.callback(rid2, cb.String())
		assertStateInvalid(t, rig, rid2, w2)
	})

	t.Run("live state with neither code nor error is refused with the generic page", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		const rid = "req-state-bare"
		authURL := rig.mustLogin(rid)
		state := authURL.Query().Get("state")
		rig.results.reset()

		w := rig.callback(rid, refusalCallbackURI+"?state="+state)
		assertGenericRefusalPage(t, w, rid)
		res := rig.results.exactlyOne(t)
		assert.Contains(t, refusal403Reasons, string(res.Reason), "the reason must be one of the closed 403 vocabulary")
		assert.Equal(t, rid, res.RequestID)
		assert.False(t, rig.hasPending(state), "the state is consumed")
		assert.Empty(t, rig.usersSnapshot(), "store untouched")
	})
}

// ---------------------------------------------------------------------------
// One generic page for every refusal reason (FR-024, US2.8)
// ---------------------------------------------------------------------------

func TestHandleCallback_Refusal_OneGenericPageForEveryReason(t *testing.T) {
	type refusalCase struct {
		name   string
		reason string
		mode   oauthserver.ErrorMode
		mutate func(*config.ServerEditionOAuthConfig)
		// seed pre-creates a record; returns its id when the attempt is
		// expected to reach the store and find it (FR-013 identity rule).
		seed func(t *testing.T, store *users.UserStore) string
		// wantEmailHash: verified email known, store not consulted.
		wantEmailHash bool
		// drive: nil = approve flow; otherwise a custom driver.
		drive func(t *testing.T, rig *refusalRig, rid string) *httptest.ResponseRecorder
	}

	cases := []refusalCase{
		{
			name:   "state_invalid",
			reason: "state_invalid",
			drive: func(t *testing.T, rig *refusalRig, rid string) *httptest.ResponseRecorder {
				return rig.callback(rid, refusalCallbackURI+"?code=x&state=bogus")
			},
		},
		{
			name:   "authorization_denied",
			reason: "authorization_denied",
			drive: func(t *testing.T, rig *refusalRig, rid string) *httptest.ResponseRecorder {
				authURL := rig.mustLogin(rid)
				rig.results.reset()
				return rig.callback(rid, rig.authorizeForm(authURL, "deny").String())
			},
		},
		{name: "id_token_invalid (bad signature)", reason: "id_token_invalid", mode: oauthserver.ErrorMode{IDTokenBadSignature: true}},
		{name: "id_token_invalid (alg none)", reason: "id_token_invalid", mode: oauthserver.ErrorMode{IDTokenAlgNone: true}},
		{name: "id_token_invalid (HS256)", reason: "id_token_invalid", mode: oauthserver.ErrorMode{IDTokenHS256: true}},
		{name: "nonce_mismatch (nonce absent)", reason: "nonce_mismatch", mode: oauthserver.ErrorMode{IDTokenNoNonce: true}},
		{name: "audience_mismatch", reason: "audience_mismatch", mode: oauthserver.ErrorMode{IDTokenWrongAudience: true}},
		{name: "audience_mismatch (multi-aud without azp)", reason: "audience_mismatch", mode: oauthserver.ErrorMode{IDTokenMultiAudNoAzp: true}},
		{name: "issuer_mismatch", reason: "issuer_mismatch", mode: oauthserver.ErrorMode{IDTokenWrongIssuer: true}},
		{name: "token_expired", reason: "token_expired", mode: oauthserver.ErrorMode{IDTokenExpired: true}},
		{name: "email_unverified", reason: "email_unverified", mode: oauthserver.ErrorMode{EmailVerifiedFalse: true}},
		{
			name:   "domain_not_allowed",
			reason: "domain_not_allowed",
			mutate: func(c *config.ServerEditionOAuthConfig) { c.AllowedDomains = []string{"other.example"} },
			// The email came from a verified ID token; the store was not consulted.
			wantEmailHash: true,
		},
		{
			name:          "userinfo_subject_mismatch",
			reason:        "userinfo_subject_mismatch",
			mode:          oauthserver.ErrorMode{UserinfoSubMismatch: true},
			wantEmailHash: true,
		},
		{
			name:   "subject_mismatch",
			reason: "subject_mismatch",
			seed: func(t *testing.T, store *users.UserStore) string {
				u := users.NewUser(refusalUser, "Alice Example", "oidc", "someone-else-sub")
				require.NoError(t, store.CreateUser(u))
				return u.ID
			},
		},
		{
			name:   "user_disabled",
			reason: "user_disabled",
			seed: func(t *testing.T, store *users.UserStore) string {
				u := users.NewUser(refusalUser, "Alice Example", "oidc", refusalSub)
				u.Disabled = true
				require.NoError(t, store.CreateUser(u))
				return u.ID
			},
		},
	}

	var (
		baselineMu   sync.Mutex
		baselineBody string
		baselineName string
	)

	for i, tc := range cases {
		i, tc := i, tc
		t.Run(tc.name, func(t *testing.T) {
			rig := newOIDCRefusalRig(t, tc.mode, tc.mutate)
			// Index-based so the request id never contains a reason string
			// (the page must not carry the reason, and the rid is on the page).
			rid := fmt.Sprintf("req-generic-%02d", i)

			var wantUserID string
			if tc.seed != nil {
				wantUserID = tc.seed(t, rig.store)
			}
			before := rig.usersSnapshot()

			var w *httptest.ResponseRecorder
			if tc.drive != nil {
				w = tc.drive(t, rig, rid)
			} else {
				w = rig.approveFlow(rid)
			}

			body := assertGenericRefusalPage(t, w, rid)

			// One page for every reason: byte-identical modulo the request id.
			baselineMu.Lock()
			if baselineBody == "" {
				baselineBody, baselineName = body, tc.name
			} else {
				assert.Equal(t, baselineBody, body, "the refusal page for %s must be identical to the one for %s", tc.name, baselineName)
			}
			baselineMu.Unlock()

			// The typed result, once, keyed by the request id.
			res := rig.results.exactlyOne(t)
			assert.EqualValues(t, tc.reason, res.Reason)
			assert.Equal(t, rid, res.RequestID)
			assert.EqualValues(t, "login", res.Surface)

			// FR-013 identity rule is stage-dependent.
			assert.Equal(t, wantUserID, res.UserID, "UserID only when the attempt reached the store and a record exists")
			if tc.wantEmailHash {
				assert.Equal(t, emailHashOf(refusalUser), res.EmailHash, "verified email, store not consulted → email_hash")
			} else {
				assert.Empty(t, res.EmailHash, "no email_hash before a verified email is known or once the store was consulted")
			}

			// The store is untouched: nothing created, nothing updated.
			assert.Equal(t, before, rig.usersSnapshot(), "user store must be untouched on a refusal")
			assert.Zero(t, rig.sessionCount(), "no session on a refusal")
		})
	}
}

// ---------------------------------------------------------------------------
// The 503 class: discovery_failed / provider_error / internal_error (FR-024,
// US2.9). Readiness is a server-level route (httpapi /readyz) and is asserted
// there by the harness; at the handler level the proxy-side invariant is that
// the handler survives the outage and the next login retries.
// ---------------------------------------------------------------------------

func TestHandleLogin_Unavailable_DiscoveryFailedRenders503(t *testing.T) {
	// A discovery document advertising a plain-http, non-loopback
	// token_endpoint is rejected as discovery_failed before any redirect to
	// the IdP (FR-020): no pending state, nothing sent anywhere.
	rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{DiscoveryHTTPTokenEndpoint: true}, nil)
	const rid = "req-disc-rejected"

	w, loc := rig.login(rid)
	assert.Nil(t, loc, "no redirect to the IdP on a rejected discovery document")
	assertUnavailablePage(t, w, rid)
	res := rig.results.exactlyOne(t)
	assert.EqualValues(t, "discovery_failed", res.Reason)
	assert.Equal(t, rid, res.RequestID)
	assert.EqualValues(t, "login", res.Surface)
	assert.Empty(t, res.UserID)
	assert.Empty(t, res.EmailHash)
	assert.Zero(t, rig.pendingLen(), "a refused login allocates no pending state")
	assert.Empty(t, rig.usersSnapshot())

	// The next login retries: a rejected document is never cached.
	rig.fake.Server.SetErrorMode(oauthserver.ErrorMode{})
	rig.results.reset()
	rig.mustLogin("req-disc-recovered")
	assert.Empty(t, rig.results.all(), "a login that reaches the IdP redirect has no terminal result yet")
}

func TestHandleLogin_Unavailable_IdPUnreachableRenders503(t *testing.T) {
	// The issuer is a loopback origin nobody listens on.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	rig := newRefusalRig(t, &config.ServerEditionOAuthConfig{
		Provider:            "oidc",
		ClientID:            "client-id",
		ClientSecret:        "client-secret",
		IssuerURL:           deadURL,
		AllowInsecureIssuer: true,
		Scopes:              []string{"openid", "profile", "email"},
		GroupsClaim:         "groups",
		EmailVerifiedPolicy: "refuse_false",
	})
	const rid = "req-idp-unreachable"

	w, loc := rig.login(rid)
	assert.Nil(t, loc)
	assertUnavailablePage(t, w, rid)
	res := rig.results.exactlyOne(t)
	assert.EqualValues(t, "provider_error", res.Reason)
	assert.Equal(t, rid, res.RequestID)
	assert.Empty(t, res.UserID)
	assert.Empty(t, res.EmailHash)
	assert.Zero(t, rig.pendingLen())
	assert.Empty(t, rig.usersSnapshot())

	// The handler is still usable: a second attempt is a fresh, independent
	// terminal result (no stuck lock, no cached failure turned into a panic).
	rig.results.reset()
	w2, _ := rig.login("req-idp-unreachable-2")
	assertUnavailablePage(t, w2, "req-idp-unreachable-2")
	assert.EqualValues(t, "provider_error", rig.results.exactlyOne(t).Reason)
}

func TestHandleCallback_Unavailable_ProviderErrorRenders503(t *testing.T) {
	t.Run("token endpoint 500 during the exchange, then recovery", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		const rid = "req-token-500"

		authURL := rig.mustLogin(rid)
		cb := rig.authorizeForm(authURL, "approve")
		rig.results.reset()

		// The outage begins between the authorization and the exchange.
		rig.fake.Server.SetErrorMode(oauthserver.ErrorMode{TokenServerError: true})
		w := rig.callback(rid, cb.String())
		assertUnavailablePage(t, w, rid)
		res := rig.results.exactlyOne(t)
		assert.EqualValues(t, "provider_error", res.Reason)
		assert.Equal(t, rid, res.RequestID)
		assert.Empty(t, res.UserID)
		assert.Empty(t, res.EmailHash, "provider_error from the token exchange precedes any verified identity")
		assert.Empty(t, rig.usersSnapshot(), "store untouched")
		assert.Zero(t, rig.sessionCount())

		// The next login retries once the IdP is back.
		rig.fake.Server.SetErrorMode(oauthserver.ErrorMode{})
		rig.results.reset()
		w2 := rig.approveFlow("req-token-recovered")
		assert.Equal(t, http.StatusFound, w2.Code, "login must succeed after the outage; body=%s", w2.Body.String())
		ok := rig.results.exactlyOne(t)
		assert.EqualValues(t, "ok", ok.Reason)
		assert.NotEmpty(t, ok.UserID)
	})

	t.Run("token endpoint answers 302 (non-redirecting back channel)", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{TokenEndpointRedirect: true}, nil)
		const rid = "req-token-302"
		w := rig.approveFlow(rid)
		assertUnavailablePage(t, w, rid)
		res := rig.results.exactlyOne(t)
		assert.EqualValues(t, "provider_error", res.Reason)
		assert.Equal(t, rid, res.RequestID)
		assert.Empty(t, rig.usersSnapshot())
	})

	t.Run("userinfo unavailable after a verified ID token carries the email hash", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{UserinfoUnavailable: true}, nil)
		const rid = "req-userinfo-503"
		w := rig.approveFlow(rid)
		assertUnavailablePage(t, w, rid)
		res := rig.results.exactlyOne(t)
		assert.EqualValues(t, "provider_error", res.Reason)
		assert.Equal(t, rid, res.RequestID)
		assert.Empty(t, res.UserID, "the record is never looked up on the userinfo path")
		assert.Equal(t, emailHashOf(refusalUser), res.EmailHash, "the ID token was verified before userinfo was consulted")
		assert.Empty(t, rig.usersSnapshot(), "store untouched — a userinfo hiccup never logs a user in with []")
	})
}

// ---------------------------------------------------------------------------
// internal_error — fault injection through the T044 seams, one sub-test per
// terminal branch (user store, JWT signer, session store).
// ---------------------------------------------------------------------------

// failingLoginStore satisfies the loginStore seam and fails every upsert
// without touching the real store.
type failingLoginStore struct{}

func (failingLoginStore) UpdateUserLogin(context.Context, users.LoginClaims) (users.LoginOutcome, error) {
	return users.LoginOutcome{}, errors.New("injected: user store unavailable")
}

func (failingLoginStore) GetUserByEmail(string) (*users.User, error) {
	return nil, nil
}

// failingSessionCreator satisfies the sessionCreator seam.
type failingSessionCreator struct{}

func (failingSessionCreator) CreateSession(string, *http.Request) (*users.Session, error) {
	return nil, errors.New("injected: session store unavailable")
}

func TestHandleCallback_Unavailable_InternalErrorRenders503(t *testing.T) {
	assertInternal := func(t *testing.T, rig *refusalRig, rid string, w *httptest.ResponseRecorder) LoginResult {
		t.Helper()
		assertUnavailablePage(t, w, rid)
		res := rig.results.exactlyOne(t)
		assert.EqualValues(t, "internal_error", res.Reason)
		assert.Equal(t, rid, res.RequestID)
		assert.EqualValues(t, "login", res.Surface)
		assert.Zero(t, rig.sessionCount(), "no session survives an internal error")
		return res
	}

	t.Run("failing loginStore: no user record created before the upsert commits", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		rig.handler.loginStore = failingLoginStore{}
		const rid = "req-internal-store"

		w := rig.approveFlow(rid)
		res := assertInternal(t, rig, rid, w)
		assert.Empty(t, res.UserID, "no record exists when the upsert failed")
		assert.Empty(t, res.EmailHash, "the store was already consulted (and failed); FR-013 reserves email_hash for reasons where the store was not yet consulted (cross-review round 8, chunk 2 P2)")

		u, err := rig.store.GetUserByEmail(refusalUser)
		require.NoError(t, err)
		assert.Nil(t, u, "the handler must write only through the loginStore seam — no record before the upsert commits")
		assert.Empty(t, rig.usersSnapshot())
	})

	t.Run("failing bearerSigner: record exists, no session, no cookie", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		rig.handler.bearerSigner = func([]byte, string, string, string, string, string, time.Duration) (string, error) {
			return "", errors.New("injected: signer unavailable")
		}
		const rid = "req-internal-signer"

		w := rig.approveFlow(rid)
		res := assertInternal(t, rig, rid, w)

		u, err := rig.store.GetUserByEmail(refusalUser)
		require.NoError(t, err)
		require.NotNil(t, u, "the upsert committed before the signer ran")
		assert.Equal(t, u.ID, res.UserID, "the attempt reached the store and a record exists → UserID")
		assert.Empty(t, res.EmailHash)
	})

	t.Run("failing sessionCreator: record exists, no session, no cookie", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		rig.handler.sessionCreator = failingSessionCreator{}
		const rid = "req-internal-session"

		w := rig.approveFlow(rid)
		res := assertInternal(t, rig, rid, w)

		u, err := rig.store.GetUserByEmail(refusalUser)
		require.NoError(t, err)
		require.NotNil(t, u, "the upsert committed before the session was created")
		assert.Equal(t, u.ID, res.UserID)
		assert.Empty(t, res.EmailHash)
	})

	// The bearer-token-bearing write is a second, separate persist
	// (sessionCreator.CreateSession already durably wrote a bearer-less row);
	// when it fails, that row must not survive as an orphan until its TTL
	// (cross-review round 3, internal/serveredition/auth/oauth_handler.go).
	t.Run("failing sessionPersist: the bearer-less row is cleaned up, not orphaned", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		var capturedID string
		rig.handler.sessionPersist = func(s *users.Session) error {
			capturedID = s.ID
			return errors.New("injected: session persistence unavailable")
		}
		const rid = "req-internal-session-persist"

		w := rig.approveFlow(rid)
		res := assertInternal(t, rig, rid, w)

		u, err := rig.store.GetUserByEmail(refusalUser)
		require.NoError(t, err)
		require.NotNil(t, u, "the upsert committed before the session was created")
		assert.Equal(t, u.ID, res.UserID)

		require.NotEmpty(t, capturedID)
		orphan, err := rig.store.GetSession(capturedID)
		require.NoError(t, err)
		assert.Nil(t, orphan, "the bearer-less row from sessionCreator must be deleted, not left until TTL")
	})
}

// ---------------------------------------------------------------------------
// Role derived live from the CURRENT admin_emails (T044; #1169's remaining
// horizon). The bearerSigner seam captures the role the handler minted.
// ---------------------------------------------------------------------------

func TestHandleCallback_RoleDerivedFromLiveAdminEmails(t *testing.T) {
	rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)

	var (
		rolesMu sync.Mutex
		roles   []string
	)
	rig.handler.bearerSigner = func(hmacKey []byte, userID, email, displayName, role, provider string, ttl time.Duration) (string, error) {
		rolesMu.Lock()
		roles = append(roles, role)
		rolesMu.Unlock()
		return GenerateBearerToken(hmacKey, userID, email, displayName, role, provider, ttl)
	}

	// Boot block: alice is not an administrator.
	w1 := rig.approveFlow("req-role-user")
	require.Equal(t, http.StatusFound, w1.Code, "login must succeed; body=%s", w1.Body.String())
	assert.EqualValues(t, "ok", rig.results.exactlyOne(t).Reason)

	// Hot reload: a NEW block (not a mutation of the boot pointer) promotes her.
	boot := rig.live.get()
	promoted := refusalServerEditionConfig(boot.OAuth)
	promoted.AdminEmails = []string{refusalUser}
	rig.live.swap(promoted)
	rig.results.reset()

	w2 := rig.approveFlow("req-role-admin")
	require.Equal(t, http.StatusFound, w2.Code, "login must succeed; body=%s", w2.Body.String())
	assert.EqualValues(t, "ok", rig.results.exactlyOne(t).Reason)

	// And a demotion takes effect on the very next login.
	rig.live.swap(boot)
	rig.results.reset()
	w3 := rig.approveFlow("req-role-demoted")
	require.Equal(t, http.StatusFound, w3.Code)

	rolesMu.Lock()
	defer rolesMu.Unlock()
	assert.Equal(t, []string{"user", "admin", "user"}, roles, "the role is derived from the live admin_emails at each login")
}

// ---------------------------------------------------------------------------
// pendingStates is bounded at 10,000 entries (FR-021): insert 10,001, the
// first is gone, and the login that used it fails state_invalid.
// ---------------------------------------------------------------------------

func TestHandleLogin_PendingStatesCappedAtTenThousand(t *testing.T) {
	// The legacy mock keeps HandleLogin network-free so 10,001 calls are cheap.
	idp := newLegacyMockIdP(t, "google", refusalUser, "Alice Example", refusalSub)
	registerLegacyMock(t, "google", idp)
	rig := newRefusalRig(t, &config.ServerEditionOAuthConfig{
		Provider:     "google",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	})

	var firstState string
	for i := 0; i <= pendingStatesCap; i++ { // 10,001 inserts
		loc := rig.mustLogin(fmt.Sprintf("req-cap-%d", i))
		if i == 0 {
			firstState = loc.Query().Get("state")
			require.NotEmpty(t, firstState)
		}
	}

	assert.Equal(t, pendingStatesCap, rig.pendingLen(), "the map never exceeds the cap")
	assert.False(t, rig.hasPending(firstState), "the oldest state is evicted on the 10,001st insert")

	rig.results.reset()
	const rid = "req-cap-evicted-login"
	w := rig.callback(rid, refusalCallbackURI+"?code=some-code&state="+firstState)
	assertGenericRefusalPage(t, w, rid)
	res := rig.results.exactlyOne(t)
	assert.EqualValues(t, "state_invalid", res.Reason)
	assert.Equal(t, rid, res.RequestID)
	assert.Empty(t, rig.usersSnapshot(), "store untouched")
	assert.Empty(t, idp.paths(), "an evicted state never reaches the IdP")
}

// ---------------------------------------------------------------------------
// Legacy providers unchanged (US2.10): no nonce, no JWKS, client_secret_post,
// stored groups []. Registered through the widened T042 factory signature.
// ---------------------------------------------------------------------------

// legacyMockIdP records every request the handler makes to a legacy provider.
type legacyMockIdP struct {
	srv *httptest.Server

	mu              sync.Mutex
	seenPaths       []string
	tokenForm       url.Values
	tokenAuthHeader string
}

func newLegacyMockIdP(t *testing.T, provider, email, name, sub string) *legacyMockIdP {
	t.Helper()
	m := &legacyMockIdP{}
	mux := http.NewServeMux()

	record := func(r *http.Request) {
		m.mu.Lock()
		m.seenPaths = append(m.seenPaths, r.URL.Path)
		m.mu.Unlock()
	}

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		require.NoError(t, r.ParseForm())
		m.mu.Lock()
		m.tokenForm = r.PostForm
		m.tokenAuthHeader = r.Header.Get("Authorization")
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	switch provider {
	case "github":
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			record(r)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 4242, "login": "alice", "name": name, "email": email, "avatar_url": "https://example.com/a.png",
			})
		})
		mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
			record(r)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"email": email, "primary": true, "verified": true}})
		})
	default:
		mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
			record(r)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub": sub, "email": email, "name": name, "picture": "https://example.com/a.png",
			})
		})
	}

	// Anything else — a JWKS or discovery fetch in particular — is recorded
	// and refused.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		http.NotFound(w, r)
	})

	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *legacyMockIdP) paths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.seenPaths))
	copy(out, m.seenPaths)
	return out
}

func (m *legacyMockIdP) token() (url.Values, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokenForm, m.tokenAuthHeader
}

// registerLegacyMock swaps a legacy factory for one pointing at the mock,
// keeping the provider's own shape (GitHub: no OIDC/PKCE, emails endpoint).
func registerLegacyMock(t *testing.T, name string, idp *legacyMockIdP) {
	t.Helper()
	original, had := providerRegistry[name]
	require.True(t, had, "legacy provider %q must exist in the registry", name)

	providerRegistry[name] = func(_ *config.ServerEditionOAuthConfig) *OAuthProvider {
		p := &OAuthProvider{
			Name:         name,
			AuthURL:      idp.srv.URL + "/authorize",
			TokenURL:     idp.srv.URL + "/token",
			UserInfoURL:  idp.srv.URL + "/userinfo",
			Scopes:       []string{"openid", "email", "profile"},
			SupportsOIDC: true,
			SupportsPKCE: true,
		}
		if name == "github" {
			p.UserInfoURL = idp.srv.URL + "/user"
			p.EmailsURL = idp.srv.URL + "/user/emails"
			p.Scopes = []string{"user:email", "read:user"}
			p.SupportsOIDC = false
			p.SupportsPKCE = false
		}
		return p
	}
	t.Cleanup(func() { providerRegistry[name] = original })
}

func TestLegacyProviders_FrontDoorUnchanged(t *testing.T) {
	for _, provider := range []string{"google", "github", "microsoft"} {
		provider := provider
		t.Run(provider, func(t *testing.T) {
			idp := newLegacyMockIdP(t, provider, refusalUser, "Alice Example", refusalSub)
			registerLegacyMock(t, provider, idp)

			oauthCfg := &config.ServerEditionOAuthConfig{
				Provider:     provider,
				ClientID:     "client-id",
				ClientSecret: "legacy-client-secret",
			}
			if provider == "microsoft" {
				oauthCfg.TenantID = "common"
			}
			rig := newRefusalRig(t, oauthCfg)
			rid := "req-legacy-" + provider

			// No nonce on the authorization request (no OIDC verification step).
			authURL := rig.mustLogin(rid)
			assert.Empty(t, authURL.Query().Get("nonce"), "legacy providers send no nonce")
			assert.Equal(t, "client-id", authURL.Query().Get("client_id"))
			state := authURL.Query().Get("state")
			require.NotEmpty(t, state)
			assert.Empty(t, idp.paths(), "login makes no discovery or JWKS request for a legacy provider")

			// The provider's callback, as the browser would deliver it.
			w := rig.callback(rid, refusalCallbackURI+"?code=legacy-code&state="+state)
			require.Equal(t, http.StatusFound, w.Code, "legacy login must succeed; body=%s", w.Body.String())
			assert.Equal(t, "/ui/", w.Header().Get("Location"))
			require.NotEmpty(t, w.Result().Cookies(), "a session cookie is set")

			// client_secret_post, never Basic; no JWKS/discovery on the back channel.
			form, authHeader := idp.token()
			assert.Equal(t, "legacy-client-secret", form.Get("client_secret"), "client_secret_post carries the secret in the form")
			assert.Equal(t, "legacy-code", form.Get("code"))
			assert.Empty(t, authHeader, "legacy providers never use client_secret_basic")
			for _, p := range idp.paths() {
				assert.Contains(t, []string{"/token", "/userinfo", "/user", "/user/emails"}, p, "no JWKS or discovery request for a legacy provider")
			}

			// Stored groups are [] (never nil) and the login is `ok` with the record id.
			u, err := rig.store.GetUserByEmail(refusalUser)
			require.NoError(t, err)
			require.NotNil(t, u)
			assert.Equal(t, provider, u.Provider)
			assert.NotEmpty(t, u.ProviderSubjectID)
			assert.NotNil(t, u.Groups, "legacy providers store [] — an explicit empty list, not an upgraded-record nil")
			assert.Empty(t, u.Groups)
			assert.False(t, u.GroupsUpdatedAt.IsZero(), "the login write stamps groups_updated_at")

			res := rig.results.exactlyOne(t)
			assert.EqualValues(t, "ok", res.Reason)
			assert.Equal(t, rid, res.RequestID)
			assert.Equal(t, u.ID, res.UserID)
		})
	}
}
