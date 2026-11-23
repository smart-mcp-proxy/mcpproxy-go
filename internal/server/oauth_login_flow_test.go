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

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The whole browser login against a real in-process daemon over real HTTP:
// discovery, register, authorize, the administrator's approval over the admin
// REST, token, an MCP session on the OAuth address, refresh, revoke, and the
// refusal of the next request of the still-open session.
func TestOAuthLogin_FullFlow_InProcess(t *testing.T) {
	env := NewTestEnvironmentWithOptions(t, TestEnvironmentOptions{
		Mutate: func(cfg *config.Config, _ string) { cfg.MCPOAuthEnabled = true },
	})
	t.Cleanup(env.Cleanup)
	base := strings.TrimSuffix(env.proxyAddr, "/mcp")
	const apiKey = "test-api-key-e2e"
	httpc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 20 * time.Second}

	do := func(method, path, ctype, body string, hdr map[string]string) (*http.Response, []byte) {
		req, err := http.NewRequest(method, base+path, bytes.NewBufferString(body))
		require.NoError(t, err)
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := httpc.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp, raw
	}
	asJSON := func(raw []byte) map[string]any {
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m), string(raw))
		return m
	}

	// 1. The address challenges, discovery documents point at this origin.
	resp, _ := do(http.MethodPost, "/mcp/oauth/cursor", "application/json", `{}`, nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), base+"/.well-known/oauth-protected-resource/mcp/oauth/cursor")
	resp, raw := do(http.MethodGet, "/.well-known/oauth-protected-resource/mcp/oauth/cursor", "", "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, base+"/mcp/oauth/cursor", asJSON(raw)["resource"])
	resp, raw = do(http.MethodGet, "/.well-known/oauth-authorization-server", "", "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, base, asJSON(raw)["issuer"])

	// 2. Register, authorize.
	redirect := "http://127.0.0.1:50999/callback"
	resp, raw = do(http.MethodPost, "/oauth/register", "application/json", `{"redirect_uris":["`+redirect+`"],"client_name":"Cursor IDE"}`, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	regID, _ := asJSON(raw)["client_id"].(string)
	verifier := strings.Repeat("z", 64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	resource := base + "/mcp/oauth/cursor"
	q := url.Values{"response_type": {"code"}, "client_id": {regID}, "redirect_uri": {redirect}, "code_challenge": {challenge},
		"code_challenge_method": {"S256"}, "resource": {resource}, "state": {"xyz"}}
	resp, _ = do(http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "", nil)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/ui/authorize", loc.Path)
	requestID := loc.Query().Get("request")
	require.NotEmpty(t, requestID)

	// 3. The administrator sees the request and approves it (admin REST, API key).
	admin := map[string]string{"X-API-Key": apiKey}
	resp, raw = do(http.MethodGet, "/api/v1/oauth/authorizations", "", "", admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.Contains(t, string(raw), requestID)
	resp, raw = do(http.MethodGet, "/api/v1/oauth/authorizations/"+requestID, "", "", admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	resp, raw = do(http.MethodPost, "/api/v1/oauth/authorizations/"+requestID+"/approve", "application/json", `{"client":{"id":"cursor"}}`, admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	redirectURL, _ := asJSON(raw)["data"].(map[string]any)["redirect_url"].(string)
	ru, err := url.Parse(redirectURL)
	require.NoError(t, err)
	assert.Equal(t, "xyz", ru.Query().Get("state"))
	code := ru.Query().Get("code")
	require.NotEmpty(t, code)

	// 4. Token.
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {regID},
		"code_verifier": {verifier}, "resource": {resource}}
	resp, raw = do(http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", form.Encode(), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	tok := asJSON(raw)
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	require.True(t, strings.HasPrefix(access, "mcp_oat_"))

	// 5. An MCP session on the OAuth address with the access token. The API key
	// is refused there.
	resp, _ = do(http.MethodPost, "/mcp/oauth/cursor", "application/json", `{}`, admin)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "the API key does not work on an OAuth address")

	newSession := func(token string) *client.Client {
		tr, terr := transport.NewStreamableHTTP(base+"/mcp/oauth/cursor", transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
		require.NoError(t, terr)
		c := client.NewClient(tr)
		require.NoError(t, c.Start(context.Background()))
		initReq := mcp.InitializeRequest{}
		initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		initReq.Params.ClientInfo = mcp.Implementation{Name: "cursor-vscode", Version: "1.0"}
		_, ierr := c.Initialize(context.Background(), initReq)
		require.NoError(t, ierr)
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	session := newSession(access)
	list, err := session.ListTools(context.Background(), mcp.ListToolsRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, list.Tools, "tools/list works on the OAuth address")

	// The name sent at initialize is stored on the login (advisory).
	svc := env.proxyServer.oauthLoginSvc()
	require.Eventually(t, func() bool {
		grants, gerr := svc.ListGrants("cursor")
		return gerr == nil && len(grants) == 1 && grants[0].ObservedClientName == "cursor-vscode"
	}, 5*time.Second, 50*time.Millisecond, "the name sent at initialize lands on the grant")

	// 6. Refresh rotates both tokens; the old refresh token is dead.
	form = url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {regID}}
	resp, raw = do(http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", form.Encode(), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	access2, _ := asJSON(raw)["access_token"].(string)
	assert.NotEqual(t, access, access2)
	// The previous access token no longer authenticates, so the open session
	// (still holding it) is refused on its next request.
	_, err = session.ListTools(context.Background(), mcp.ListToolsRequest{})
	assert.Error(t, err, "rotation replaced the access token; the old one is refused on the next request")
	session2 := newSession(access2)
	_, err = session2.ListTools(context.Background(), mcp.ListToolsRequest{})
	require.NoError(t, err)

	// 7. Revoke the login (admin REST): the next request of the live session is refused.
	grants, err := svc.ListGrants("cursor")
	require.NoError(t, err)
	require.Len(t, grants, 1)
	resp, raw = do(http.MethodDelete, "/api/v1/clients/cursor/logins/"+grants[0].GrantID, "", "", admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	_, err = session2.ListTools(context.Background(), mcp.ListToolsRequest{})
	assert.Error(t, err, "revoked login: refused on the next request of an open session")
	resp, _ = do(http.MethodPost, "/mcp/oauth/cursor", "application/json", `{}`, map[string]string{"Authorization": "Bearer " + access2})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
