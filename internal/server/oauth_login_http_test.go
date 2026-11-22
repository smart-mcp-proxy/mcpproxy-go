//go:build !server

package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T033: metadata and the 401 challenge, unknown client id -> 404 everywhere.
func TestOAuthLoginHTTP_MetadataAndChallenge(t *testing.T) {
	f := newOAuthFlow(t, nil)

	as := f.get("/.well-known/oauth-authorization-server", nil)
	require.Equal(t, http.StatusOK, as.Code)
	body := decodeBody(t, as)
	assert.Equal(t, oauthTestOrigin, body["issuer"])
	assert.Equal(t, oauthTestOrigin+"/oauth/token", body["token_endpoint"])
	assert.Equal(t, oauthTestOrigin+"/oauth/register", body["registration_endpoint"])
	assert.Equal(t, []any{"S256"}, body["code_challenge_methods_supported"])
	assert.Equal(t, []any{"none"}, body["token_endpoint_auth_methods_supported"])
	assert.Equal(t, "no-store", as.Header().Get("Cache-Control"))

	for _, c := range []struct{ meta, resource string }{
		{"/.well-known/oauth-protected-resource/mcp/oauth", "/mcp/oauth"},
		{"/.well-known/oauth-protected-resource/mcp/oauth/", "/mcp/oauth"},
		{"/.well-known/oauth-protected-resource/mcp/oauth/cursor", "/mcp/oauth/cursor"},
	} {
		rec := f.get(c.meta, nil)
		require.Equal(t, http.StatusOK, rec.Code, c.meta)
		pr := decodeBody(t, rec)
		assert.Equal(t, oauthTestOrigin+c.resource, pr["resource"], c.meta)
		assert.Equal(t, []any{oauthTestOrigin}, pr["authorization_servers"], c.meta)
		assert.Equal(t, []any{"header"}, pr["bearer_methods_supported"], c.meta)
	}

	for _, path := range []string{"/mcp/oauth", "/mcp/oauth/", "/mcp/oauth/cursor"} {
		rec := f.get(path, nil)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, path)
		clientPart := strings.TrimSuffix(strings.TrimPrefix(path, "/mcp/oauth"), "/")
		assert.Equal(t, `Bearer resource_metadata="`+oauthTestOrigin+`/.well-known/oauth-protected-resource/mcp/oauth`+clientPart+`"`,
			rec.Header().Get("WWW-Authenticate"), path)
	}

	// An unknown client id is 404 on the address and on its metadata. So is an
	// address nested deeper than a client id.
	for _, path := range []string{
		"/mcp/oauth/no-such-client", "/.well-known/oauth-protected-resource/mcp/oauth/no-such-client",
		"/mcp/oauth/cursor/extra", "/.well-known/oauth-protected-resource/mcp/oauth/cursor/extra",
		"/mcp/oauth/Bad_ID!",
	} {
		assert.Equal(t, http.StatusNotFound, f.get(path, nil).Code, path)
	}
}

