//go:build server

package serveredition

// Spec 107 T048 (PR-B, Phase B.3) — the front door behind an ingress, driven
// through the PRODUCTION setup and route table (the T046 harness) so a wiring
// mistake in setup.go fails here even when every handler-level test passes.
//
// Compile-red until T050 (`config.Config.TrustedProxies`, the trusted-proxy
// provider) and T051 (`ServerEditionConfig.PublicURL`,
// `ServerEditionConfig.SessionCookieSecure`, the Secure-policy session
// manager, the boot lines, the MCPPROXY_PUBLIC_URL override). Contract
// exercised (spec FR-025..FR-027, US2.1, US2.6; contracts/config-keys.md rows
// `public_url`, `session_cookie_secure`, `trusted_proxies`; research D10):
//
//	// T050 — edition-neutral, top-level, live.
//	config.Config.TrustedProxies []string   // CIDR or IP; default empty = trust nobody
//	// T051 — server build.
//	config.ServerEditionConfig.PublicURL           string // absolute origin, no path; env MCPPROXY_PUBLIC_URL
//	config.ServerEditionConfig.SessionCookieSecure string // "auto" (default) | "true" | "false"
//
// Behaviour pinned:
//   - public_url set → redirect_uri is `<public_url>/api/v1/auth/callback`
//     whatever Host or X-Forwarded-* say; the cookie is Secure for an https
//     public_url; a callback that arrives over plain http while public_url is
//     https logs ONE operator-readable warning carrying the request id and
//     the login is never blocked (FR-025).
//   - public_url unset → X-Forwarded-Proto / X-Forwarded-For / X-Forwarded-Host
//     are honoured only when RemoteAddr is inside trusted_proxies, the client
//     IP is the right-most untrusted hop, otherwise the listener scheme and
//     RemoteAddr are used (FR-027).
//   - session_cookie_secure auto|true|false matrix (FR-026); validation refuses
//     `false` with an https public_url or tls.enabled through BOTH
//     Config.Validate (boot) and ValidateDetailed (PATCH, /config/apply);
//     explicit `false` elsewhere is honoured with one boot warning.
//   - boot: one INFO line with the resolved public URL and callback URL and no
//     IdP request (US2.1); unset public_url on a non-loopback listener → one
//     boot warning (the `doctor` finding is asserted through the management
//     service in internal/management/diagnostics_serveredition_test.go, T052).
//   - MCPPROXY_PUBLIC_URL overrides the file value; an unset variable leaves
//     it (the personal build's twin is internal/config/env_public_url_personal_test.go).
//   - the per-user connect flow's base URL follows public_url with no
//     first-seen latch (connector_provider.go).

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

const (
	fdUser            = "alice@example.com"
	fdPassword        = "pass"
	fdPublicHTTPS     = "https://mcp.example.com"
	fdPublicHTTP      = "http://mcp.example.com"
	fdCallbackPath    = "/api/v1/auth/callback"
	fdLoopbackListen  = "127.0.0.1:8080"
	fdAnyListen       = "0.0.0.0:8080"
	fdRequestIDHeader = "X-Test-Request-Id"
	fdIngressHost     = "127.0.0.1:8080"
	fdCookieName      = teamsauth.SessionCookieName

	// The FR-025 scheme-disagreement wording; the assertion below matches the
	// two halves separately so a re-phrased line that keeps the substance
	// still passes.
	fdSchemeWarnPublic = "public_url is https"
	fdSchemeWarnHTTP   = "arrived over http"
)

// The callback the fake IdP accepts for a loopback host (any port, any
// scheme — tests/oauthserver's localhost rule), plus the two public ones.
var fdRegisteredCallbacks = []string{
	"http://127.0.0.1" + fdCallbackPath,
	fdPublicHTTPS + fdCallbackPath,
	fdPublicHTTP + fdCallbackPath,
}

// frontDoorOptions shapes one deployment: the config keys under test, the
// listener the config declares, and whether the harness itself terminates
// TLS (an in-process https listener).
type frontDoorOptions struct {
	PublicURL           string
	SessionCookieSecure string
	TrustedProxies      []string
	Listen              string // default fdLoopbackListen
	TLS                 bool   // in-process TLS: cfg.TLS.Enabled and an https httptest listener
	// IssuerURL replaces the fake IdP (no fake is started); used with a
	// counting issuer to prove setup makes no IdP request.
	IssuerURL string
	Servers   []*config.ServerConfig
	// CredentialKey enables the per-user credential store (connect flow).
	CredentialKey string
}

// frontDoorHarness is the T046 harness with an observed logger, a request-id
// middleware and the front-door keys threaded through the production setup.
type frontDoorHarness struct {
	t        *testing.T
	opts     frontDoorOptions
	router   *chi.Mux
	server   *httptest.Server
	idp      *oauthserver.ServerResult
	users    *users.UserStore
	hmacKey  []byte
	logs     *observer.ObservedLogs
	cfgMu    sync.RWMutex
	cfg      *config.Config
	setupErr error
}

// currentConfig is the live provider handed to setup (read-only snapshot).
func (h *frontDoorHarness) currentConfig() *config.Config {
	h.cfgMu.RLock()
	defer h.cfgMu.RUnlock()
	return h.cfg
}

// setTrustedProxies replaces the live trusted_proxies list the way a hot
// reload does: copy-on-write, a new snapshot pointer, no re-setup.
func (h *frontDoorHarness) setTrustedProxies(list []string) {
	h.cfgMu.Lock()
	defer h.cfgMu.Unlock()
	next := *h.cfg
	next.TrustedProxies = list
	h.cfg = &next
}

