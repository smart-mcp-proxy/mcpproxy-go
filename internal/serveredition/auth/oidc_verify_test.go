//go:build server

package auth

// Spec 107 T037 (PR-B) — the generic `oidc` provider's verified ID token
// (FR-021), claims (FR-022) and groups capture (FR-008 capture half), driven
// end to end through HandleLogin → the in-process tests/oauthserver fake IdP
// (headless form POST) → HandleCallback. Every case is driven through the
// fake's ErrorMode knob of research D2 / T033 or its Options.UserClaims; the
// one case with no knob (a wrong nonce) tampers with the nonce the handler
// stored beside the state. No case is synthesised against a second fake.
//
// Compile-red until T043 (the provider: OAuthProvider.BuildAuthURL emits
// `nonce`, ExchangeCode + verified ID token via the JWKS, groups from the token
// or /userinfo after the `sub` comparison; OAuthUserInfo.Groups/EmailVerified)
// AND T044 (the handler: NewOAuthHandler taking a ServerEditionConfigProvider,
// oauthState.Nonce, LoginResult/LoginRefusal/LoginResultObserver, the generic
// 403/503 pages, users.User.Groups/GroupsUpdatedAt written through
// UpdateUserLogin), on top of T041 (config keys; users.User.Validate admits
// `oidc`) and T042 (the provider is constructed once in NewOAuthHandler).
//
// Conventions shared with oauth_handler_subject_test.go (T038): the closed
// reason is compared as its wire value (`string(res.Reason)`, FR-013), never
// by constant name; flags likewise.
//
// Observation of the back channel (JWKS/userinfo request counts, the raw
// id_token for "never logged", the one mid-flow re-knob) goes through
// http.DefaultTransport, so the provider's non-redirecting 10 s client (T043)
// must leave Transport nil — the stdlib default — exactly like today's
// package-level httpClient in oauth_providers.go.

import (
	"bytes"
	"context"
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
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

const (
	oidcVerifyEmail    = "alice@example.com"
	oidcVerifyPassword = "pass"
	oidcVerifySub      = "alice-sub-1"
	oidcVerifyName     = "Alice Example"
	oidcVerifyHost     = "127.0.0.1"
	oidcVerifyCallback = "http://127.0.0.1/api/v1/auth/callback"
	oidcVerifyHMACKey  = "test-hmac-key-for-jwt-signing-32b"
	oidcVerifyGroups   = "groups"
)

// oidcVerifyClaims are alice's identity claims at the fake IdP (the id_token
// and /userinfo carry the same set).
func oidcVerifyClaims() map[string]any {
	return map[string]any{
		"sub":            oidcVerifySub,
		"email":          oidcVerifyEmail,
		"email_verified": true,
		"name":           oidcVerifyName,
		"groups":         []any{"eng", "sre"},
	}
}

// startOIDCVerifyIdP starts the fake OpenID Provider with alice's claims.
func startOIDCVerifyIdP(t *testing.T, claims map[string]any) *oauthserver.ServerResult {
	t.Helper()
	srv := oauthserver.Start(t, oauthserver.Options{
		OIDC:               true,
		ValidUsers:         map[string]string{oidcVerifyEmail: oidcVerifyPassword},
		UserClaims:         map[string]map[string]any{oidcVerifyEmail: claims},
		ClientRedirectURIs: []string{oidcVerifyCallback},
		GroupsClaim:        oidcVerifyGroups,
	})
	t.Cleanup(func() { _ = srv.Shutdown() })
	return srv
}

// oidcVerifyConfig is the `provider: oidc` block pointing at the fake, with
// every PR-B key set explicitly (ApplyDefaults is a setup.go concern).
func oidcVerifyConfig(idp *oauthserver.ServerResult) *config.ServerEditionConfig {
	return &config.ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &config.ServerEditionOAuthConfig{
			Provider:            "oidc",
			IssuerURL:           idp.IssuerURL,
			AllowInsecureIssuer: true, // loopback http fake
			ClientID:            idp.ClientID,
			ClientSecret:        idp.ClientSecret,
			Scopes:              []string{"openid", "profile", "email", "groups"},
			GroupsClaim:         oidcVerifyGroups,
			EmailVerifiedPolicy: "refuse_false",
		},
		SessionTTL:     config.Duration(time.Hour),
		BearerTokenTTL: config.Duration(time.Hour),
	}
}

