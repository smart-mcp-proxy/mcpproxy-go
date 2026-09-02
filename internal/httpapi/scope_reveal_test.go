package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// ---------------------------------------------------------------------------
// #1167 — reveal_secret_headers must be ANDed with caller identity
// ---------------------------------------------------------------------------

// TestGetServers_RevealRequiresAuthenticatedAdmin pins the sharpest half of
// #1167: with reveal_secret_headers on, a scoped read-only agent token received
// all four credential classes in plaintext on GET /api/v1/servers.
//
// The oracle is VALUE EQUALITY against the same fixture put through the shared
// redactor, not "the body does not contain the secret" — an empty body, a 500
// or a dropped field would pass a substring-absence oracle vacuously. The
// agent is entitled to SEE `alpha`, so its presence is asserted as a positive
// control: only the credential is withheld.
func TestGetServers_RevealRequiresAuthenticatedAdmin(t *testing.T) {
	expectedMasked := scopeFixtureServers()[0]
	oauth.RedactServerSecretFields(&expectedMasked)

	t.Run("agent token gets masked values", func(t *testing.T) {
		ctrl := &scopeController{cfg: scopeFixtureConfig(true), servers: scopeFixtureServers(), withManagement: true}
		srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

		rec := scopeGet(t, srv, "/api/v1/servers", token)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		data := scopeDecodeData(t, rec)

		// Positive control: the fixture landed and the route matched.
		require.Contains(t, scopeServerNames(t, data), "alpha",
			"precondition: the scoped agent must still SEE its own server")

		entry := scopeServerEntry(t, data, "alpha")
		headers, _ := entry["headers"].(map[string]interface{})
		require.NotNil(t, headers, "alpha must still carry a headers map: %#v", entry)
		assert.Equal(t, expectedMasked.Headers["Authorization"], headers["Authorization"],
			"an agent token must get the SHARED redactor's output, not the raw Bearer token")
		assert.Equal(t, expectedMasked.URL, entry["url"],
			"the URL query credential must take the shared live rule for an agent token")
		assert.NotContains(t, rec.Body.String(), alphaHeaderSecret)
		assert.NotContains(t, rec.Body.String(), alphaQuerySecret)
	})

	t.Run("admin api key still gets the operator opt-in", func(t *testing.T) {
		ctrl := &scopeController{cfg: scopeFixtureConfig(true), servers: scopeFixtureServers(), withManagement: true}
		srv, _ := scopedAgentServer(t, ctrl, []string{"alpha"})

		rec := scopeGet(t, srv, "/api/v1/servers", scopeAdminAPIKey)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		data := scopeDecodeData(t, rec)

		entry := scopeServerEntry(t, data, "alpha")
		headers, _ := entry["headers"].(map[string]interface{})
		require.NotNil(t, headers)
		assert.Equal(t, "Bearer "+alphaHeaderSecret, headers["Authorization"],
			"this is a NARROWING, not a removal: the operator opt-in must still work for an admin")
	})

	t.Run("no auth context at all is unprivileged", func(t *testing.T) {
		// The middleware's testing/bootstrap passthrough forwards with NO
		// AuthContext. Absence of an identity must not satisfy a gate that a
		// scoped token fails.
		ctrl := &scopeController{cfg: scopeFixtureConfig(true), servers: scopeFixtureServers(), withManagement: true}
		srv := NewServer(passthroughController{ctrl}, zap.NewNop().Sugar(), nil)

		rec := scopeGet(t, srv, "/api/v1/servers", "")
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		data := scopeDecodeData(t, rec)
		entry := scopeServerEntry(t, data, "alpha")
		headers, _ := entry["headers"].(map[string]interface{})
		require.NotNil(t, headers)
		assert.Equal(t, expectedMasked.Headers["Authorization"], headers["Authorization"],
			"an unauthenticated caller must not be handed raw credentials")
	})
}

// passthroughController forces apiKeyAuthMiddleware down its no-usable-config
// branch (GetCurrentConfig returns a non-*config.Config), which forwards the
// request with a nil AuthContext.
type passthroughController struct{ *scopeController }