func newFrontDoorHarness(t *testing.T, opts frontDoorOptions) *frontDoorHarness {
	t.Helper()

	h := &frontDoorHarness{t: t, opts: opts}
	if opts.Listen == "" {
		opts.Listen = fdLoopbackListen
	}

	oauthCfg := &config.ServerEditionOAuthConfig{
		Provider:            "oidc",
		AllowInsecureIssuer: true, // loopback http fake
		Scopes:              []string{"openid", "profile", "email"},
		GroupsClaim:         "groups",
		EmailVerifiedPolicy: config.EmailVerifiedPolicyRefuseFalse,
	}
	if opts.IssuerURL != "" {
		oauthCfg.IssuerURL = opts.IssuerURL
		oauthCfg.ClientID = "counting-client"
		oauthCfg.ClientSecret = "counting-secret"
	} else {
		h.idp = oauthserver.Start(t, oauthserver.Options{
			OIDC:       true,
			ValidUsers: map[string]string{fdUser: fdPassword},
			UserClaims: map[string]map[string]any{fdUser: {
				"email": fdUser, "email_verified": true, "name": "Alice", "groups": []any{"eng"},
			}},
			ClientRedirectURIs: fdRegisteredCallbacks,
			GroupsClaim:        "groups",
		})
		t.Cleanup(func() { _ = h.idp.Shutdown() })
		oauthCfg.IssuerURL = h.idp.IssuerURL
		oauthCfg.ClientID = h.idp.ClientID
		oauthCfg.ClientSecret = h.idp.ClientSecret
	}

	tmpDir := t.TempDir()
	db, err := bbolt.Open(filepath.Join(tmpDir, "front-door.db"), 0600, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core).Sugar()
	h.logs = logs

	tokens, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { tokens.Close() })

	h.cfg = &config.Config{
		Listen:         opts.Listen,
		DataDir:        tmpDir,
		TrustedProxies: opts.TrustedProxies,
		Servers:        opts.Servers,
		ServerEdition: &config.ServerEditionConfig{
			Enabled:                 true,
			AdminEmails:             []string{"admin@example.com"},
			SessionTTL:              config.Duration(24 * time.Hour),
			BearerTokenTTL:          config.Duration(24 * time.Hour),
			OAuth:                   oauthCfg,
			PublicURL:               opts.PublicURL,
			SessionCookieSecure:     opts.SessionCookieSecure,
			CredentialEncryptionKey: opts.CredentialKey,
		},
	}
	if opts.TLS {
		h.cfg.TLS = &config.TLSConfig{Enabled: true}
	}

	// The production request-id middleware lives in internal/httpapi; the
	// harness stamps the id from a test header so a log line can be keyed
	// to one request.
	h.router = chi.NewRouter()
	h.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rid := r.Header.Get(fdRequestIDHeader); rid != "" {
				r = r.WithContext(reqcontext.WithRequestID(r.Context(), rid))
			}
			next.ServeHTTP(w, r)
		})
	})

	h.setupErr = setupMultiUserOAuth(Dependencies{
		Router:         h.router,
		DB:             db,
		Logger:         logger,
		DataDir:        tmpDir,
		Config:         h.cfg,
		ConfigProvider: h.currentConfig,
		StorageManager: tokens,
	})

	h.users = users.NewUserStore(db)
	h.hmacKey, err = auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	if opts.TLS {
		h.server = httptest.NewTLSServer(h.router)
	} else {
		h.server = httptest.NewServer(h.router)
	}
	t.Cleanup(h.server.Close)
	return h
}