// backChannelTap wraps http.DefaultTransport for one rig so the test can see
// the provider's back-channel requests to the fake: count them, capture the
// tokens the token endpoint returned (to assert they are never logged), fail
// one of them at the transport level, or re-knob the fake between the token
// exchange and the userinfo fetch of a single callback.
type backChannelTap struct {
	next http.RoundTripper
	idp  *oauthserver.ServerResult

	mu             sync.Mutex
	discovery      int
	jwks           int
	token          int
	userinfo       int
	idTokens       []string // raw id_tokens the token endpoint returned
	accessTokens   []string
	failUserinfo   error                  // returned instead of forwarding /userinfo
	beforeUserinfo *oauthserver.ErrorMode // SetErrorMode before forwarding /userinfo
}

func (tap *backChannelTap) RoundTrip(req *http.Request) (*http.Response, error) {
	tap.mu.Lock()
	var (
		fail   error
		reknob *oauthserver.ErrorMode
	)
	switch req.URL.Path {
	case "/.well-known/openid-configuration":
		tap.discovery++
	case "/jwks.json":
		tap.jwks++
	case "/token":
		tap.token++
	case "/userinfo":
		tap.userinfo++
		fail = tap.failUserinfo
		reknob = tap.beforeUserinfo
	}
	tap.mu.Unlock()

	if fail != nil {
		return nil, fail
	}
	if reknob != nil {
		tap.idp.Server.SetErrorMode(*reknob)
	}

	resp, err := tap.next.RoundTrip(req)
	if err != nil || req.URL.Path != "/token" || resp.StatusCode != http.StatusOK {
		return resp, err
	}

	// Capture the tokens without disturbing the provider's read of the body.
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var tok struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(body, &tok) == nil {
		tap.mu.Lock()
		if tok.IDToken != "" {
			tap.idTokens = append(tap.idTokens, tok.IDToken)
		}
		if tok.AccessToken != "" {
			tap.accessTokens = append(tap.accessTokens, tok.AccessToken)
		}
		tap.mu.Unlock()
	}
	return resp, nil
}

func (tap *backChannelTap) counts() (discovery, jwks, token, userinfo int) {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	return tap.discovery, tap.jwks, tap.token, tap.userinfo
}

func (tap *backChannelTap) resetCounts() {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	tap.discovery, tap.jwks, tap.token, tap.userinfo = 0, 0, 0, 0
}

func (tap *backChannelTap) tokens() []string {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	out := append([]string(nil), tap.idTokens...)
	return append(out, tap.accessTokens...)
}

// oidcVerifyRig is one handler + one fresh store over the shared fake IdP,
// with a LoginResultObserver recording every terminal result and an observed
// zap core capturing what the handler logs.
type oidcVerifyRig struct {
	t       *testing.T
	idp     *oauthserver.ServerResult
	cfg     *config.ServerEditionConfig
	store   *users.UserStore
	handler *OAuthHandler
	logs    *observer.ObservedLogs
	tap     *backChannelTap
	browser *http.Client // the user's browser towards the fake; never counted

	resultsMu sync.Mutex
	results   []LoginResult

	lastNonce string // nonce of the most recent HandleLogin
}