func (p passthroughController) GetCurrentConfig() interface{} { return map[string]interface{}{} }

// TestGetServerDiagnostics_RevealRequiresAuthenticatedAdmin covers the
// per-server diagnostics door, where health.detail echoes the raw connect error
// with its URL credential.
//
// The oracle keys on FIELD VALUES only. A NotContains(body, "beta") oracle
// would be meaningless here: the server id is a path parameter this route
// echoes back into the body.
func TestGetServerDiagnostics_RevealRequiresAuthenticatedAdmin(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(true), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha", "beta"})

	t.Run("agent token gets the scrubbed detail", func(t *testing.T) {
		rec := scopeGet(t, srv, "/api/v1/servers/beta/diagnostics", token)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		data := scopeDecodeData(t, rec)
		health, _ := data["health"].(map[string]interface{})
		require.NotNil(t, health, "precondition: the fixture must carry a health block: %#v", data)
		assert.Equal(t, oauth.ScrubUpstreamText(betaHealthDetail), health["detail"],
			"health.detail must take the one shared free-text rule for a non-admin caller")
	})

	t.Run("admin api key still gets the raw detail", func(t *testing.T) {
		rec := scopeGet(t, srv, "/api/v1/servers/beta/diagnostics", scopeAdminAPIKey)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		data := scopeDecodeData(t, rec)
		health, _ := data["health"].(map[string]interface{})
		require.NotNil(t, health)
		assert.Equal(t, betaHealthDetail, health["detail"],
			"the operator opt-in must still work for an authenticated admin")
	})
}

// ---------------------------------------------------------------------------
// #1166 — REST enumeration must honour allowed_servers
// ---------------------------------------------------------------------------

// TestGetServers_AgentTokenSeesOnlyAllowedSubset_ManagementPath pins the
// management-service branch. The stats half is what a naive array-only filter
// fails: total_servers would stay an exact count oracle for what was hidden.
func TestGetServers_AgentTokenSeesOnlyAllowedSubset_ManagementPath(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	rec := scopeGet(t, srv, "/api/v1/servers", token)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	data := scopeDecodeData(t, rec)

	assert.Equal(t, []string{"alpha"}, scopeServerNames(t, data),
		"a token scoped to alpha must enumerate exactly alpha")

	stats, _ := data["stats"].(map[string]interface{})
	require.NotNil(t, stats, "precondition: the response must carry stats: %#v", data)
	assert.Equal(t, float64(1), stats["total_servers"], "stats must narrow with the array")
	assert.Equal(t, float64(3), stats["total_tools"], "total_tools must count only alpha's tools")
}

// TestGetServers_AgentTokenSeesOnlyAllowedSubset_LegacyPath pins the OTHER
// branch. A fix applied only inside the `if mgmtSvc != nil` block passes the
// management test and fails this one.
func TestGetServers_AgentTokenSeesOnlyAllowedSubset_LegacyPath(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: false}
	require.Nil(t, ctrl.GetManagementService(),
		"precondition: this harness must genuinely select the legacy fallback")
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	rec := scopeGet(t, srv, "/api/v1/servers", token)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	data := scopeDecodeData(t, rec)

	assert.Equal(t, []string{"alpha"}, scopeServerNames(t, data))
	stats, _ := data["stats"].(map[string]interface{})
	require.NotNil(t, stats)
	assert.Equal(t, float64(1), stats["total_servers"])
	assert.Equal(t, float64(3), stats["total_tools"])
}