// T034: register.
func TestOAuthLoginHTTP_Register(t *testing.T) {
	f := newOAuthFlow(t, nil)
	rec := f.postJSON("/oauth/register", `{"redirect_uris":["http://127.0.0.1:1/cb"],"client_name":"Cursor IDE","software_id":"x","software_version":"1"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	body := decodeBody(t, rec)
	assert.NotEmpty(t, body["client_id"])
	assert.NotContains(t, body, "client_secret")
	assert.Equal(t, "none", body["token_endpoint_auth_method"])
	assert.Equal(t, []any{"authorization_code", "refresh_token"}, body["grant_types"])
	assert.Equal(t, []any{"code"}, body["response_types"])
	assert.Equal(t, "cursor ide", body["client_name"], "the reported name is sanitised")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	cases := map[string]struct{ body, code string }{
		"plain http non-loopback": {`{"redirect_uris":["http://example.com/cb"]}`, "invalid_redirect_uri"},
		"fragment":                {`{"redirect_uris":["https://example.com/cb#x"]}`, "invalid_redirect_uri"},
		"custom scheme":           {`{"redirect_uris":["cursor://callback"]}`, "invalid_redirect_uri"},
		"none":                    {`{}`, "invalid_redirect_uri"},
		"six":                     {`{"redirect_uris":["https://a.example/1","https://a.example/2","https://a.example/3","https://a.example/4","https://a.example/5","https://a.example/6"]}`, "invalid_redirect_uri"},
		"secret method":           {`{"redirect_uris":["https://a.example/1"],"token_endpoint_auth_method":"client_secret_basic"}`, "invalid_client_metadata"},
		"grant type":              {`{"redirect_uris":["https://a.example/1"],"grant_types":["client_credentials"]}`, "invalid_client_metadata"},
		"response type":           {`{"redirect_uris":["https://a.example/1"],"response_types":["token"]}`, "invalid_client_metadata"},
		"not json":                {`nope`, "invalid_client_metadata"},
	}
	for name, c := range cases {
		rec := f.postJSON("/oauth/register", c.body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, name)
		assert.Equal(t, c.code, decodeBody(t, rec)["error"], name)
	}

	// Method and size.
	assert.Equal(t, http.StatusMethodNotAllowed, f.get("/oauth/register", nil).Code)
	big := `{"redirect_uris":["https://a.example/1"],"client_name":"` + strings.Repeat("a", 17<<10) + `"}`
	assert.Equal(t, http.StatusRequestEntityTooLarge, f.postJSON("/oauth/register", big).Code)
}

func TestOAuthLoginHTTP_Register_RateLimited(t *testing.T) {
	f := newOAuthFlow(t, nil)
	f.limited = true
	var limited bool
	for i := 0; i < 20; i++ {
		rec := f.postJSON("/oauth/register", `{"redirect_uris":["https://a.example/1"]}`)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			assert.NotEmpty(t, rec.Header().Get("Retry-After"))
			assert.Equal(t, "temporarily_unavailable", decodeBody(t, rec)["error"])
			break
		}
	}
	assert.True(t, limited, "a burst past the register budget must answer 429 with Retry-After")
}

// T034: authorize.
func TestOAuthLoginHTTP_Authorize(t *testing.T) {
	f := newOAuthFlow(t, nil)
	cb := "http://127.0.0.1:54321/callback?x=1"
	regID := f.register(cb)
	_, challenge := pkce()
	resource := oauthTestOrigin + "/mcp/oauth/cursor"
	check := func(h http.Header) {
		t.Helper()
		assert.Equal(t, "no-store", h.Get("Cache-Control"))
		assert.Equal(t, "no-referrer", h.Get("Referrer-Policy"))
	}

	// Success: 302 to the consent page.
	rec := f.get(f.authorizeQuery(regID, cb, challenge, resource, "s"), nil)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.True(t, strings.HasPrefix(rec.Header().Get("Location"), oauthTestOrigin+"/ui/authorize?request="), rec.Header().Get("Location"))
	check(rec.Header())

	// The loopback port is ignored at match time.
	rec = f.get(f.authorizeQuery(regID, "http://127.0.0.1:9999/callback?x=1", challenge, resource, "s"), nil)
	assert.Equal(t, http.StatusFound, rec.Code)

	// No redirect for an unknown client or an unregistered redirect URI.
	for name, q := range map[string]string{
		"unknown client":  f.authorizeQuery("nope", cb, challenge, resource, ""),
		"other redirect":  f.authorizeQuery(regID, "http://127.0.0.1:54321/other", challenge, resource, ""),
		"remote redirect": f.authorizeQuery(regID, "https://evil.example/cb", challenge, resource, ""),
	} {
		rec := f.get(q, nil)
		assert.Equal(t, http.StatusBadRequest, rec.Code, name)
		assert.Empty(t, rec.Header().Get("Location"), name)
		assert.Contains(t, rec.Header().Get("Content-Type"), "text/html", name)
		check(rec.Header())
	}

	// An unknown per-client resource is a 404 page, never a redirect.
	rec = f.get(f.authorizeQuery(regID, cb, challenge, oauthTestOrigin+"/mcp/oauth/no-such-client", "s"), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, rec.Header().Get("Location"))
	check(rec.Header())

	// Redirectable errors keep the callback query and return state unchanged.
	state := "a b&c=d/é?#"
	rec = f.get(f.authorizeQuery(regID, cb, challenge, "https://other.example/mcp", state), nil)
	require.Equal(t, http.StatusFound, rec.Code)
	loc, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "/callback", loc.Path)
	assert.Equal(t, "1", loc.Query().Get("x"), "the existing callback query is kept")
	assert.Equal(t, "invalid_target", loc.Query().Get("error"))
	assert.Equal(t, state, loc.Query().Get("state"), "state comes back exactly as sent")
	check(rec.Header())

	// No state sent -> none returned.
	rec = f.get(f.authorizeQuery(regID, cb, challenge, "https://other.example/mcp", ""), nil)
	loc, _ = url.Parse(rec.Header().Get("Location"))
	assert.False(t, loc.Query().Has("state"))

	// Wrong response_type and a missing challenge are redirectable too.
	rec = f.get("/oauth/authorize?response_type=token&client_id="+regID+"&redirect_uri="+url.QueryEscape(cb)+"&resource="+url.QueryEscape(resource), nil)
	loc, _ = url.Parse(rec.Header().Get("Location"))
	assert.Equal(t, "unsupported_response_type", loc.Query().Get("error"))
	rec = f.get("/oauth/authorize?response_type=code&client_id="+regID+"&redirect_uri="+url.QueryEscape(cb)+"&resource="+url.QueryEscape(resource), nil)
	loc, _ = url.Parse(rec.Header().Get("Location"))
	assert.Equal(t, "invalid_request", loc.Query().Get("error"))

	// POST is not an authorize method.
	req, _ := http.NewRequest(http.MethodPost, "/oauth/authorize", http.NoBody)
	assert.Equal(t, http.StatusMethodNotAllowed, f.do(req).Code)
}

// T034: token, every row of the contract's error table.
func TestOAuthLoginHTTP_Token_Errors(t *testing.T) {
	f := newOAuthFlow(t, nil)
	regID := f.register(oauthTestRedirect)
	verifier, challenge := pkce()
	resource := oauthTestOrigin + "/mcp/oauth/cursor"
	mint := func() string {
		id := f.authorize(regID, oauthTestRedirect, challenge, resource)
		return f.approve(id, "cursor")
	}

	// unknown code
	rec := f.exchange("mcp_code_unknown", regID, verifier, resource)
	assert.Equal(t, 400, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"])

	// wrong verifier, wrong client, wrong redirect: invalid_grant
	rec = f.exchange(mint(), regID, strings.Repeat("w", 43), resource)
	assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"], "wrong verifier")
	rec = f.exchange(mint(), "other-client", verifier, resource)
	assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"], "wrong client")
	rec = f.postForm("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {mint()}, "redirect_uri": {"http://127.0.0.1:54321/elsewhere"},
		"client_id": {regID}, "code_verifier": {verifier},
	})
	assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"], "wrong redirect uri")

	// resource substitution -> invalid_target
	code := mint()
	rec = f.exchange(code, regID, verifier, oauthTestOrigin+"/mcp/oauth/windsurf")
	assert.Equal(t, 400, rec.Code)
	assert.Equal(t, "invalid_target", decodeBody(t, rec)["error"])

	// origin mismatch: invalid_grant and the code is NOT consumed
	code = mint()
	other := *f
	other.host = "localhost:8080"
	rec = other.exchange(code, regID, verifier, resource)
	assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"], "origin mismatch")
	rec = f.exchange(code, regID, verifier, resource)
	require.Equal(t, 200, rec.Code, "the code survives a request from another origin: %s", rec.Body.String())

	// a reused code revokes the login it produced
	access, _ := decodeBody(t, rec)["access_token"].(string)
	assert.Equal(t, http.StatusOK, f.get("/mcp/oauth/cursor", bearer(access)).Code)
	rec = f.exchange(code, regID, verifier, resource)
	assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"], "reuse")
	assert.Equal(t, http.StatusUnauthorized, f.get("/mcp/oauth/cursor", bearer(access)).Code, "reuse revokes the login")

	// grant types
	rec = f.postForm("/oauth/token", url.Values{"grant_type": {"password"}})
	assert.Equal(t, 400, rec.Code)
	assert.Equal(t, "unsupported_grant_type", decodeBody(t, rec)["error"])
	rec = f.postForm("/oauth/token", url.Values{})
	assert.Equal(t, "unsupported_grant_type", decodeBody(t, rec)["error"])

	// content type, size and method
	req, _ := http.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	assert.Equal(t, 400, f.do(req).Code)
	big := url.Values{"grant_type": {"authorization_code"}, "code": {strings.Repeat("a", 17<<10)}}
	assert.Equal(t, http.StatusRequestEntityTooLarge, f.postForm("/oauth/token", big).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, f.get("/oauth/token", nil).Code)
}

func TestOAuthLoginHTTP_Token_RefreshRules(t *testing.T) {
	f := newOAuthFlow(t, nil)
	regID, tok := f.login("cursor")

	// malformed, bad MAC, unknown, wrong client: one indistinguishable answer
	forged := tok.refresh[:len(tok.refresh)-4] + "AAAA"
	for name, r := range map[string]string{"malformed": "nonsense", "bad mac": forged, "empty": ""} {
		rec := f.refreshWith(regID, r)
		assert.Equal(t, 400, rec.Code, name)
		assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"], name)
	}
	rec := f.refreshWith("some-other-client", tok.refresh)
	assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"], "wrong client")

	// resource substitution
	rec = f.postForm("/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {tok.refresh}, "client_id": {regID},
		"resource": {oauthTestOrigin + "/mcp/oauth/windsurf"},
	})
	assert.Equal(t, "invalid_target", decodeBody(t, rec)["error"])

	// rotation: the new pair works, the old refresh token is an authentic replay
	// that revokes the login.
	rec = f.refreshWith(regID, tok.refresh)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	next := decodeBody(t, rec)
	assert.NotEqual(t, tok.refresh, next["refresh_token"])
	newAccess, _ := next["access_token"].(string)
	assert.Equal(t, http.StatusOK, f.get("/mcp/oauth/cursor", bearer(newAccess)).Code)
	rec = f.refreshWith(regID, tok.refresh)
	assert.Equal(t, "invalid_grant", decodeBody(t, rec)["error"], "replay")
	assert.Equal(t, http.StatusUnauthorized, f.get("/mcp/oauth/cursor", bearer(newAccess)).Code, "a replay revokes the login")
}

func TestOAuthLoginHTTP_Token_RateLimited(t *testing.T) {
	f := newOAuthFlow(t, nil)
	f.limited = true
	var limited bool
	for i := 0; i < 40; i++ {
		rec := f.postForm("/oauth/token", url.Values{"grant_type": {"authorization_code"}})
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			assert.NotEmpty(t, rec.Header().Get("Retry-After"))
			break
		}
	}
	assert.True(t, limited)
}