// newOIDCVerifyRig builds a rig over idp. mode is installed on the fake for
// the rig's lifetime and cleared on cleanup; mutate may adjust the config
// before the handler is constructed.
func newOIDCVerifyRig(t *testing.T, idp *oauthserver.ServerResult, mode oauthserver.ErrorMode, mutate func(*config.ServerEditionConfig)) *oidcVerifyRig {
	t.Helper()

	idp.Server.SetErrorMode(mode)
	t.Cleanup(func() { idp.Server.SetErrorMode(oauthserver.ErrorMode{}) })

	db, err := bbolt.Open(filepath.Join(t.TempDir(), "oidc-verify.db"), 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	cfg := oidcVerifyConfig(idp)
	if mutate != nil {
		mutate(cfg)
	}

	core, logs := observer.New(zapcore.DebugLevel)
	sessionMgr := NewSessionManager(store, time.Hour, false)
	handler := NewOAuthHandler(store, sessionMgr, StaticServerEditionConfig(cfg), []byte(oidcVerifyHMACKey), zap.New(core).Sugar())

	rig := &oidcVerifyRig{t: t, idp: idp, cfg: cfg, store: store, handler: handler, logs: logs}
	handler.LoginResultObserver = func(res LoginResult) {
		rig.resultsMu.Lock()
		defer rig.resultsMu.Unlock()
		rig.results = append(rig.results, res)
	}

	// Observe the provider's back channel; the browser client bypasses the tap.
	orig := http.DefaultTransport
	rig.tap = &backChannelTap{next: orig, idp: idp}
	http.DefaultTransport = rig.tap
	t.Cleanup(func() { http.DefaultTransport = orig })
	rig.browser = &http.Client{
		Transport: orig,
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return rig
}

// login drives GET /api/v1/auth/login and returns the authorization URL the
// handler redirected to (state, nonce, PKCE and redirect_uri included).
func (rig *oidcVerifyRig) login() *url.URL {
	rig.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login?redirect_uri=/ui/", nil)
	req.Host = oidcVerifyHost
	w := httptest.NewRecorder()
	rig.handler.HandleLogin(w, req)
	require.Equal(rig.t, http.StatusFound, w.Code, "login must redirect to the IdP; body: %s", w.Body.String())

	authURL, err := url.Parse(w.Header().Get("Location"))
	require.NoError(rig.t, err)
	q := authURL.Query()
	require.Equal(rig.t, rig.idp.AuthorizationEndpoint, authURL.Scheme+"://"+authURL.Host+authURL.Path, "the discovered authorization_endpoint")
	require.NotEmpty(rig.t, q.Get("state"))
	require.NotEmpty(rig.t, q.Get("nonce"), "an oidc login carries a per-login nonce (FR-021)")
	require.Equal(rig.t, oidcVerifyCallback, q.Get("redirect_uri"))
	require.Equal(rig.t, "S256", q.Get("code_challenge_method"))
	rig.lastNonce = q.Get("nonce")
	return authURL
}

// approve submits the fake's headless login form as alice and returns the
// redirect back to the proxy's callback.
func (rig *oidcVerifyRig) approve(authURL *url.URL) *url.URL {
	rig.t.Helper()
	form := authURL.Query()
	form.Set("username", oidcVerifyEmail)
	form.Set("password", oidcVerifyPassword)
	form.Set("consent", "on")
	form.Set("action", "approve")
	resp, err := rig.browser.PostForm(rig.idp.AuthorizationEndpoint, form)
	require.NoError(rig.t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(rig.t, http.StatusFound, resp.StatusCode, "the fake must redirect back with a code")
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(rig.t, err)
	require.NotEmpty(rig.t, loc.Query().Get("code"))
	return loc
}

// callback delivers the IdP's redirect to HandleCallback.
func (rig *oidcVerifyRig) callback(loc *url.URL) *http.Response {
	rig.t.Helper()
	req := httptest.NewRequest(http.MethodGet, loc.String(), nil)
	req.Host = oidcVerifyHost
	w := httptest.NewRecorder()
	rig.handler.HandleCallback(w, req)
	resp := w.Result()
	rig.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// attempt runs one full login attempt: login → approve → callback.
func (rig *oidcVerifyRig) attempt() (*http.Response, LoginResult) {
	rig.t.Helper()
	resp := rig.callback(rig.approve(rig.login()))
	return resp, rig.lastResult()
}

// lastResult returns the single LoginResult reported since the previous call.
func (rig *oidcVerifyRig) lastResult() LoginResult {
	rig.t.Helper()
	rig.resultsMu.Lock()
	defer rig.resultsMu.Unlock()
	require.Len(rig.t, rig.results, 1, "exactly one terminal LoginResult per attempt, got %d", len(rig.results))
	res := rig.results[0]
	rig.results = nil
	return res
}

func (rig *oidcVerifyRig) pendingState(state string) (*oauthState, bool) {
	rig.handler.statesMu.Lock()
	defer rig.handler.statesMu.Unlock()
	s, ok := rig.handler.pendingStates[state]
	return s, ok
}

// seedAlice stores a record for alice bound to (oidc, alice-sub-1) with a
// known group list and timestamps in the past, so "untouched" is observable.
func (rig *oidcVerifyRig) seedAlice(groups []string) *users.User {
	rig.t.Helper()
	u := users.NewUser(oidcVerifyEmail, oidcVerifyName, "oidc", oidcVerifySub)
	past := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	u.CreatedAt = past
	u.LastLoginAt = past
	u.Groups = groups
	u.GroupsUpdatedAt = past
	require.NoError(rig.t, rig.store.CreateUser(u))
	return rig.alice()
}

func (rig *oidcVerifyRig) alice() *users.User {
	rig.t.Helper()
	u, err := rig.store.GetUserByEmail(oidcVerifyEmail)
	require.NoError(rig.t, err)
	return u
}

func (rig *oidcVerifyRig) userCount() int {
	rig.t.Helper()
	all, err := rig.store.ListUsers()
	require.NoError(rig.t, err)
	return len(all)
}

// logText joins every observed entry at or above level: message plus the
// structured context, so a claim value smuggled into a field is caught too.
func (rig *oidcVerifyRig) logText(min zapcore.Level) string {
	var b strings.Builder
	for _, e := range rig.logs.All() {
		if e.Level < min {
			continue
		}
		b.WriteString(e.Message)
		for k, v := range e.ContextMap() {
			fmt.Fprintf(&b, " %s=%v", k, v)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// assertLogNamesCheck: at least one WARN+ line carries the closed reason and
// one of the words naming the failed check.
func (rig *oidcVerifyRig) assertLogNamesCheck(reason string, checkWords ...string) {
	rig.t.Helper()
	text := strings.ToLower(rig.logText(zapcore.WarnLevel))
	assert.Contains(rig.t, text, reason, "the refusal log line carries the closed reason")
	named := false
	for _, w := range checkWords {
		if strings.Contains(text, strings.ToLower(w)) {
			named = true
			break
		}
	}
	assert.True(rig.t, named, "the refusal log line names the failed check (one of %v); got:\n%s", checkWords, text)
}

// assertLogNeverCarriesClaims: no line at any level carries a claim value, a
// token or the client secret.
func (rig *oidcVerifyRig) assertLogNeverCarriesClaims() {
	rig.t.Helper()
	text := rig.logText(zapcore.DebugLevel)
	forbidden := map[string]string{
		"email":         oidcVerifyEmail,
		"sub":           oidcVerifySub,
		"name":          oidcVerifyName,
		"nonce":         rig.lastNonce,
		"client_secret": rig.idp.ClientSecret,
	}
	for what, v := range forbidden {
		if v == "" {
			continue
		}
		assert.NotContains(rig.t, text, v, "log must never carry the %s claim value", what)
	}
	for _, tok := range rig.tap.tokens() {
		assert.NotContains(rig.t, text, tok, "log must never carry a token")
	}
}

func oidcFlags(res LoginResult) []string {
	out := []string{}
	for _, f := range res.Flags {
		out = append(out, string(f))
	}
	return out
}

// --- FR-021: the tamper matrix ------------------------------------------------
//
// Each case: one knob (or the nonce tampered in the pending state), one closed
// reason, the generic 403, the user store untouched (alice pre-seeded so an
// update would be visible), the state consumed, and a log line naming the
// check but never a claim value. The control row proves the seed and the
// flow are sound: without a knob the same attempt succeeds and rewrites the
// record.

func TestOIDCVerify_TamperMatrix(t *testing.T) {
	idp := startOIDCVerifyIdP(t, oidcVerifyClaims())

	cases := []struct {
		name       string
		mode       oauthserver.ErrorMode
		wrongNonce bool
		reason     string
		checkWords []string
	}{
		{name: "control (no tamper)", reason: "ok"},
		{name: "bad signature", mode: oauthserver.ErrorMode{IDTokenBadSignature: true}, reason: "id_token_invalid", checkWords: []string{"signature"}},
		{name: "wrong iss", mode: oauthserver.ErrorMode{IDTokenWrongIssuer: true}, reason: "issuer_mismatch", checkWords: []string{"iss"}},
		{name: "wrong aud", mode: oauthserver.ErrorMode{IDTokenWrongAudience: true}, reason: "audience_mismatch", checkWords: []string{"aud"}},
		{name: "multi-aud without matching azp", mode: oauthserver.ErrorMode{IDTokenMultiAudNoAzp: true}, reason: "audience_mismatch", checkWords: []string{"azp", "aud"}},
		{name: "expired", mode: oauthserver.ErrorMode{IDTokenExpired: true}, reason: "token_expired", checkWords: []string{"exp"}},
		{name: "nbf in the future", mode: oauthserver.ErrorMode{IDTokenNbfFuture: true}, reason: "id_token_invalid", checkWords: []string{"nbf", "not valid yet", "not before"}},
		{name: "wrong nonce", wrongNonce: true, reason: "nonce_mismatch", checkWords: []string{"nonce"}},
		{name: "missing nonce", mode: oauthserver.ErrorMode{IDTokenNoNonce: true}, reason: "nonce_mismatch", checkWords: []string{"nonce"}},
		{name: "alg none", mode: oauthserver.ErrorMode{IDTokenAlgNone: true}, reason: "id_token_invalid", checkWords: []string{"alg", "signing method", "none"}},
		{name: "HS256", mode: oauthserver.ErrorMode{IDTokenHS256: true}, reason: "id_token_invalid", checkWords: []string{"alg", "signing method", "hs256"}},
		{name: "unknown kid", mode: oauthserver.ErrorMode{IDTokenUnknownKid: true}, reason: "id_token_invalid", checkWords: []string{"kid", "key"}},
		{name: "key-type/alg mismatch", mode: oauthserver.ErrorMode{IDTokenKeyAlgMismatch: true}, reason: "id_token_invalid", checkWords: []string{"alg", "kid", "key", "signing method"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newOIDCVerifyRig(t, idp, tc.mode, nil)
			seeded := rig.seedAlice([]string{"ops"})

			authURL := rig.login()
			state := authURL.Query().Get("state")
			if tc.wrongNonce {
				// The only case without a knob: the handler stored the nonce
				// beside the state; make the stored one disagree with the
				// nonce the IdP will echo.
				pending, ok := rig.pendingState(state)
				require.True(t, ok)
				require.Equal(t, rig.lastNonce, pending.Nonce, "the nonce sent to the IdP is the one stored beside the state")
				pending.Nonce = "tampered-" + pending.Nonce
			}
			resp := rig.callback(rig.approve(authURL))
			res := rig.lastResult()

			assert.Equal(t, tc.reason, string(res.Reason))
			_, stillPending := rig.pendingState(state)
			assert.False(t, stillPending, "the state (and its nonce) is consumed on every callback")

			if tc.reason == "ok" {
				require.Equal(t, http.StatusFound, resp.StatusCode)
				assert.Equal(t, "/ui/", resp.Header.Get("Location"))
				after := rig.alice()
				require.NotNil(t, after)
				assert.Equal(t, seeded.ID, after.ID)
				assert.Equal(t, seeded.ID, res.UserID)
				assert.Equal(t, []string{"eng", "sre"}, after.Groups, "the control login rewrites the seed's groups from the verified token")
				assert.True(t, after.LastLoginAt.After(seeded.LastLoginAt), "the control login advances last_login")
				assert.Equal(t, 1, rig.userCount())
				return
			}

			assert.Equal(t, http.StatusForbidden, resp.StatusCode, "every verification failure renders the one generic 403 page")
			assert.Empty(t, resp.Cookies(), "no session cookie on a refused login")
			assert.Empty(t, res.UserID, "the store is never consulted for an unverified token")
			assert.Empty(t, res.EmailHash, "an unverified claim is never hashed")

			after := rig.alice()
			require.NotNil(t, after)
			assert.Equal(t, seeded, after, "user store untouched: the seeded record is byte-for-byte what it was")
			assert.Equal(t, 1, rig.userCount(), "no record created")

			rig.assertLogNamesCheck(tc.reason, tc.checkWords...)
			rig.assertLogNeverCarriesClaims()
		})
	}
}

// Unknown kid → exactly one JWKS refetch, then refuse. Caches are warmed by a
// successful login first so the count is the refetch alone; the control shows
// a warm cache serves a valid login with no JWKS fetch at all.
func TestOIDCVerify_UnknownKidRefetchesJWKSOnce(t *testing.T) {
	idp := startOIDCVerifyIdP(t, oidcVerifyClaims())
	rig := newOIDCVerifyRig(t, idp, oauthserver.ErrorMode{}, nil)

	// Warm: discovery + JWKS.
	resp, res := rig.attempt()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "ok", string(res.Reason))
	_, jwks, _, _ := rig.tap.counts()
	require.GreaterOrEqual(t, jwks, 1, "the first verification fetched the JWKS")

	// Control: warm caches, valid token → no JWKS fetch.
	rig.tap.resetCounts()
	resp, res = rig.attempt()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "ok", string(res.Reason))
	discovery, jwks, _, _ := rig.tap.counts()
	assert.Equal(t, 0, jwks, "a known kid is served from the cache")
	assert.Equal(t, 0, discovery, "discovery is cached across logins (T036/T042)")

	// Unknown kid: one refetch, then refuse.
	idp.Server.SetErrorMode(oauthserver.ErrorMode{IDTokenUnknownKid: true})
	rig.tap.resetCounts()
	resp, res = rig.attempt()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "id_token_invalid", string(res.Reason))
	_, jwks, _, _ = rig.tap.counts()
	assert.Equal(t, 1, jwks, "exactly one JWKS refetch per unknown-kid attempt")

	// The refetch did not poison the cache: the real kid still verifies.
	idp.Server.SetErrorMode(oauthserver.ErrorMode{})
	resp, res = rig.attempt()
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "ok", string(res.Reason))
}

// --- FR-022 / US2.4: email_verified_policy × {true, false, absent} ------------

func TestOIDCVerify_EmailVerifiedPolicy(t *testing.T) {
	withTrue := startOIDCVerifyIdP(t, oidcVerifyClaims())

	// "absent": the fake derives email_verified=true when the key is missing,
	// so the claim is set to JSON null, which decodes to a nil *bool exactly
	// like an absent claim (OAuthUserInfo.EmailVerified *bool, data-model §7).
	absentClaims := oidcVerifyClaims()
	absentClaims["email_verified"] = nil
	withAbsent := startOIDCVerifyIdP(t, absentClaims)

	type claimState struct {
		name string
		idp  *oauthserver.ServerResult
		mode oauthserver.ErrorMode
	}
	states := []claimState{
		{name: "true", idp: withTrue},
		{name: "false", idp: withTrue, mode: oauthserver.ErrorMode{EmailVerifiedFalse: true}},
		{name: "absent", idp: withAbsent},
	}
	// policy → claim state → login allowed?
	expect := map[string]map[string]bool{
		"refuse_false": {"true": true, "false": false, "absent": true},
		"require_true": {"true": true, "false": false, "absent": false},
		"ignore":       {"true": true, "false": true, "absent": true},
	}

	for _, policy := range []string{"refuse_false", "require_true", "ignore"} {
		for _, st := range states {
			allowed := expect[policy][st.name]
			t.Run(fmt.Sprintf("%s/email_verified=%s", policy, st.name), func(t *testing.T) {
				rig := newOIDCVerifyRig(t, st.idp, st.mode, func(c *config.ServerEditionConfig) {
					c.OAuth.EmailVerifiedPolicy = policy
				})

				resp, res := rig.attempt()

				if allowed {
					assert.Equal(t, http.StatusFound, resp.StatusCode)
					assert.Equal(t, "ok", string(res.Reason))
					u := rig.alice()
					require.NotNil(t, u, "the login created the record")
					assert.Equal(t, u.ID, res.UserID)
					assert.Equal(t, "oidc", u.Provider)
					assert.Equal(t, oidcVerifySub, u.ProviderSubjectID)
					return
				}

				assert.Equal(t, http.StatusForbidden, resp.StatusCode, "the generic 403 page")
				assert.Equal(t, "email_unverified", string(res.Reason))
				assert.Empty(t, res.UserID)
				assert.Empty(t, res.EmailHash, "email_unverified carries neither user_id nor email_hash (FR-013)")
				assert.Equal(t, 0, rig.userCount(), "user store untouched: no record created")
				assert.Nil(t, rig.alice())
				rig.assertLogNamesCheck("email_unverified", "email_verified")
			})
		}
	}
}

// A present-but-non-boolean email_verified (e.g. a string) must never be
// treated as absent: under the default refuse_false policy that would let a
// malformed claim whose plain-text value is "false" log the user in anyway.
// It is refused as id_token_invalid instead of being guessed (cross-review
// round 7, chunk 1 P2).
func TestOIDCVerify_EmailVerifiedMalformedType(t *testing.T) {
	claims := oidcVerifyClaims()
	claims["email_verified"] = "false" // string, not bool
	idp := startOIDCVerifyIdP(t, claims)
	rig := newOIDCVerifyRig(t, idp, oauthserver.ErrorMode{}, nil)

	resp, res := rig.attempt()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "id_token_invalid", string(res.Reason))
	assert.Equal(t, 0, rig.userCount(), "user store untouched: no record created")
	assert.Nil(t, rig.alice())
	rig.assertLogNamesCheck("id_token_invalid", "email_verified")
}

// --- FR-008 / FR-022 / US2.3: groups ------------------------------------------

func TestOIDCVerify_GroupsFromToken(t *testing.T) {
	idp := startOIDCVerifyIdP(t, oidcVerifyClaims())
	rig := newOIDCVerifyRig(t, idp, oauthserver.ErrorMode{}, nil)

	start := time.Now().UTC().Add(-time.Second)
	resp, res := rig.attempt()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "ok", string(res.Reason))
	assert.NotContains(t, oidcFlags(res), "groups_claim_missing")

	u := rig.alice()
	require.NotNil(t, u)
	assert.Equal(t, []string{"eng", "sre"}, u.Groups, "exactly the verified token's groups claim")
	assert.False(t, u.GroupsUpdatedAt.IsZero())
	assert.True(t, u.GroupsUpdatedAt.After(start))

	_, _, _, userinfo := rig.tap.counts()
	assert.Equal(t, 0, userinfo, "userinfo is not consulted when the token carries the claim")

	// Wholesale replace on the next login: the claim now says something else.
	// (The fake derives claims per login; change them through the knob-free
	// route of re-seeding the record and asserting the token list wins.)
	u.Groups = []string{"stale-a", "stale-b"}
	require.NoError(t, rig.store.UpdateUser(u))
	resp, res = rig.attempt()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "ok", string(res.Reason))
	assert.Equal(t, []string{"eng", "sre"}, rig.alice().Groups, "replaced wholesale, never merged")
}

