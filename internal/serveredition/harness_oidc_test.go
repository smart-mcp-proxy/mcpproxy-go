//go:build server

package serveredition

// Spec 107 T046: the wiring harness over an in-process OpenID Provider
// (tests/oauthserver with Options.OIDC) so a test can drive a REAL login —
// GET /api/v1/auth/login → the fake's headless form → the callback — through
// the production setup and route table, and an httptest reverse proxy that
// sets or strips the X-Forwarded-* headers and rewrites Host the way an
// ingress does (for the PR-B front-door fixtures of Phase B.3).

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

const (
	harnessIdPPassword  = "pass"
	harnessGroupsClaim  = "groups"
	harnessCallbackHost = "127.0.0.1"
)

// harnessCallbackURI is the redirect_uri the handler derives from the request
// Host the harness sends (no public_url in PR-B's harness).
const harnessCallbackURI = "http://" + harnessCallbackHost + "/api/v1/auth/callback"

// oidcWiringHarness is a wiringHarness whose provider is the in-process fake
// IdP. Every user in `users` (email → groups) can log in with
// harnessIdPPassword; their `sub` is derived by the fake from the email.
type oidcWiringHarness struct {
	*wiringHarness
	idp      *oauthserver.ServerResult
	idpUsers map[string][]string
	// server serves the production router over TCP so the browser client and
	// the reverse-proxy helper can reach it.
	server *httptest.Server
}

// newOIDCWiringHarness starts the fake OpenID Provider with the given users
// and drives the production setup with `provider: oidc` pointing at it.
func newOIDCWiringHarness(t *testing.T, users map[string][]string) *oidcWiringHarness {
	t.Helper()

	valid := map[string]string{}
	claims := map[string]map[string]any{}
	for email, groups := range users {
		valid[email] = harnessIdPPassword
		gs := make([]any, 0, len(groups))
		for _, g := range groups {
			gs = append(gs, g)
		}
		claims[email] = map[string]any{
			"email":            email,
			"email_verified":   true,
			"name":             strings.SplitN(email, "@", 2)[0],
			harnessGroupsClaim: gs,
		}
	}
	idp := oauthserver.Start(t, oauthserver.Options{
		OIDC:               true,
		ValidUsers:         valid,
		UserClaims:         claims,
		ClientRedirectURIs: []string{harnessCallbackURI},
		GroupsClaim:        harnessGroupsClaim,
	})
	t.Cleanup(func() { _ = idp.Shutdown() })

	base := newWiringHarnessWith(t, &config.ServerEditionOAuthConfig{
		Provider:            "oidc",
		ClientID:            idp.ClientID,
		ClientSecret:        idp.ClientSecret,
		IssuerURL:           idp.IssuerURL,
		AllowInsecureIssuer: true, // loopback http fake
		Scopes:              []string{"openid", "profile", "email"},
		GroupsClaim:         harnessGroupsClaim,
		EmailVerifiedPolicy: config.EmailVerifiedPolicyRefuseFalse,
	})
	h := &oidcWiringHarness{wiringHarness: base, idp: idp, idpUsers: users}
	h.server = httptest.NewServer(h.router)
	t.Cleanup(h.server.Close)
	return h
}

// loginAs drives a complete login for email — login redirect, the fake's
// headless form POST (username/password/consent), the callback — through the
// production router, asserting the record now carries exactly `groups`, and
// returns a cookie jar holding the session cookie for later requests. The
// user must have been declared to newOIDCWiringHarness with those groups
// (the fake's claims are fixed at start).
func (h *oidcWiringHarness) loginAs(t *testing.T, email string, groups []string) http.CookieJar {
	t.Helper()
	declared, ok := h.idpUsers[email]
	require.True(t, ok, "user %q must be declared to newOIDCWiringHarness", email)
	require.Equal(t, groups, declared, "the fake's groups for %q are fixed at start", email)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	noRedirect := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 1. GET /api/v1/auth/login → 302 to the IdP with state, nonce, PKCE.
	loginReq, err := http.NewRequest(http.MethodGet, h.server.URL+"/api/v1/auth/login?redirect_uri=/ui/", nil)
	require.NoError(t, err)
	loginReq.Host = harnessCallbackHost
	loginResp, err := noRedirect.Do(loginReq)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, loginResp.Body)
	_ = loginResp.Body.Close()
	require.Equal(t, http.StatusFound, loginResp.StatusCode, "login must redirect to the IdP")
	authURL, err := url.Parse(loginResp.Header.Get("Location"))
	require.NoError(t, err)
	require.NotEmpty(t, authURL.Query().Get("nonce"))

	// 2. The fake's headless form → 302 back to the callback with a code.
	form := authURL.Query()
	form.Set("username", email)
	form.Set("password", harnessIdPPassword)
	form.Set("consent", "on")
	form.Set("action", "approve")
	idpResp, err := noRedirect.PostForm(h.idp.AuthorizationEndpoint, form)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, idpResp.Body)
	_ = idpResp.Body.Close()
	require.Equal(t, http.StatusFound, idpResp.StatusCode, "the fake must redirect back with a code")
	cb, err := url.Parse(idpResp.Header.Get("Location"))
	require.NoError(t, err)
	require.NotEmpty(t, cb.Query().Get("code"))

	// 3. The callback on the production router, delivered as the browser would.
	cbReq, err := http.NewRequest(http.MethodGet, h.server.URL+cb.RequestURI(), nil)
	require.NoError(t, err)
	cbReq.Host = harnessCallbackHost
	cbResp, err := noRedirect.Do(cbReq)
	require.NoError(t, err)
	body, _ := io.ReadAll(cbResp.Body)
	_ = cbResp.Body.Close()
	require.Equal(t, http.StatusFound, cbResp.StatusCode, "callback must succeed; body=%s", body)
	assert.Equal(t, "/ui/", cbResp.Header.Get("Location"))

	serverURL, err := url.Parse(h.server.URL)
	require.NoError(t, err)
	jar.SetCookies(serverURL, cbResp.Cookies())
	require.NotEmpty(t, jar.Cookies(serverURL), "the callback must set the session cookie")

	u, err := h.users.GetUserByEmail(email)
	require.NoError(t, err)
	require.NotNil(t, u, "the login created the record")
	assert.Equal(t, "oidc", u.Provider)
	assert.NotEmpty(t, u.ProviderSubjectID)
	assert.Equal(t, groups, u.Groups, "the stored groups are the verified token's")
	return jar
}