// TestGetServers_AdminContextsUnfiltered is a REGRESSION GUARD, not a bite
// test: it asserts the unfiltered result, which is what unfixed code returns
// too. It exists because a fix that empties an admin's server list is a worse
// bug than the one being fixed, and case (c) — the middleware's nil-AuthContext
// passthrough — is the regression that would blank the list for most of this
// package's existing tests.
func TestGetServers_AdminContextsUnfiltered(t *testing.T) {
	newCtrl := func() *scopeController {
		return &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	}

	t.Run("global api key", func(t *testing.T) {
		srv, _ := scopedAgentServer(t, newCtrl(), []string{"alpha"})
		data := scopeDecodeData(t, scopeGet(t, srv, "/api/v1/servers", scopeAdminAPIKey))
		assert.ElementsMatch(t, []string{"alpha", "beta"}, scopeServerNames(t, data))
		stats, _ := data["stats"].(map[string]interface{})
		require.NotNil(t, stats)
		assert.Equal(t, float64(2), stats["total_servers"])
		assert.Equal(t, float64(10), stats["total_tools"])
	})

	t.Run("tray unix socket", func(t *testing.T) {
		srv, _ := scopedAgentServer(t, newCtrl(), []string{"alpha"})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", http.NoBody)
		req = req.WithContext(transport.TagConnectionContext(req.Context(), transport.ConnectionSourceTray))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		assert.ElementsMatch(t, []string{"alpha", "beta"}, scopeServerNames(t, scopeDecodeData(t, rec)))
	})

	t.Run("no auth context passthrough", func(t *testing.T) {
		srv := NewServer(passthroughController{newCtrl()}, zap.NewNop().Sugar(), nil)
		rec := scopeGet(t, srv, "/api/v1/servers", "")
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		assert.ElementsMatch(t, []string{"alpha", "beta"}, scopeServerNames(t, scopeDecodeData(t, rec)),
			"absence of a token must be treated as unrestricted, exactly as auth.AuthorizeServerOp does")
	})
}

// TestGetConfig_AgentTokenForbidden_AdminUnaffected covers BOTH issues on the
// widest door on the tree. It is not in #1167's site list but the live
// reproduction showed it leaking every credential class AND the whole
// inventory, and the document also carries the global admin api_key — a
// privilege-escalation primitive for a scoped token.
func TestGetConfig_AgentTokenForbidden_AdminUnaffected(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(true), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	t.Run("agent token is denied", func(t *testing.T) {
		rec := scopeGet(t, srv, "/api/v1/config", token)
		require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
		body := rec.Body.String()
		assert.NotContains(t, body, scopeAdminAPIKey, "the admin api_key must never reach an agent token")
		assert.NotContains(t, body, alphaHeaderSecret)
		assert.NotContains(t, body, alphaQuerySecret)
		assert.NotContains(t, body, betaArgvSecret)
		assert.NotContains(t, body, betaEnvSecret)
		assert.NotContains(t, body, `"beta"`, "the config document must not enumerate a server outside the token's scope")
	})

	t.Run("admin api key is unaffected", func(t *testing.T) {
		rec := scopeGet(t, srv, "/api/v1/config", scopeAdminAPIKey)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		body := rec.Body.String()
		assert.Contains(t, body, `"alpha"`, "an admin must still read the whole document")
		assert.Contains(t, body, `"beta"`)
		assert.Contains(t, body, alphaHeaderSecret,
			"with reveal_secret_headers on, an authenticated admin still gets raw values")
	})
}

// TestGetStatus_UpstreamStatsScoped pins the most-polled door. The nested
// assertion matters: withLiveUpstreamStats rebuilds data.status from the same
// map, so a fix that filters only the top-level field leaves the nested copy
// leaking.
func TestGetStatus_UpstreamStatsScoped(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	statsKeys := func(data map[string]interface{}, path ...string) map[string]interface{} {
		cur := data
		for _, p := range path {
			next, ok := cur[p].(map[string]interface{})
			require.True(t, ok, "missing %q in %#v", p, cur)
			cur = next
		}
		return cur
	}

	t.Run("agent token sees only its own server", func(t *testing.T) {
		rec := scopeGet(t, srv, "/api/v1/status", token)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		data := scopeDecodeData(t, rec)

		top := statsKeys(data, "upstream_stats")
		servers := statsKeys(data, "upstream_stats", "servers")
		require.Len(t, servers, 1, "%#v", servers)
		require.Contains(t, servers, "alpha", "precondition: the allowed server must still be reported")
		assert.Equal(t, float64(1), top["total_servers"],
			"the sibling scalar must be recomputed or it stays an exact count oracle")
		assert.Equal(t, float64(3), top["total_tools"])

		nested := statsKeys(data, "status", "upstream_stats", "servers")
		require.Len(t, nested, 1, "the NESTED snapshot must narrow too: %#v", nested)
		require.Contains(t, nested, "alpha")
	})

	t.Run("admin sees everything", func(t *testing.T) {
		rec := scopeGet(t, srv, "/api/v1/status", scopeAdminAPIKey)
		data := scopeDecodeData(t, rec)
		servers := statsKeys(data, "upstream_stats", "servers")
		assert.Len(t, servers, 2)
		assert.Equal(t, float64(2), statsKeys(data, "upstream_stats")["total_servers"])
	})
}

