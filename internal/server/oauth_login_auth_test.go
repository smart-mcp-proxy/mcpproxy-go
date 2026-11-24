//go:build !server

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// T036: mcp_oat_ is accepted only on its own resource and origin.
func TestOAuthLoginAuth_OnlyOnItsOwnResourceAndOrigin(t *testing.T) {
	f := newOAuthFlow(t, nil)
	_, tok := f.login("cursor")

	rec := f.get("/mcp/oauth/cursor", bearer(tok.access))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeBody(t, rec)
	assert.Equal(t, "cursor", got["client"])
	assert.Equal(t, "oauth", got["kind"])
	assert.NotEmpty(t, got["grant"])
	assert.Equal(t, "client-cursor", got["agent"], "labelled like the client's API key")
	// A trailing slash is the same address.
	assert.Equal(t, http.StatusOK, f.get("/mcp/oauth/cursor/", bearer(tok.access)).Code)
	// The scheme name is case-insensitive.
	assert.Equal(t, http.StatusOK, f.get("/mcp/oauth/cursor", map[string]string{"Authorization": "bearer " + tok.access}).Code)

	// another client's address, the generic address, another origin: 401 with the challenge
	for name, c := range map[string]struct {
		path, host string
	}{
		"other client": {"/mcp/oauth/windsurf", oauthTestHost},
		"generic":      {"/mcp/oauth", oauthTestHost},
		"other origin": {"/mcp/oauth/cursor", "localhost:8080"},
	} {
		other := *f
		other.host = c.host
		rec := other.get(c.path, bearer(tok.access))
		assert.Equal(t, http.StatusUnauthorized, rec.Code, name)
		assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "resource_metadata=", name)
	}
	assert.Equal(t, http.StatusNotFound, f.get("/mcp/oauth/no-such-client", bearer(tok.access)).Code)
}