// forwardingProxyOptions shape one hop of an ingress in front of the proxy.
type forwardingProxyOptions struct {
	// Host rewrites the Host header the upstream sees ("" = keep the client's).
	Host string
	// Set adds or replaces X-Forwarded-* (and any other) headers.
	Set map[string]string
	// Strip removes headers the client sent (an ingress that sanitises).
	Strip []string
	// SourceIP is the local address the proxy dials the upstream from, so
	// the upstream's RemoteAddr is a chosen peer ("" = the default). On
	// macOS only 127.0.0.1 is bound by default; Linux binds all of 127/8.
	SourceIP string
}

// newForwardingProxy starts an httptest reverse proxy in front of upstream
// that applies opts to every request: an ingress that sets or strips the
// forwarded headers, rewrites Host and dials from a chosen source address.
func newForwardingProxy(t *testing.T, upstream *httptest.Server, opts forwardingProxyOptions) *httptest.Server {
	t.Helper()

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if opts.SourceIP != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(opts.SourceIP)}
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := http.NewRequestWithContext(r.Context(), r.Method, upstream.URL+r.URL.RequestURI(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		for k, vs := range r.Header {
			for _, v := range vs {
				out.Header.Add(k, v)
			}
		}
		for _, k := range opts.Strip {
			out.Header.Del(k)
		}
		for k, v := range opts.Set {
			out.Header.Set(k, v)
		}
		out.Host = r.Host
		if opts.Host != "" {
			out.Host = opts.Host
		}
		resp, err := client.Do(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

// The harness itself, end to end through the production wiring: a real
// `oidc` login lands a session whose /auth/me carries the verified groups
// (FR-008, contracts/rest-endpoints.md §2), and the record is bound to the
// fake's subject.
func TestOIDCWiringHarness_LoginAsLandsSessionWithGroups(t *testing.T) {
	h := newOIDCWiringHarness(t, map[string][]string{
		"alice@example.com": {"eng", "sre"},
		"bob@example.com":   {},
	})

	jar := h.loginAs(t, "alice@example.com", []string{"eng", "sre"})

	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, err := client.Get(h.server.URL + "/api/v1/auth/me")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var me struct {
		Email           string   `json:"email"`
		Role            string   `json:"role"`
		Provider        string   `json:"provider"`
		Groups          []string `json:"groups"`
		GroupsUpdatedAt *string  `json:"groups_updated_at"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&me))
	assert.Equal(t, "alice@example.com", me.Email)
	assert.Equal(t, "user", me.Role)
	assert.Equal(t, "oidc", me.Provider)
	assert.Equal(t, []string{"eng", "sre"}, me.Groups)
	require.NotNil(t, me.GroupsUpdatedAt)
	_, err = time.Parse(time.RFC3339, *me.GroupsUpdatedAt)
	assert.NoError(t, err)

	// A user without groups stores [] (never null on the wire).
	bobJar := h.loginAs(t, "bob@example.com", []string{})
	bobResp, err := (&http.Client{Jar: bobJar, Timeout: 5 * time.Second}).Get(h.server.URL + "/api/v1/auth/me")
	require.NoError(t, err)
	defer bobResp.Body.Close()
	var bob map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(bobResp.Body).Decode(&bob))
	assert.Equal(t, "[]", string(bob["groups"]))
}

// The reverse-proxy helper: headers it sets reach the upstream, headers it
// strips do not, and Host is rewritten.
func TestForwardingProxy_SetsStripsAndRewritesHost(t *testing.T) {
	var seen struct {
		host  string
		proto string
		xff   string
		xreal string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.host = r.Host
		seen.proto = r.Header.Get("X-Forwarded-Proto")
		seen.xff = r.Header.Get("X-Forwarded-For")
		seen.xreal = r.Header.Get("X-Real-IP")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	proxy := newForwardingProxy(t, upstream, forwardingProxyOptions{
		Host:  "proxy.example.com",
		Set:   map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "203.0.113.9"},
		Strip: []string{"X-Real-IP"},
	})

	req, err := http.NewRequest(http.MethodGet, proxy.URL+"/anything", nil)
	require.NoError(t, err)
	req.Header.Set("X-Real-IP", "198.51.100.1") // an attacker-supplied header the ingress strips
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	assert.Equal(t, "proxy.example.com", seen.host)
	assert.Equal(t, "https", seen.proto)
	assert.Equal(t, "203.0.113.9", seen.xff)
	assert.Empty(t, seen.xreal, "a stripped header never reaches the upstream")
}