// TestGetGlobalTools_ScopedToAllowedServers. After #1166 filters /api/v1/servers
// a scoped token would otherwise get a STRICTLY LARGER inventory here — every
// server_name plus every tool name and description. Its MCP twin filters via
// serverInScope.
func TestGetGlobalTools_ScopedToAllowedServers(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	toolServerNames := func(rec *httptest.ResponseRecorder) []string {
		data := scopeDecodeData(t, rec)
		raw, ok := data["tools"].([]interface{})
		require.True(t, ok, "no tools array in %#v", data)
		names := make([]string, 0, len(raw))
		for _, entry := range raw {
			m, _ := entry.(map[string]interface{})
			name, _ := m["server_name"].(string)
			names = append(names, name)
		}
		return names
	}

	agent := scopeGet(t, srv, "/api/v1/tools", token)
	require.Equal(t, http.StatusOK, agent.Code, "body: %s", agent.Body.String())
	assert.Equal(t, []string{"alpha"}, toolServerNames(agent),
		"GET /api/v1/tools must not hand back the inventory GET /api/v1/servers just denied")

	admin := scopeGet(t, srv, "/api/v1/tools", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code)
	assert.ElementsMatch(t, []string{"alpha", "beta"}, toolServerNames(admin),
		"an admin must still see every server's tools")
}

// TestSearchTools_ScopedToAllowedServers covers the discovery surface, whose
// MCP twin (retrieve_tools) filters through serverInScope.
func TestSearchTools_ScopedToAllowedServers(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	hits := func(rec *httptest.ResponseRecorder) []string {
		data := scopeDecodeData(t, rec)
		raw, ok := data["results"].([]interface{})
		require.True(t, ok, "no results array in %#v", data)
		names := make([]string, 0, len(raw))
		for _, entry := range raw {
			m, _ := entry.(map[string]interface{})
			tool, _ := m["tool"].(map[string]interface{})
			name, _ := tool["server_name"].(string)
			names = append(names, name)
		}
		return names
	}

	agent := scopeGet(t, srv, "/api/v1/index/search?q=tool", token)
	require.Equal(t, http.StatusOK, agent.Code, "body: %s", agent.Body.String())
	assert.Equal(t, []string{"alpha"}, hits(agent))

	admin := scopeGet(t, srv, "/api/v1/index/search?q=tool", scopeAdminAPIKey)
	assert.ElementsMatch(t, []string{"alpha", "beta"}, hits(admin))
}