// T036: every other credential is 401 on an OAuth address, with
// require_mcp_auth off, and never falls to the anonymous context.
func TestOAuthLoginAuth_OtherCredentialsRefused(t *testing.T) {
	for _, requireAuth := range []bool{false, true} {
		f := newOAuthFlow(t, func(c *config.Config) { c.RequireMCPAuth = requireAuth })
		hdrs := map[string]map[string]string{
			"none":             nil,
			"x-api-key":        {"X-API-Key": oauthLoginTestAPIKey},
			"bearer api key":   {"Authorization": "Bearer " + oauthLoginTestAPIKey},
			"mcp_cli_":         {"Authorization": "Bearer mcp_cli_0123456789abcdef"},
			"mcp_agt_":         {"Authorization": "Bearer mcp_agt_0123456789abcdef"},
			"garbage":          {"Authorization": "Bearer garbage"},
			"basic":            {"Authorization": "Basic Zm9vOmJhcg=="},
			"oat in x-api-key": {"X-API-Key": "mcp_oat_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		}
		for name, h := range hdrs {
			for _, path := range []string{"/mcp/oauth", "/mcp/oauth/cursor"} {
				rec := f.get(path, h)
				assert.Equal(t, http.StatusUnauthorized, rec.Code, "require=%v %s %s", requireAuth, name, path)
				assert.NotEmpty(t, rec.Header().Get("WWW-Authenticate"), name)
			}
		}
		rec := f.get("/mcp/oauth/cursor?apikey="+oauthLoginTestAPIKey, nil)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "?apikey=")
	}
}

// T036: mcp_oat_ on /mcp: 401 while the feature is on or was used, the
// previous (anonymous) behaviour while it is off and never used.
func TestOAuthLoginAuth_OatOnClassicMCP(t *testing.T) {
	const oat = "mcp_oat_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	hdr := bearer(oat)

	on := newOAuthFlow(t, nil)
	assert.Equal(t, http.StatusUnauthorized, on.get("/mcp", hdr).Code, "on")
	assert.Equal(t, http.StatusUnauthorized, on.get("/mcp/", hdr).Code, "on")

	off := newOAuthFlow(t, func(c *config.Config) { c.MCPOAuthEnabled = false })
	assert.Equal(t, http.StatusOK, off.get("/mcp", hdr).Code, "off and never used: unchanged anonymous behaviour")

	used := newOAuthFlow(t, func(c *config.Config) { c.MCPOAuthEnabled = false })
	used.srv.oauthLoginEverUsed = func() bool { return true }
	assert.Equal(t, http.StatusUnauthorized, used.get("/mcp", hdr).Code, "off after use")

	// require_mcp_auth off does not turn the refusal into an anonymous pass.
	require.False(t, on.srv.liveConfig().RequireMCPAuth)
}

// T036: a revoked login is refused on the next request, also for a client that
// kept the access token (and the refresh token no longer works).
func TestOAuthLoginAuth_RevokedLoginRefusedNextRequest(t *testing.T) {
	f := newOAuthFlow(t, nil)
	regID, tok := f.login("cursor")
	require.Equal(t, http.StatusOK, f.get("/mcp/oauth/cursor", bearer(tok.access)).Code)

	svc := f.srv.oauthLoginSvc()
	grants, err := svc.ListGrants("cursor")
	require.NoError(t, err)
	require.Len(t, grants, 1)
	changed, err := svc.RevokeGrant(adminCtx(), "cursor", grants[0].GrantID)
	require.NoError(t, err)
	require.True(t, changed)

	assert.Equal(t, http.StatusUnauthorized, f.get("/mcp/oauth/cursor", bearer(tok.access)).Code)
	assert.Equal(t, http.StatusBadRequest, f.refreshWith(regID, tok.refresh).Code)
}

// T036: switching the feature off refuses a live access token at once, and
// switching it back on restores the login (nothing was deleted).
func TestOAuthLoginAuth_SwitchOffRefusesAtOnce(t *testing.T) {
	f := newOAuthFlow(t, nil)
	_, tok := f.login("cursor")
	require.Equal(t, http.StatusOK, f.get("/mcp/oauth/cursor", bearer(tok.access)).Code)

	f.setEnabled(false)
	assert.Equal(t, http.StatusNotFound, f.get("/mcp/oauth/cursor", bearer(tok.access)).Code, "off after use: 404")
	f.setEnabled(true)
	assert.Equal(t, http.StatusOK, f.get("/mcp/oauth/cursor", bearer(tok.access)).Code, "off -> on restores the login")
}

// T036: profile scope equals what the client's API key would carry.
func TestOAuthLoginAuth_ProfileScopeFollowsClientBinding(t *testing.T) {
	f := newOAuthFlow(t, func(c *config.Config) {
		c.RequireMCPAuth = true // a named pin is only allowed with MCP auth required (FR-008a)
		c.Profiles = []config.ProfileConfig{{Name: "dev", Servers: []string{}}}
	})
	regID := f.register(oauthTestRedirect)
	verifier, challenge := pkce()
	resource := f.origin() + "/mcp/oauth/cursor"
	id := f.authorize(regID, oauthTestRedirect, challenge, resource)
	pin := "dev"
	redirect, err := f.srv.oauthLoginSvc().Approve(adminCtx(), id, runtime.ApprovalChoice{
		Client: runtime.ClientChoice{ID: "cursor"}, Profile: &pin, Mode: auth.ProfileModeLocked,
	})
	require.NoError(t, err)
	code := redirectParam(t, redirect, "code")
	rec := f.exchange(code, regID, verifier, resource)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	access, _ := decodeBody(t, rec)["access_token"].(string)

	got := decodeBody(t, f.get("/mcp/oauth/cursor", bearer(access)))
	want := auth.ClientAuthContext(auth.ClientIdentity{ClientID: "cursor", ProfilePin: "dev", ProfileMode: auth.ProfileModeLocked,
		CredentialKind: auth.CredentialKindOAuth})
	assert.Equal(t, "dev", got["pin"])
	assert.Equal(t, auth.ProfileModeLocked, got["mode"])
	assert.Equal(t, want.AgentName, got["agent"])
}

// newOAuthFlow's mux has no Touch on the sentinel: the "initialize name stored on
// the grant" behaviour is exercised against the real daemon in the in-process
// flow test (TestOAuthLogin_FullFlow_InProcess).

func redirectParam(t *testing.T, redirect, key string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, redirect, http.NoBody)
	v := req.URL.Query().Get(key)
	require.NotEmpty(t, v, redirect)
	return v
}

func (f *oauthFlow) setEnabled(on bool) {
	f.t.Helper()
	cfg := f.srv.runtime.Config()
	require.NotNil(f.t, cfg)
	next := *cfg
	next.MCPOAuthEnabled = on
	f.srv.runtime.UpdateConfig(&next, "")
}