// Groups come from /userinfo only when the token lacks the claim, and only
// when userinfo's `sub` equals the verified token's `sub`. The fake has no
// "groups only in userinfo" knob: a userinfo knob strips the claim from the
// id_token, and the tap clears the knob right before the provider's userinfo
// request so the endpoint answers with the real sub and groups.
func TestOIDCVerify_GroupsFromUserinfoWhenTokenLacksClaim(t *testing.T) {
	idp := startOIDCVerifyIdP(t, oidcVerifyClaims())
	rig := newOIDCVerifyRig(t, idp, oauthserver.ErrorMode{UserinfoSubMismatch: true}, nil)
	clean := oauthserver.ErrorMode{}
	rig.tap.beforeUserinfo = &clean

	resp, res := rig.attempt()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "ok", string(res.Reason))
	assert.NotContains(t, oidcFlags(res), "groups_claim_missing")

	u := rig.alice()
	require.NotNil(t, u)
	assert.Equal(t, []string{"eng", "sre"}, u.Groups, "the userinfo groups, accepted because sub matched")
	assert.False(t, u.GroupsUpdatedAt.IsZero())

	_, _, _, userinfo := rig.tap.counts()
	assert.Equal(t, 1, userinfo, "userinfo consulted exactly once")
}

func TestOIDCVerify_UserinfoSubjectMismatchRefuses(t *testing.T) {
	idp := startOIDCVerifyIdP(t, oidcVerifyClaims())
	rig := newOIDCVerifyRig(t, idp, oauthserver.ErrorMode{UserinfoSubMismatch: true}, nil)
	seeded := rig.seedAlice([]string{"ops"})

	resp, res := rig.attempt()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "the generic 403 page")
	assert.Equal(t, "userinfo_subject_mismatch", string(res.Reason))
	assert.Empty(t, res.UserID, "the record is never looked up on this path (FR-013)")
	assert.Empty(t, resp.Cookies())

	assert.Equal(t, seeded, rig.alice(), "user store untouched; stored groups untouched")
	assert.Equal(t, 1, rig.userCount())
	_, _, _, userinfo := rig.tap.counts()
	assert.Equal(t, 1, userinfo, "no userinfo claim is read before the sub comparison, and no retry")
	rig.assertLogNamesCheck("userinfo_subject_mismatch", "sub")
}

