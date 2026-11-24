//go:build !server

package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

const (
	oauthTestHost     = "127.0.0.1:8080"
	oauthTestOrigin   = "http://127.0.0.1:8080"
	oauthTestRedirect = "http://127.0.0.1:54321/callback"
)

// oauthFlow drives the public OAuth endpoints of an in-process server through
// the same mux shape startCustomHTTPServer builds.
type oauthFlow struct {
	t    *testing.T
	srv  *Server
	mux  http.Handler
	host string
	// limited keeps the endpoint rate limits between requests; by default every
	// request starts with a fresh budget so table tests do not trip them.
	limited bool
}

func newOAuthFlow(t *testing.T, mutate func(*config.Config)) *oauthFlow {
	t.Helper()
	srv := newOAuthLoginTestServer(t, func(c *config.Config) {
		c.MCPOAuthEnabled = true
		if mutate != nil {
			mutate(c)
		}
	})
	return &oauthFlow{t: t, srv: srv, mux: oauthLoginTestMux(srv, true), host: oauthTestHost}
}

func (f *oauthFlow) origin() string { return "http://" + f.host }

func (f *oauthFlow) do(req *http.Request) *httptest.ResponseRecorder {
	req.RemoteAddr = "127.0.0.1:4000"
	req.Host = f.host
	if !f.limited {
		f.srv.oauthLimiterOnce.Do(func() {})
		f.srv.oauthLimiter = mcplogin.NewLimiter(nil)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *oauthFlow) get(path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return f.do(req)
}

func (f *oauthFlow) postJSON(path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return f.do(req)
}

func (f *oauthFlow) postForm(path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return f.do(req)
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

func (f *oauthFlow) register(redirect string) string {
	f.t.Helper()
	rec := f.postJSON("/oauth/register", `{"redirect_uris":["`+redirect+`"],"client_name":"Test Client"}`)
	require.Equal(f.t, http.StatusCreated, rec.Code, rec.Body.String())
	id, _ := decodeBody(f.t, rec)["client_id"].(string)
	require.NotEmpty(f.t, id)
	return id
}

// pkce returns a verifier and its S256 challenge.
func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func (f *oauthFlow) authorizeQuery(clientID, redirect, challenge, resource, state string) string {
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {resource},
	}
	if state != "" {
		q.Set("state", state)
	}
	return "/oauth/authorize?" + q.Encode()
}

// authorize returns the request id the consent URL carries.
func (f *oauthFlow) authorize(clientID, redirect, challenge, resource string) string {
	f.t.Helper()
	rec := f.get(f.authorizeQuery(clientID, redirect, challenge, resource, "st"), nil)
	require.Equal(f.t, http.StatusFound, rec.Code, rec.Body.String())
	loc, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(f.t, err)
	require.Equal(f.t, "/ui/authorize", loc.Path)
	return loc.Query().Get("request")
}

// approve approves request id for client and returns the code from the redirect.
func (f *oauthFlow) approve(id, clientID string) string {
	f.t.Helper()
	redirect, err := f.srv.oauthLoginSvc().Approve(adminCtx(), id, runtime.ApprovalChoice{Client: runtime.ClientChoice{ID: clientID}})
	require.NoError(f.t, err)
	u, err := url.Parse(redirect)
	require.NoError(f.t, err)
	code := u.Query().Get("code")
	require.NotEmpty(f.t, code, redirect)
	return code
}

type oauthTokens struct{ access, refresh string }

func (f *oauthFlow) exchange(code, clientID, verifier, resource string) *httptest.ResponseRecorder {
	return f.postForm("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthTestRedirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {resource},
	})
}

// login runs register -> authorize -> approve -> token for a per-client address.
func (f *oauthFlow) login(client string) (regID string, tok oauthTokens) {
	f.t.Helper()
	regID = f.register(oauthTestRedirect)
	verifier, challenge := pkce()
	resource := f.origin() + "/mcp/oauth/" + client
	id := f.authorize(regID, oauthTestRedirect, challenge, resource)
	code := f.approve(id, client)
	rec := f.exchange(code, regID, verifier, resource)
	require.Equal(f.t, http.StatusOK, rec.Code, rec.Body.String())
	body := decodeBody(f.t, rec)
	tok.access, _ = body["access_token"].(string)
	tok.refresh, _ = body["refresh_token"].(string)
	require.True(f.t, strings.HasPrefix(tok.access, "mcp_oat_"))
	require.True(f.t, strings.HasPrefix(tok.refresh, "mcp_ort_"))
	return regID, tok
}

func (f *oauthFlow) refreshWith(regID, refresh string) *httptest.ResponseRecorder {
	return f.postForm("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {regID}})
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }
