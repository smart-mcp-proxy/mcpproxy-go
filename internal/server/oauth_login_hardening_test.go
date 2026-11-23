//go:build !server

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Item 1: register accepts exactly one JSON value within the size limit, and
// only as application/json.
func TestOAuthLoginHardening_RegisterBodyAndMediaType(t *testing.T) {
	f := newOAuthFlow(t, nil)
	obj := `{"redirect_uris":["https://a.example/1"]}`

	// padding past 16 KiB after a valid object is refused, not accepted
	rec := f.postJSON("/oauth/register", obj+strings.Repeat(" ", 17<<10))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	// a second JSON value
	rec = f.postJSON("/oauth/register", obj+` {"x":1}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "invalid_client_metadata", decodeBody(t, rec)["error"])
	// trailing whitespace within the limit is fine
	assert.Equal(t, http.StatusCreated, f.postJSON("/oauth/register", obj+"\n  ").Code)

	// media type
	for ct, want := range map[string]int{
		"application/json":                  http.StatusCreated,
		"application/json; charset=utf-8":   http.StatusCreated,
		"text/plain":                        http.StatusUnsupportedMediaType,
		"application/x-www-form-urlencoded": http.StatusUnsupportedMediaType,
		"":                                  http.StatusUnsupportedMediaType,
	} {
		req, _ := http.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(obj))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		assert.Equal(t, want, f.do(req).Code, "content type %q", ct)
	}
}

// Item 2: extra whitespace after "Bearer" is the same token on the classic routes.
func TestOAuthLoginHardening_BearerWhitespaceOnClassicMCP(t *testing.T) {
	const oat = "mcp_oat_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	on := newOAuthFlow(t, nil)
	assert.Equal(t, http.StatusUnauthorized, on.get("/mcp", map[string]string{"Authorization": "Bearer  " + oat}).Code)
	assert.Equal(t, http.StatusUnauthorized, on.get("/mcp", map[string]string{"Authorization": "Bearer \t" + oat}).Code)
	off := newOAuthFlow(t, func(c *config.Config) { c.MCPOAuthEnabled = false })
	assert.Equal(t, http.StatusOK, off.get("/mcp", map[string]string{"Authorization": "Bearer  " + oat}).Code, "off and never used: unchanged")
}

// Item 7: a web page cannot drive the public state-changing endpoints.
func TestOAuthLoginHardening_CrossSiteRefused(t *testing.T) {
	f := newOAuthFlow(t, nil)
	body := `{"redirect_uris":["https://a.example/1"]}`
	post := func(path, ctype, payload string, hdr map[string]string) int {
		req, _ := http.NewRequest(http.MethodPost, path, strings.NewReader(payload))
		req.Header.Set("Content-Type", ctype)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		return f.do(req).Code
	}
	form := url.Values{"grant_type": {"authorization_code"}}.Encode()
	for name, hdr := range map[string]map[string]string{
		"foreign origin": {"Origin": "https://evil.example"},
		"null origin":    {"Origin": "null"},
		"cross-site":     {"Sec-Fetch-Site": "cross-site"},
	} {
		assert.Equal(t, http.StatusForbidden, post("/oauth/register", "application/json", body, hdr), "register "+name)
		assert.Equal(t, http.StatusForbidden, post("/oauth/token", "application/x-www-form-urlencoded", form, hdr), "token "+name)
	}
	// Nothing was stored and no budget was spent by the refused requests.
	// Own origin, same-origin and absent headers are accepted.
	assert.Equal(t, http.StatusCreated, post("/oauth/register", "application/json", body, map[string]string{"Origin": oauthTestOrigin}))
	assert.Equal(t, http.StatusCreated, post("/oauth/register", "application/json", body, map[string]string{"Sec-Fetch-Site": "same-origin"}))
	assert.Equal(t, http.StatusCreated, post("/oauth/register", "application/json", body, nil))

	// The authorize GET is a top-level navigation and keeps working.
	regID := f.register(oauthTestRedirect)
	_, challenge := pkce()
	rec := f.get(f.authorizeQuery(regID, oauthTestRedirect, challenge, oauthTestOrigin+"/mcp/oauth/cursor", "s"), map[string]string{"Sec-Fetch-Site": "cross-site"})
	assert.Equal(t, http.StatusFound, rec.Code)
}

// Item 9: a request that went through a proxy is not a local one.
func TestOAuthLoginHardening_ProxyHeadersRefused(t *testing.T) {
	f := newOAuthFlow(t, nil)
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-IP"} {
		for _, path := range oauthLoginGatedPaths {
			assert.Equal(t, http.StatusNotFound, f.get(path, map[string]string{h: "203.0.113.9"}).Code, h+" "+path)
		}
		// Classic routes are untouched.
		assert.Equal(t, http.StatusOK, f.get("/mcp", map[string]string{h: "203.0.113.9"}).Code, "classic /mcp with "+h)
	}
	assert.Equal(t, http.StatusOK, f.get("/.well-known/oauth-authorization-server", nil).Code)
}

// Item 6: what is open on a login ends with the login.
func TestOAuthLogin_OpenStreamsEndWithTheLogin(t *testing.T) {
	env := NewTestEnvironmentWithOptions(t, TestEnvironmentOptions{
		Mutate: func(cfg *config.Config, _ string) { cfg.MCPOAuthEnabled = true },
	})
	t.Cleanup(env.Cleanup)
	base := strings.TrimSuffix(env.proxyAddr, "/mcp")
	svc := env.proxyServer.oauthLoginSvc()
	require.NotNil(t, svc)
	require.Zero(t, env.proxyServer.oauthTrackedRequests(), "nothing tracked before the first OAuth request")
	httpc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	login := func(client string) string {
		redirect := "http://127.0.0.1:50999/callback"
		resp, err := httpc.Post(base+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["`+redirect+`"]}`))
		require.NoError(t, err)
		var reg map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&reg))
		resp.Body.Close()
		regID, _ := reg["client_id"].(string)
		verifier := strings.Repeat("q", 50)
		sum := sha256.Sum256([]byte(verifier))
		resource := base + "/mcp/oauth/" + client
		q := url.Values{"response_type": {"code"}, "client_id": {regID}, "redirect_uri": {redirect},
			"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {resource}}
		resp, err = httpc.Get(base + "/oauth/authorize?" + q.Encode())
		require.NoError(t, err)
		resp.Body.Close()
		loc, err := url.Parse(resp.Header.Get("Location"))
		require.NoError(t, err)
		redir, err := svc.Approve(context.Background(), loc.Query().Get("request"), runtime.ApprovalChoice{Client: runtime.ClientChoice{ID: client}})
		require.NoError(t, err)
		ru, _ := url.Parse(redir)
		form := url.Values{"grant_type": {"authorization_code"}, "code": {ru.Query().Get("code")}, "redirect_uri": {redirect},
			"client_id": {regID}, "code_verifier": {verifier}, "resource": {resource}}
		resp, err = httpc.Post(base+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
		require.NoError(t, err)
		defer resp.Body.Close()
		var tok map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&tok))
		access, _ := tok["access_token"].(string)
		require.NotEmpty(t, access)
		return access
	}

	// openStream opens a session on the client's OAuth address and an SSE GET
	// stream on it; done closes when the stream ends.
	openStream := func(client, access string) (done chan struct{}) {
		do := func(method, body, session string) *http.Response {
			req, _ := http.NewRequest(method, base+"/mcp/oauth/"+client, bytes.NewBufferString(body))
			req.Header.Set("Authorization", "Bearer "+access)
			req.Header.Set("Accept", "application/json, text/event-stream")
			if method == http.MethodPost {
				req.Header.Set("Content-Type", "application/json")
			}
			if session != "" {
				req.Header.Set("Mcp-Session-Id", session)
			}
			resp, err := httpc.Do(req)
			require.NoError(t, err)
			return resp
		}
		resp := do(http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		session := resp.Header.Get("Mcp-Session-Id")
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		resp = do(http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, session)
		resp.Body.Close()
		stream := do(http.MethodGet, "", session)
		require.Equal(t, http.StatusOK, stream.StatusCode)
		done = make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, stream.Body)
			stream.Body.Close()
			close(done)
		}()
		return done
	}
	open := func(done chan struct{}, d time.Duration) bool {
		select {
		case <-done:
			return false
		case <-time.After(d):
			return true
		}
	}

	accessA := login("cursor")
	accessB := login("windsurf")
	streamA := openStream("cursor", accessA)
	streamB := openStream("windsurf", accessB)
	require.True(t, open(streamA, time.Second), "an authenticated stream stays open")
	require.True(t, open(streamB, 0))

	// Revoking A's login ends A's stream promptly and leaves B's open.
	grants, err := svc.ListGrants("cursor")
	require.NoError(t, err)
	require.Len(t, grants, 1)
	_, err = svc.RevokeGrant(context.Background(), "cursor", grants[0].GrantID)
	require.NoError(t, err)
	select {
	case <-streamA:
	case <-time.After(5 * time.Second):
		t.Fatal("the revoked login's stream is still open")
	}
	require.True(t, open(streamB, time.Second), "an unrelated login's stream stays open")

	// Switching the feature off ends the rest.
	cfg := env.proxyServer.runtime.Config()
	off := *cfg
	off.MCPOAuthEnabled = false
	env.proxyServer.runtime.UpdateConfig(&off, "")
	select {
	case <-streamB:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream survived the switch-off")
	}
	require.Eventually(t, func() bool { return env.proxyServer.oauthTrackedRequests() == 0 }, 5*time.Second, 50*time.Millisecond,
		"ended streams leave nothing behind")
}

// A tracked request ends at its access token's expiry and leaves no entry.
func TestOAuthLoginHardening_RegistryExpiryAndRelease(t *testing.T) {
	srv := newOAuthLoginTestServer(t, func(c *config.Config) { c.MCPOAuthEnabled = true })
	ctx, cancel := context.WithCancel(context.Background())
	release := srv.trackOAuthRequest("g1", time.Now().Add(150*time.Millisecond), 0, cancel)
	require.Equal(t, 1, srv.oauthTrackedRequests())
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the request outlived its access token")
	}
	release()
	assert.Zero(t, srv.oauthTrackedRequests())

	// A request that ends normally leaves nothing behind.
	release2 := srv.trackOAuthRequest("g2", time.Now().Add(time.Hour), 0, func() {})
	release2()
	assert.Zero(t, srv.oauthTrackedRequests())
}

// oauthTrackedRequests is the number of requests currently tracked (tests).
func (s *Server) oauthTrackedRequests() int {
	r := s.oauthStreams.Load()
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