// A userinfo fetch that fails at the transport, redirect, status or decoding
// level is provider_error → 503, login refused, store untouched, stored groups
// not reset (an IdP hiccup never converts a grant into the default grant and
// never logs a user in with []).
func TestOIDCVerify_UserinfoFailureIsProviderError(t *testing.T) {
	idp := startOIDCVerifyIdP(t, oidcVerifyClaims())

	cases := []struct {
		name          string
		mode          oauthserver.ErrorMode
		transportFail bool
	}{
		// UserinfoUnavailable only strips the claim from the id_token here; the
		// request never reaches the fake — the tap fails it at the transport.
		{name: "transport error", mode: oauthserver.ErrorMode{UserinfoUnavailable: true}, transportFail: true},
		{name: "3xx to another origin", mode: oauthserver.ErrorMode{UserinfoRedirect: true}},
		{name: "non-200", mode: oauthserver.ErrorMode{UserinfoUnavailable: true}},
		{name: "non-JSON body", mode: oauthserver.ErrorMode{UserinfoNonJSON: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newOIDCVerifyRig(t, idp, tc.mode, nil)
			if tc.transportFail {
				rig.tap.failUserinfo = errors.New("injected transport failure")
			}
			seeded := rig.seedAlice([]string{"ops"})

			resp, res := rig.attempt()

			assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "unavailability is the one distinct class (US2.9)")
			assert.Equal(t, "provider_error", string(res.Reason))
			assert.Empty(t, res.UserID, "the record is never looked up on this path (FR-013)")
			assert.Empty(t, resp.Cookies())

			after := rig.alice()
			require.NotNil(t, after)
			assert.Equal(t, seeded, after, "user store untouched")
			assert.Equal(t, []string{"ops"}, after.Groups, "stored groups untouched — never reset to []")
			assert.Equal(t, 1, rig.userCount())

			_, _, _, userinfo := rig.tap.counts()
			assert.Equal(t, 1, userinfo, "userinfo attempted exactly once, no retry")
			rig.assertLogNamesCheck("provider_error", "userinfo")
			text := rig.logText(zapcore.DebugLevel)
			assert.NotContains(t, text, rig.idp.ClientSecret, "never the client secret")
			for _, tok := range rig.tap.tokens() {
				assert.NotContains(t, text, tok, "never a token")
			}
		})
	}
}