// client returns a client that trusts the harness listener (the httptest
// certificate under TLS) and never follows redirects.
func (h *frontDoorHarness) client() *http.Client {
	c := h.server.Client()
	c.Timeout = 5 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// frontDoorRequest shapes how one browser request reaches the proxy.
type frontDoorRequest struct {
	via       *httptest.Server  // nil = the harness listener directly
	host      string            // Host header ("" = the listener's)
	headers   map[string]string // e.g. X-Forwarded-Proto
	requestID string
}

func (h *frontDoorHarness) do(t *testing.T, method, path string, fr frontDoorRequest) *http.Response {
	t.Helper()
	base := h.server.URL
	client := h.client()
	if fr.via != nil {
		base = fr.via.URL
		client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	req, err := http.NewRequest(method, base+path, http.NoBody)
	require.NoError(t, err)
	if fr.host != "" {
		req.Host = fr.host
	}
	for k, v := range fr.headers {
		req.Header.Set(k, v)
	}
	if fr.requestID != "" {
		req.Header.Set(fdRequestIDHeader, fr.requestID)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(strings.NewReader(string(body)))
	return resp
}

// loginOutcome is what one complete login left behind.
type loginOutcome struct {
	// idpRedirectURI is the redirect_uri the handler put on the authorization
	// URL — the callback the IdP is told to send the browser back to.
	idpRedirectURI string
	callback       *http.Response
	cookie         *http.Cookie
	session        *users.Session
}

// login drives login → the fake's form → callback, delivering the login and
// the callback with the SAME front-door shaping (an ingress shapes every
// request the same way). The callback request id is fr.requestID + "-cb".
func (h *frontDoorHarness) login(t *testing.T, fr frontDoorRequest, redirectURI string) loginOutcome {
	t.Helper()
	require.NotNil(t, h.idp, "login needs the fake IdP")

	q := url.Values{}
	if redirectURI != "" {
		q.Set("redirect_uri", redirectURI)
	}
	loginResp := h.do(t, http.MethodGet, "/api/v1/auth/login?"+q.Encode(), fr)
	body, _ := io.ReadAll(loginResp.Body)
	require.Equal(t, http.StatusFound, loginResp.StatusCode, "login must redirect to the IdP; body=%s", body)
	authURL, err := url.Parse(loginResp.Header.Get("Location"))
	require.NoError(t, err)
	out := loginOutcome{idpRedirectURI: authURL.Query().Get("redirect_uri")}
	require.NotEmpty(t, out.idpRedirectURI, "the authorization URL must carry redirect_uri")

	form := authURL.Query()
	form.Set("username", fdUser)
	form.Set("password", fdPassword)
	form.Set("consent", "on")
	form.Set("action", "approve")
	idpClient := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	idpResp, err := idpClient.PostForm(h.idp.AuthorizationEndpoint, form)
	require.NoError(t, err)
	idpBody, _ := io.ReadAll(idpResp.Body)
	_ = idpResp.Body.Close()
	require.Equal(t, http.StatusFound, idpResp.StatusCode, "the fake must redirect back with a code; body=%s", idpBody)
	cb, err := url.Parse(idpResp.Header.Get("Location"))
	require.NoError(t, err)
	require.NotEmpty(t, cb.Query().Get("code"), "the IdP must answer with a code: %s", cb)

	cbReq := fr
	if fr.requestID != "" {
		cbReq.requestID = fr.requestID + "-cb"
	}
	out.callback = h.do(t, http.MethodGet, cb.RequestURI(), cbReq)
	for _, c := range out.callback.Cookies() {
		if c.Name == fdCookieName {
			out.cookie = c
		}
	}
	if out.cookie != nil && out.cookie.Value != "" {
		out.session, err = h.users.GetSession(out.cookie.Value)
		require.NoError(t, err)
	}
	return out
}

// mustLogin is login with a successful callback required.
func (h *frontDoorHarness) mustLogin(t *testing.T, fr frontDoorRequest) loginOutcome {
	t.Helper()
	out := h.login(t, fr, "/ui/")
	body, _ := io.ReadAll(out.callback.Body)
	require.Equal(t, http.StatusFound, out.callback.StatusCode, "callback must succeed; body=%s", body)
	assert.Equal(t, "/ui/", out.callback.Header.Get("Location"))
	require.NotNil(t, out.cookie, "the callback must set the session cookie")
	require.NotNil(t, out.session, "the cookie must name a stored session")
	assert.True(t, out.cookie.HttpOnly, "HttpOnly is unchanged (FR-026)")
	assert.Equal(t, http.SameSiteLaxMode, out.cookie.SameSite, "SameSite=Lax is unchanged (FR-026)")
	return out
}

// entriesWith returns the observed entries at the level whose message or any
// string field contains every needle.
func (h *frontDoorHarness) entriesWith(level zapcore.Level, needles ...string) []observer.LoggedEntry {
	var out []observer.LoggedEntry
	for _, e := range h.logs.All() {
		if e.Level != level {
			continue
		}
		hay := e.Message
		for _, f := range e.Context {
			hay += " " + f.Key + "=" + fieldString(f)
		}
		ok := true
		for _, n := range needles {
			if !strings.Contains(hay, n) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, e)
		}
	}
	return out
}

func fieldString(f zapcore.Field) string {
	switch f.Type {
	case zapcore.StringType:
		return f.String
	case zapcore.StringerType:
		if s, ok := f.Interface.(interface{ String() string }); ok {
			return s.String()
		}
	}
	if f.Interface != nil {
		if b, err := json.Marshal(f.Interface); err == nil {
			return string(b)
		}
	}
	return ""
}

func fieldValue(e observer.LoggedEntry, key string) string {
	for _, f := range e.Context {
		if f.Key == key {
			return fieldString(f)
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// public_url behind a Host-rewriting ingress (US2.6 first clause, FR-025)
// ---------------------------------------------------------------------------

// TestFrontDoor_PublicURLBehindHostRewritingIngress: the ingress rewrites Host
// to the upstream address and strips X-Forwarded-Proto; with public_url set
// the redirect_uri is still `<public_url>/api/v1/auth/callback`, the cookie is
// Secure, the callback that arrived over plain http is logged (with the
// callback's request id) and the login is never blocked.
func TestFrontDoor_PublicURLBehindHostRewritingIngress(t *testing.T) {
	h := newFrontDoorHarness(t, frontDoorOptions{PublicURL: fdPublicHTTPS})
	require.NoError(t, h.setupErr)
	ingress := newForwardingProxy(t, h.server, forwardingProxyOptions{
		Host:  fdIngressHost,
		Strip: []string{"X-Forwarded-Proto", "X-Forwarded-For", "X-Forwarded-Host"},
	})

	out := h.mustLogin(t, frontDoorRequest{
		via:       ingress,
		headers:   map[string]string{"X-Forwarded-Proto": "https"}, // stripped by the ingress
		requestID: "rid-ingress",
	})

	assert.Equal(t, fdPublicHTTPS+fdCallbackPath, out.idpRedirectURI,
		"public_url is the sole source of the IdP redirect_uri; the rewritten Host must not leak into it")
	assert.True(t, out.cookie.Secure, "an https public_url makes the session cookie Secure under auto")

	// The callback reached the proxy over plain http (the ingress terminates
	// TLS and forwards http) while public_url says https: one warning, keyed
	// by the callback's request id, and the login above still succeeded.
	warns := h.entriesWith(zapcore.WarnLevel, fdSchemeWarnPublic, fdSchemeWarnHTTP)
	require.Len(t, warns, 1, "exactly one scheme-disagreement warning for the callback; got %+v", h.logs.All())
	assert.Equal(t, "rid-ingress-cb", fieldValue(warns[0], "request_id"),
		"the warning must carry the request id so the operator can find the request")
}

// TestFrontDoor_PublicURLIgnoresHostAndForwardedHeaders: with public_url set,
// Host-header injection and forwarded headers — trusted or not — never move
// the callback (FR-025 "Host and X-Forwarded-* are then ignored").
func TestFrontDoor_PublicURLIgnoresHostAndForwardedHeaders(t *testing.T) {
	for _, trusted := range [][]string{nil, {"127.0.0.1/32", "::1/128"}} {
		name := "untrusted"
		if trusted != nil {
			name = "trusted"
		}
		t.Run(name, func(t *testing.T) {
			h := newFrontDoorHarness(t, frontDoorOptions{PublicURL: fdPublicHTTPS, TrustedProxies: trusted})
			require.NoError(t, h.setupErr)

			out := h.mustLogin(t, frontDoorRequest{
				host: "evil.example",
				headers: map[string]string{
					"X-Forwarded-Proto": "http",
					"X-Forwarded-Host":  "evil.example",
					"X-Forwarded-For":   "1.2.3.4",
				},
				requestID: "rid-evil",
			})
			assert.Equal(t, fdPublicHTTPS+fdCallbackPath, out.idpRedirectURI)
			assert.True(t, out.cookie.Secure, "public_url decides Secure, not the forwarded scheme")
			if trusted != nil {
				assert.Equal(t, "1.2.3.4", out.session.IPAddress, "the client IP still follows the trusted forwarded chain")
			} else {
				assert.Equal(t, "127.0.0.1", out.session.IPAddress, "an untrusted X-Forwarded-For never sets the session IP")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// public_url unset: forwarded headers only from trusted proxies (US2.6, FR-027)
// ---------------------------------------------------------------------------

// TestFrontDoor_ForwardedHeadersOnlyFromTrustedProxies: the same request —
// X-Forwarded-Proto: https, X-Forwarded-For: 1.2.3.4 — from an address outside
// trusted_proxies is ignored (listener scheme, RemoteAddr); from a trusted
// proxy it is honoured (https callback, Secure cookie, recorded IP).
func TestFrontDoor_ForwardedHeadersOnlyFromTrustedProxies(t *testing.T) {
	forwarded := map[string]string{
		"X-Forwarded-Proto": "https",
		"X-Forwarded-For":   "1.2.3.4",
	}

	t.Run("untrusted peer is ignored", func(t *testing.T) {
		// A non-empty list that does NOT contain the loopback peer, so the
		// case is not confused with "no list configured".
		h := newFrontDoorHarness(t, frontDoorOptions{TrustedProxies: []string{"10.0.0.0/8"}})
		require.NoError(t, h.setupErr)

		out := h.mustLogin(t, frontDoorRequest{host: "mcp.example.com", headers: forwarded, requestID: "rid-untrusted"})
		assert.Equal(t, fdPublicHTTP+fdCallbackPath, out.idpRedirectURI, "scheme from the listener, not the untrusted header")
		assert.False(t, out.cookie.Secure, "an untrusted client cannot force Secure on an http deployment")
		assert.Equal(t, "127.0.0.1", out.session.IPAddress, "RemoteAddr, not the untrusted X-Forwarded-For")
		assert.Empty(t, h.entriesWith(zapcore.WarnLevel, fdSchemeWarnPublic), "no public_url, no scheme-disagreement warning")
	})

	t.Run("empty list trusts nobody", func(t *testing.T) {
		h := newFrontDoorHarness(t, frontDoorOptions{})
		require.NoError(t, h.setupErr)

		out := h.mustLogin(t, frontDoorRequest{host: "mcp.example.com", headers: forwarded, requestID: "rid-nobody"})
		assert.Equal(t, fdPublicHTTP+fdCallbackPath, out.idpRedirectURI)
		assert.False(t, out.cookie.Secure)
		assert.Equal(t, "127.0.0.1", out.session.IPAddress)
	})

	t.Run("trusted peer is honoured", func(t *testing.T) {
		h := newFrontDoorHarness(t, frontDoorOptions{TrustedProxies: []string{"127.0.0.1/32", "::1/128"}})
		require.NoError(t, h.setupErr)

		out := h.mustLogin(t, frontDoorRequest{host: "mcp.example.com", headers: forwarded, requestID: "rid-trusted"})
		assert.Equal(t, fdPublicHTTPS+fdCallbackPath, out.idpRedirectURI, "X-Forwarded-Proto from a trusted proxy sets the scheme")
		assert.True(t, out.cookie.Secure, "a trusted https hop makes the cookie Secure under auto")
		assert.Equal(t, "1.2.3.4", out.session.IPAddress, "X-Forwarded-For from a trusted proxy sets the session IP")
	})

	t.Run("trusted peer: right-most untrusted hop is the client", func(t *testing.T) {
		h := newFrontDoorHarness(t, frontDoorOptions{TrustedProxies: []string{"127.0.0.1/32", "10.0.0.0/8"}})
		require.NoError(t, h.setupErr)

		out := h.mustLogin(t, frontDoorRequest{
			host: "mcp.example.com",
			headers: map[string]string{
				"X-Forwarded-Proto": "https",
				// spoofed by the client, real client, trusted internal hop
				"X-Forwarded-For": "9.9.9.9, 1.2.3.4, 10.0.0.5",
			},
			requestID: "rid-hops",
		})
		assert.Equal(t, "1.2.3.4", out.session.IPAddress,
			"the first hop is client-controlled; the client IP is the right-most address not in trusted_proxies")
	})

	t.Run("trusted X-Forwarded-Host rewrites the callback host", func(t *testing.T) {
		h := newFrontDoorHarness(t, frontDoorOptions{TrustedProxies: []string{"127.0.0.1/32"}})
		require.NoError(t, h.setupErr)

		out := h.mustLogin(t, frontDoorRequest{
			host:      fdIngressHost,
			headers:   map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "mcp.example.com"},
			requestID: "rid-xfh",
		})
		assert.Equal(t, fdPublicHTTPS+fdCallbackPath, out.idpRedirectURI)
	})

	t.Run("untrusted X-Forwarded-Host is ignored", func(t *testing.T) {
		h := newFrontDoorHarness(t, frontDoorOptions{})
		require.NoError(t, h.setupErr)

		out := h.mustLogin(t, frontDoorRequest{
			host:      fdIngressHost,
			headers:   map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "mcp.example.com"},
			requestID: "rid-xfh-untrusted",
		})
		assert.Equal(t, "http://"+fdIngressHost+fdCallbackPath, out.idpRedirectURI, "Host and the listener scheme")
	})

	t.Run("X-Real-IP only from a trusted peer", func(t *testing.T) {
		for _, trusted := range [][]string{nil, {"127.0.0.1/32"}} {
			h := newFrontDoorHarness(t, frontDoorOptions{TrustedProxies: trusted})
			require.NoError(t, h.setupErr)
			out := h.mustLogin(t, frontDoorRequest{headers: map[string]string{"X-Real-IP": "5.6.7.8"}, requestID: "rid-xri"})
			if trusted == nil {
				assert.Equal(t, "127.0.0.1", out.session.IPAddress)
			} else {
				assert.Equal(t, "5.6.7.8", out.session.IPAddress)
			}
		}
	})
}

// TestFrontDoor_TrustedProxiesIsLive: the readers evaluate the provider per
// request (contracts/config-keys.md: `trusted_proxies` is live). The same
// headers are ignored, then honoured, then ignored again as the live
// configuration changes — without re-running setup.
func TestFrontDoor_TrustedProxiesIsLive(t *testing.T) {
	h := newFrontDoorHarness(t, frontDoorOptions{})
	require.NoError(t, h.setupErr)
	forwarded := map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "1.2.3.4"}

	out := h.mustLogin(t, frontDoorRequest{host: "mcp.example.com", headers: forwarded, requestID: "rid-live-1"})
	assert.Equal(t, "127.0.0.1", out.session.IPAddress)
	assert.Equal(t, fdPublicHTTP+fdCallbackPath, out.idpRedirectURI)

	h.setTrustedProxies([]string{"127.0.0.1/32"})
	out = h.mustLogin(t, frontDoorRequest{host: "mcp.example.com", headers: forwarded, requestID: "rid-live-2"})
	assert.Equal(t, "1.2.3.4", out.session.IPAddress, "a reader that captured the boot slice is not live")
	assert.Equal(t, fdPublicHTTPS+fdCallbackPath, out.idpRedirectURI)

	h.setTrustedProxies(nil)
	out = h.mustLogin(t, frontDoorRequest{host: "mcp.example.com", headers: forwarded, requestID: "rid-live-3"})
	assert.Equal(t, "127.0.0.1", out.session.IPAddress)
	assert.Equal(t, fdPublicHTTP+fdCallbackPath, out.idpRedirectURI)
}

// ---------------------------------------------------------------------------
// session_cookie_secure matrix (FR-026)
// ---------------------------------------------------------------------------

func TestFrontDoor_SessionCookieSecureMatrix(t *testing.T) {
	cases := []struct {
		name       string
		opts       frontDoorOptions
		host       string
		headers    map[string]string
		wantSecure bool
	}{
		{name: "auto, plain http loopback", opts: frontDoorOptions{SessionCookieSecure: "auto"}, wantSecure: false},
		{name: "default (unset) is auto", opts: frontDoorOptions{}, wantSecure: false},
		{name: "auto, https public_url", opts: frontDoorOptions{SessionCookieSecure: "auto", PublicURL: fdPublicHTTPS}, wantSecure: true},
		{name: "auto, http public_url", opts: frontDoorOptions{SessionCookieSecure: "auto", PublicURL: fdPublicHTTP}, host: "mcp.example.com", wantSecure: false},
		{name: "auto, in-process TLS", opts: frontDoorOptions{SessionCookieSecure: "auto", TLS: true}, wantSecure: true},
		{name: "auto, trusted X-Forwarded-Proto https", opts: frontDoorOptions{SessionCookieSecure: "auto", TrustedProxies: []string{"127.0.0.1/32"}},
			host: "mcp.example.com", headers: map[string]string{"X-Forwarded-Proto": "https"}, wantSecure: true},
		{name: "auto, untrusted X-Forwarded-Proto https", opts: frontDoorOptions{SessionCookieSecure: "auto"},
			host: "mcp.example.com", headers: map[string]string{"X-Forwarded-Proto": "https"}, wantSecure: false},
		{name: "true, plain http", opts: frontDoorOptions{SessionCookieSecure: "true"}, wantSecure: true},
		{name: "true, http public_url", opts: frontDoorOptions{SessionCookieSecure: "true", PublicURL: fdPublicHTTP}, host: "mcp.example.com", wantSecure: true},
		{name: "false, plain http", opts: frontDoorOptions{SessionCookieSecure: "false"}, wantSecure: false},
		{name: "false, http public_url", opts: frontDoorOptions{SessionCookieSecure: "false", PublicURL: fdPublicHTTP}, host: "mcp.example.com", wantSecure: false},
		{name: "false, trusted X-Forwarded-Proto https is still false", opts: frontDoorOptions{SessionCookieSecure: "false", TrustedProxies: []string{"127.0.0.1/32"}},
			host: "mcp.example.com", headers: map[string]string{"X-Forwarded-Proto": "https"}, wantSecure: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newFrontDoorHarness(t, tc.opts)
			require.NoError(t, h.setupErr, "the matrix row must boot")

			out := h.mustLogin(t, frontDoorRequest{host: tc.host, headers: tc.headers, requestID: "rid-matrix"})
			assert.Equal(t, tc.wantSecure, out.cookie.Secure, "Secure attribute of the session cookie")

			// Logout clears the cookie with the same attributes, or the
			// browser keeps the Secure one (RFC 6265 §4.1.2).
			logout, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/v1/auth/logout", http.NoBody)
			require.NoError(t, err)
			logout.AddCookie(&http.Cookie{Name: fdCookieName, Value: out.cookie.Value})
			resp, err := h.client().Do(logout)
			require.NoError(t, err)
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			var cleared *http.Cookie
			for _, c := range resp.Cookies() {
				if c.Name == fdCookieName {
					cleared = c
				}
			}
			require.NotNil(t, cleared, "logout must clear the cookie")
			assert.Equal(t, tc.wantSecure, cleared.Secure, "the clearing cookie carries the same Secure decision")
		})
	}
}

// TestFrontDoor_ExplicitFalseWarnsAtBoot: an explicit `false` on a plain-http
// deployment is honoured with ONE startup warning naming the key (its only
// legitimate use is a plain-http loopback or test deployment); `auto` and
// `true` warn about nothing.
func TestFrontDoor_ExplicitFalseWarnsAtBoot(t *testing.T) {
	for _, policy := range []string{"false", "auto", "true", ""} {
		t.Run("policy="+policy, func(t *testing.T) {
			h := newFrontDoorHarness(t, frontDoorOptions{SessionCookieSecure: policy, PublicURL: "http://127.0.0.1:8080"})
			require.NoError(t, h.setupErr)
			warns := h.entriesWith(zapcore.WarnLevel, "session_cookie_secure")
			if policy == "false" {
				assert.Len(t, warns, 1, "exactly one boot warning for an explicit false; got %+v", warns)
			} else {
				assert.Empty(t, warns, "no warning for %q", policy)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Boot lines (US2.1, FR-025)
// ---------------------------------------------------------------------------

// TestFrontDoor_BootLogsResolvedURLsWithoutContactingIdP: with public_url set,
// setup logs one INFO line carrying the resolved public URL and the callback
// URL, and makes no request to the issuer (discovery stays lazy).
func TestFrontDoor_BootLogsResolvedURLsWithoutContactingIdP(t *testing.T) {
	var hits atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(issuer.Close)

	h := newFrontDoorHarness(t, frontDoorOptions{PublicURL: fdPublicHTTPS, IssuerURL: issuer.URL})
	require.NoError(t, h.setupErr)

	infos := h.entriesWith(zapcore.InfoLevel, fdPublicHTTPS+fdCallbackPath)
	require.Len(t, infos, 1, "exactly one INFO boot line carrying the callback URL; got %+v", h.logs.All())
	assert.Contains(t, fieldsJoined(infos[0]), fdPublicHTTPS, "the same line carries the resolved public URL")
	assert.Equal(t, int32(0), hits.Load(), "setup must not contact the IdP (discovery is lazy, US2.1)")
}

func fieldsJoined(e observer.LoggedEntry) string {
	parts := []string{e.Message}
	for _, f := range e.Context {
		parts = append(parts, f.Key+"="+fieldString(f))
	}
	return strings.Join(parts, " ")
}

// TestFrontDoor_UnsetPublicURLOnNonLoopbackListenerWarns: the published image
// listens on 0.0.0.0:8080 with no public_url — that is a boot warning
// recommending the key, never a validation error; a loopback listener and a
// set public_url warn about nothing. (The `doctor` finding of the same case
// is asserted through the management service in
// internal/management/diagnostics_serveredition_test.go, T052.)
func TestFrontDoor_UnsetPublicURLOnNonLoopbackListenerWarns(t *testing.T) {
	cases := []struct {
		name     string
		listen   string
		public   string
		wantWarn bool
	}{
		{name: "0.0.0.0 without public_url", listen: fdAnyListen, public: "", wantWarn: true},
		{name: "[::] without public_url", listen: "[::]:8080", public: "", wantWarn: true},
		{name: "0.0.0.0 with public_url", listen: fdAnyListen, public: fdPublicHTTPS, wantWarn: false},
		{name: "loopback without public_url", listen: fdLoopbackListen, public: "", wantWarn: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newFrontDoorHarness(t, frontDoorOptions{Listen: tc.listen, PublicURL: tc.public})
			require.NoError(t, h.setupErr, "never a validation error (FR-025)")
			warns := h.entriesWith(zapcore.WarnLevel, "public_url")
			if tc.wantWarn {
				assert.Len(t, warns, 1, "one boot warning recommending public_url; got %+v", warns)
			} else {
				assert.Empty(t, warns)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Validation through both doors (FR-025, FR-026, FR-039)
// ---------------------------------------------------------------------------

// frontDoorConfig returns a Config that passes Config.Validate on its own
// with the given front-door keys.
func frontDoorConfig(publicURL, cookieSecure string, tls bool) *config.Config {
	cfg := &config.Config{
		Listen:            fdLoopbackListen,
		ToolsLimit:        15,
		ToolResponseLimit: 1000,
		CallToolTimeout:   config.Duration(time.Minute),
		Servers:           []*config.ServerConfig{},
		ServerEdition: &config.ServerEditionConfig{
			Enabled:     true,
			AdminEmails: []string{"admin@example.com"},
			OAuth: &config.ServerEditionOAuthConfig{
				Provider:     "google",
				ClientID:     "cid",
				ClientSecret: "csec",
			},
			PublicURL:           publicURL,
			SessionCookieSecure: cookieSecure,
		},
	}
	if tls {
		cfg.TLS = &config.TLSConfig{Enabled: true}
	}
	return cfg
}

const (
	msgSecureFalseRefused = "server_edition.session_cookie_secure=false cannot be combined with an https public_url or tls.enabled"
	msgPublicURLShape     = "server_edition.public_url must be an absolute origin (scheme://host[:port]) with no path"
)

// TestFrontDoor_ValidationBothDoors: every rule is applied identically by
// Config.Validate (boot) and ValidateDetailed (PATCH /api/v1/config and
// /config/apply both call it — internal/runtime/runtime.go ValidateConfig /
// applyConfigLocked), with the message text of contracts/config-keys.md.
func TestFrontDoor_ValidationBothDoors(t *testing.T) {
	cases := []struct {
		name       string
		publicURL  string
		secure     string
		tls        bool
		wantErrMsg string // "" = valid
	}{
		{name: "false with https public_url is refused", publicURL: fdPublicHTTPS, secure: "false", wantErrMsg: msgSecureFalseRefused},
		{name: "false with in-process TLS is refused", publicURL: "", secure: "false", tls: true, wantErrMsg: msgSecureFalseRefused},
		{name: "false with http public_url is allowed", publicURL: fdPublicHTTP, secure: "false"},
		{name: "false with no public_url and no TLS is allowed", secure: "false"},
		{name: "auto with https public_url", publicURL: fdPublicHTTPS, secure: "auto"},
		{name: "true with http public_url is allowed (explicit)", publicURL: fdPublicHTTP, secure: "true"},
		{name: "unknown policy is refused", secure: "yes", wantErrMsg: "server_edition.session_cookie_secure"},
		{name: "public_url with a path is refused", publicURL: "https://mcp.example.com/proxy", wantErrMsg: msgPublicURLShape},
		{name: "public_url with a trailing slash is refused", publicURL: "https://mcp.example.com/", wantErrMsg: msgPublicURLShape},
		{name: "public_url with a query is refused", publicURL: "https://mcp.example.com?x=1", wantErrMsg: msgPublicURLShape},
		{name: "public_url with a fragment is refused", publicURL: "https://mcp.example.com#f", wantErrMsg: msgPublicURLShape},
		{name: "public_url without a scheme is refused", publicURL: "mcp.example.com", wantErrMsg: msgPublicURLShape},
		{name: "public_url with a non-http scheme is refused", publicURL: "ftp://mcp.example.com", wantErrMsg: msgPublicURLShape},
		{name: "public_url with userinfo is refused", publicURL: "https://user:pw@mcp.example.com", wantErrMsg: msgPublicURLShape},
		{name: "public_url with a port is allowed", publicURL: "https://mcp.example.com:8443"},
		{name: "http public_url is allowed", publicURL: fdPublicHTTP},
		{name: "unset public_url is allowed", publicURL: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Boot door.
			err := frontDoorConfig(tc.publicURL, tc.secure, tc.tls).Validate()
			// Write doors (PATCH, apply).
			detailed := frontDoorConfig(tc.publicURL, tc.secure, tc.tls).ValidateDetailed()

			if tc.wantErrMsg == "" {
				assert.NoError(t, err, "Config.Validate")
				assert.Empty(t, detailed, "ValidateDetailed")
				return
			}
			require.Error(t, err, "Config.Validate must refuse")
			assert.Contains(t, err.Error(), tc.wantErrMsg, "boot message")
			require.NotEmpty(t, detailed, "ValidateDetailed must refuse identically")
			found := false
			for _, ve := range detailed {
				if strings.Contains(ve.Message, tc.wantErrMsg) {
					found = true
				}
			}
			assert.True(t, found, "ValidateDetailed must carry the same message; got %+v", detailed)
		})
	}
}

// TestFrontDoor_SetupRefusesFalseWithHTTPSPublicURL: the boot door as setup
// runs it — the process comes up with the feature disabled and the error
// names the rule.
func TestFrontDoor_SetupRefusesFalseWithHTTPSPublicURL(t *testing.T) {
	h := newFrontDoorHarness(t, frontDoorOptions{PublicURL: fdPublicHTTPS, SessionCookieSecure: "false"})
	require.Error(t, h.setupErr)
	assert.Contains(t, h.setupErr.Error(), msgSecureFalseRefused)
}

// ---------------------------------------------------------------------------
// MCPPROXY_PUBLIC_URL (FR-025, the one nested key with an env alias)
// ---------------------------------------------------------------------------

func writeFrontDoorConfigFile(t *testing.T, publicURL string) string {
	t.Helper()
	dir := t.TempDir()
	doc := map[string]any{
		"listen":   fdLoopbackListen,
		"data_dir": dir,
		"server_edition": map[string]any{
			"enabled":      true,
			"admin_emails": []string{"admin@example.com"},
			"oauth":        map[string]any{"provider": "google", "client_id": "cid", "client_secret": "csec"},
			"public_url":   publicURL,
		},
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	path := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

func TestFrontDoor_PublicURLEnvOverride(t *testing.T) {
	t.Run("env overrides the file value", func(t *testing.T) {
		t.Setenv("MCPPROXY_PUBLIC_URL", "https://env.example")
		cfg, err := config.LoadFromFile(writeFrontDoorConfigFile(t, "https://file.example"))
		require.NoError(t, err)
		assert.Equal(t, "https://env.example", cfg.ServerEdition.PublicURL)
	})
	t.Run("env sets an unset file value", func(t *testing.T) {
		t.Setenv("MCPPROXY_PUBLIC_URL", "https://env.example")
		cfg, err := config.LoadFromFile(writeFrontDoorConfigFile(t, ""))
		require.NoError(t, err)
		assert.Equal(t, "https://env.example", cfg.ServerEdition.PublicURL)
	})
	t.Run("unset env leaves the file value", func(t *testing.T) {
		t.Setenv("MCPPROXY_PUBLIC_URL", "")
		require.NoError(t, os.Unsetenv("MCPPROXY_PUBLIC_URL"))
		cfg, err := config.LoadFromFile(writeFrontDoorConfigFile(t, "https://file.example"))
		require.NoError(t, err)
		assert.Equal(t, "https://file.example", cfg.ServerEdition.PublicURL)
	})
	t.Run("an invalid env value is refused like a file value", func(t *testing.T) {
		t.Setenv("MCPPROXY_PUBLIC_URL", "https://env.example/path")
		_, err := config.LoadFromFile(writeFrontDoorConfigFile(t, "https://file.example"))
		require.Error(t, err, "the override goes through the same validation")
		assert.Contains(t, err.Error(), msgPublicURLShape)
	})
}

// ---------------------------------------------------------------------------
// Connect-flow base URL follows public_url (FR-025, connector_provider.go)
// ---------------------------------------------------------------------------

func fdConnectServer(name string) *config.ServerConfig {
	return &config.ServerConfig{
		Name:     name,
		URL:      "https://" + name + ".example.com/mcp",
		Protocol: "http",
		Shared:   true,
		Enabled:  true,
		AuthBroker: &config.AuthBrokerConfig{
			Mode:                  config.AuthBrokerModeOAuthConnect,
			AuthorizationEndpoint: "https://as.example.com/authorize",
			TokenEndpoint:         "https://as.example.com/token",
			ClientID:              "client-" + name,
			Scopes:                []string{"repo"},
		},
	}
}

// connectRedirectURI drives GET /api/v1/user/credentials/{server}/connect as
// a tenant and returns the redirect_uri the proxy registered on the upstream
// authorization URL.
func (h *frontDoorHarness) connectRedirectURI(t *testing.T, bearer, server string, fr frontDoorRequest) string {
	t.Helper()
	if fr.headers == nil {
		fr.headers = map[string]string{}
	}
	fr.headers["Authorization"] = "Bearer " + bearer
	resp := h.do(t, http.MethodGet, "/api/v1/user/credentials/"+server+"/connect", fr)
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusFound, resp.StatusCode, "connect must redirect to the upstream AS; body=%s", body)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	return loc.Query().Get("redirect_uri")
}

func TestFrontDoor_ConnectFlowBaseURLFollowsPublicURL(t *testing.T) {
	credKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	const server = "gh"
	wantPath := "/api/v1/user/credentials/" + server + "/callback"

	t.Run("public_url set: never the Host, no first-seen latch", func(t *testing.T) {
		h := newFrontDoorHarness(t, frontDoorOptions{
			PublicURL:     fdPublicHTTPS,
			Servers:       []*config.ServerConfig{fdConnectServer(server)},
			CredentialKey: credKey,
		})
		require.NoError(t, h.setupErr)
		u := users.NewUser("tenant@example.com", "Tenant", "oidc", "sub-tenant")
		require.NoError(t, h.users.CreateUser(u))
		bearer, err := teamsauth.GenerateBearerToken(h.hmacKey, u.ID, u.Email, u.DisplayName, "user", u.Provider, time.Hour)
		require.NoError(t, err)

		// The FIRST request carries a hostile Host and forwarded headers: a
		// latch of the first-seen origin would poison every later connector.
		first := h.connectRedirectURI(t, bearer, server, frontDoorRequest{
			host:    "evil.example",
			headers: map[string]string{"X-Forwarded-Proto": "http", "X-Forwarded-Host": "evil.example"},
		})
		assert.Equal(t, fdPublicHTTPS+wantPath, first)

		second := h.connectRedirectURI(t, bearer, server, frontDoorRequest{host: fdIngressHost})
		assert.Equal(t, fdPublicHTTPS+wantPath, second)
	})

	t.Run("public_url unset: forwarded scheme only from a trusted peer", func(t *testing.T) {
		for _, trusted := range [][]string{nil, {"127.0.0.1/32"}} {
			h := newFrontDoorHarness(t, frontDoorOptions{
				TrustedProxies: trusted,
				Servers:        []*config.ServerConfig{fdConnectServer(server)},
				CredentialKey:  credKey,
			})
			require.NoError(t, h.setupErr)
			u := users.NewUser("tenant@example.com", "Tenant", "oidc", "sub-tenant")
			require.NoError(t, h.users.CreateUser(u))
			bearer, err := teamsauth.GenerateBearerToken(h.hmacKey, u.ID, u.Email, u.DisplayName, "user", u.Provider, time.Hour)
			require.NoError(t, err)

			got := h.connectRedirectURI(t, bearer, server, frontDoorRequest{
				host:    "mcp.example.com",
				headers: map[string]string{"X-Forwarded-Proto": "https"},
			})
			if trusted == nil {
				assert.Equal(t, fdPublicHTTP+wantPath, got, "untrusted X-Forwarded-Proto is ignored")
			} else {
				assert.Equal(t, fdPublicHTTPS+wantPath, got, "trusted X-Forwarded-Proto is honoured")
			}
		}
	})
}