// TestServerDiagnostics_UnentitledIsIndistinguishableFromAbsent is the
// STATUS-PARITY oracle. "The body must not contain the server name" would pass
// vacuously here — a 404 on this route echoes the caller's own path parameter
// straight back into the message.
//
// The positive control on the same router is what stops the whole test passing
// for the wrong reason (a broken fixture, a route that never matched, a 404 for
// everyone).
func TestServerDiagnostics_UnentitledIsIndistinguishableFromAbsent(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	// Positive control: the fixture landed, the route matched, and the token
	// genuinely reaches the handler.
	allowed := scopeGet(t, srv, "/api/v1/servers/alpha/diagnostics", token)
	require.Equal(t, http.StatusOK, allowed.Code,
		"precondition: the scoped token must reach this route for its OWN server; body: %s", allowed.Body.String())

	// Second control: `beta` really does exist, and an admin can see it here.
	adminBeta := scopeGet(t, srv, "/api/v1/servers/beta/diagnostics", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, adminBeta.Code,
		"precondition: beta must exist and be visible to an admin; body: %s", adminBeta.Body.String())

	unentitled := scopeGet(t, srv, "/api/v1/servers/beta/diagnostics", token)
	absent := scopeGet(t, srv, "/api/v1/servers/does-not-exist-at-all/diagnostics", token)

	assert.Equal(t, absent.Code, unentitled.Code,
		"an unentitled server must return the same status as one that does not exist")
	assert.Equal(t, http.StatusNotFound, absent.Code,
		"precondition: an absent server must be distinguishable from a successful read, or the parity above is vacuous")

	// Same body modulo the echoed path parameter and the per-request id.
	requestIDPattern := regexp.MustCompile(`"request_id":"[^"]*"`)
	normalize := func(rec *httptest.ResponseRecorder, name string) string {
		body := strings.ReplaceAll(rec.Body.String(), name, "<name>")
		return requestIDPattern.ReplaceAllString(body, `"request_id":"<id>"`)
	}
	assert.Equal(t, normalize(absent, "does-not-exist-at-all"), normalize(unentitled, "beta"),
		"the two responses must be byte-identical once the echoed name is normalised")
}

// ---------------------------------------------------------------------------
// The shared predicate itself
// ---------------------------------------------------------------------------

func TestRevealSecretsAllowed_RequiresBothHalves(t *testing.T) {
	admin := auth.WithAuthContext(t.Context(), auth.AdminContext())
	agent := auth.WithAuthContext(t.Context(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}})
	anon := auth.WithAuthContext(t.Context(), auth.AnonymousContext())

	assert.True(t, auth.RevealSecretsAllowed(admin, true))
	assert.False(t, auth.RevealSecretsAllowed(admin, false), "the operator flag is still required")
	assert.False(t, auth.RevealSecretsAllowed(agent, true), "a scoped agent token is not the operator")
	assert.False(t, auth.RevealSecretsAllowed(anon, true), "an unauthenticated back-compat admin proved no identity")
	assert.False(t, auth.RevealSecretsAllowed(t.Context(), true), "no AuthContext means no identity")
	var nilCtx context.Context
	assert.False(t, auth.RevealSecretsAllowed(nilCtx, true), "a nil ctx must fail closed, not panic")
}

func TestCanEnumerateServer_AbsenceIsUnrestricted(t *testing.T) {
	agent := auth.WithAuthContext(t.Context(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}})
	wildcard := auth.WithAuthContext(t.Context(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"*"}})

	assert.True(t, auth.CanEnumerateServer(agent, "alpha"))
	assert.False(t, auth.CanEnumerateServer(agent, "beta"))
	assert.True(t, auth.CanEnumerateServer(wildcard, "beta"))
	assert.True(t, auth.CanEnumerateServer(t.Context(), "beta"),
		"no AuthContext must stay unrestricted — the middleware forwards that way on its no-config path")
	var nilCtx context.Context
	assert.True(t, auth.CanEnumerateServer(nilCtx, "beta"), "a nil ctx must not panic")
	assert.True(t, auth.CanEnumerateServer(auth.WithAuthContext(t.Context(), auth.AdminContext()), "beta"))
}

// TestVisibleServers_NeverCompactsInPlace pins the aliasing rule the SSE path
// depends on: the input slice's backing array is shared with the event bus and
// every other subscriber, so a compaction here would corrupt an admin's view
// concurrently.
func TestVisibleServers_NeverCompactsInPlace(t *testing.T) {
	agent := auth.WithAuthContext(t.Context(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"beta"}})
	input := scopeFixtureServers()

	out := visibleServers(agent, input)
	require.Len(t, out, 1)
	assert.Equal(t, "beta", out[0].Name)
	assert.Equal(t, []string{"alpha", "beta"}, []string{input[0].Name, input[1].Name},
		"the caller's slice must be untouched")

	empty := visibleServers(auth.WithAuthContext(t.Context(), &auth.AuthContext{Type: auth.AuthTypeAgent}), input)
	raw, err := json.Marshal(map[string]interface{}{"servers": empty})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"servers":[]`,
		"an empty subset must marshal as [] and not null — the Swift tray decodes this strictly")
}