// Absent in both, a non-array/non-string shape, or an Entra overage marker →
// stored groups [] (fail closed), login succeeds, `groups_claim_missing` on
// the result's flags, and a warning naming the user id and the claim name —
// never the token. A single string is a valid shape (US2.3: "a flat JSON
// array of strings, or a single string") and is stored as one group.
func TestOIDCVerify_GroupsClaimMissingFailsClosed(t *testing.T) {
	withGroups := startOIDCVerifyIdP(t, oidcVerifyClaims())

	objectClaims := oidcVerifyClaims()
	objectClaims["groups"] = map[string]any{"value": "eng"} // neither array nor string
	withObject := startOIDCVerifyIdP(t, objectClaims)

	cases := []struct {
		name            string
		idp             *oauthserver.ServerResult
		mode            oauthserver.ErrorMode
		wantGroups      []string
		wantMissing     bool
		userinfoAtLeast int
	}{
		{name: "absent from the token and from userinfo", idp: withGroups, mode: oauthserver.ErrorMode{GroupsAbsentEverywhere: true}, wantGroups: []string{}, wantMissing: true, userinfoAtLeast: 1},
		{name: "non-array non-string shape", idp: withObject, wantGroups: []string{}, wantMissing: true},
		{name: "Entra overage marker", idp: withGroups, mode: oauthserver.ErrorMode{GroupsOverageMarker: true}, wantGroups: []string{}, wantMissing: true},
		{name: "single string is one group", idp: withGroups, mode: oauthserver.ErrorMode{GroupsNonArray: true}, wantGroups: []string{"eng,sre"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newOIDCVerifyRig(t, tc.idp, tc.mode, nil)

			resp, res := rig.attempt()
			require.Equal(t, http.StatusFound, resp.StatusCode, "the login succeeds")
			assert.Equal(t, "ok", string(res.Reason))

			u := rig.alice()
			require.NotNil(t, u)
			assert.Equal(t, u.ID, res.UserID)
			assert.NotNil(t, u.Groups, "stored as [], never null")
			assert.Equal(t, tc.wantGroups, u.Groups)
			assert.False(t, u.GroupsUpdatedAt.IsZero(), "groups_updated_at is stamped even when the list is empty")

			_, _, _, userinfo := rig.tap.counts()
			assert.GreaterOrEqual(t, userinfo, tc.userinfoAtLeast, "the token lacked the claim, so userinfo was consulted")

			if !tc.wantMissing {
				assert.NotContains(t, oidcFlags(res), "groups_claim_missing")
				return
			}
			assert.Contains(t, oidcFlags(res), "groups_claim_missing")

			warn := rig.logText(zapcore.WarnLevel)
			assert.Contains(t, warn, "groups_claim_missing", "a warning is logged")
			assert.Contains(t, warn, oidcVerifyGroups, "the warning names the claim looked for")
			assert.Contains(t, warn, u.ID, "the warning names the user id")
			all := rig.logText(zapcore.DebugLevel)
			for _, tok := range rig.tap.tokens() {
				assert.NotContains(t, all, tok, "never the token")
			}
		})
	}
}

// If the token endpoint answers without an access_token (RFC 6749 §5.1
// requires it, but a misconfigured or hostile IdP could still omit it) and
// the ID token lacks the groups claim, the provider cannot attempt the
// userinfo fetch it is required to try (FR-008/FR-022). That is a fetch that
// never happened for lack of a credential, not "claim absent" — treated the
// same as any other userinfo failure class: provider_error, login refused,
// store untouched (never a silent groups=[] login, cross-review round 3).
func TestOIDCVerify_ResolveGroups_NoAccessTokenIsProviderError(t *testing.T) {
	prov := newOIDCProvider(&config.ServerEditionOAuthConfig{GroupsClaim: "groups"})
	prov.disc = &discoveryDoc{
		UserinfoEndpoint: "https://idp.example/userinfo",
		expiresAt:        time.Now().Add(time.Hour),
	}
	claims := &idTokenClaims{Subject: "alice", raw: map[string]any{}} // no "groups" claim

	groups, missing, err := prov.resolveGroups(context.Background(), claims, "")

	require.Error(t, err)
	var oe *oidcError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, LoginProviderError, oe.reason)
	assert.Nil(t, groups)
	assert.False(t, missing, "not a fail-closed groups_claim_missing outcome")
}
