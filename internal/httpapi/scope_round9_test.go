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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Round-9 cross-review findings on the #1166/#1167 branch. Every test here
// drives the PRODUCTION chi route table through (*Server).ServeHTTP with a real
// mcp_agt_ token, and every scoped assertion is paired with the admin control
// proving the fixture landed and the route matched.

func scopeR9Server(t *testing.T) (*Server, string, *scopeController) {
	t.Helper()
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	return srv, token, ctrl
}

var scopeR9RequestID = regexp.MustCompile(`"request_id":"[^"]*"`)

func scopeR9Normalize(rec *httptest.ResponseRecorder, name string) string {
	body := strings.ReplaceAll(rec.Body.String(), name, "<name>")
	return scopeR9RequestID.ReplaceAllString(body, `"request_id":"<id>"`)
}

// ---------------------------------------------------------------------------
// G1 — the /servers/{id} read subtree
// ---------------------------------------------------------------------------

// scopeR9ServerSubtreeReads enumerates the sub-resources that had NO scope gate.
// Adding a new read sub-resource under /servers/{id} without covering it here
// should make this table incomplete — keep it in sync.
var scopeR9ServerSubtreeReads = []struct {
	name string
	path string // %s is the server name
}{
	{"tools", "/api/v1/servers/%s/tools"},
	{"logs", "/api/v1/servers/%s/logs"},
	{"tool-calls", "/api/v1/servers/%s/tool-calls"},
	{"scan-status", "/api/v1/servers/%s/scan/status"},
	{"scan-report", "/api/v1/servers/%s/scan/report"},
	{"scan-files", "/api/v1/servers/%s/scan/files"},
	{"integrity", "/api/v1/servers/%s/integrity"},
	{"tools-export", "/api/v1/servers/%s/tools/export"},
	{"tool-diff", "/api/v1/servers/%s/tools/does_not_matter/diff"},
	{"diagnostics", "/api/v1/servers/%s/diagnostics"},
}

// TestServerSubtree_UnentitledIsIndistinguishableFromAbsent is the STATUS-PARITY
// oracle for the whole subtree.
//
// "The body must not contain the server name" would pass vacuously on several of
// these routes — they echo the caller's own path parameter back into the
// response. So the assertion is: the unentitled response is byte-identical to
// the absent one once the echoed name and the per-request id are normalised,
// AND the absent response is genuinely distinguishable from a successful read.
//
// Two positive controls stop the whole thing passing for the wrong reason: the
// scoped token reaching the same route for its OWN server, and an admin reading
// beta on the same router.
func TestServerSubtree_UnentitledIsIndistinguishableFromAbsent(t *testing.T) {
	for _, rt := range scopeR9ServerSubtreeReads {
		t.Run(rt.name, func(t *testing.T) {
			srv, token, _ := scopeR9Server(t)
			pathFor := func(name string) string { return strings.Replace(rt.path, "%s", name, 1) }

			// Control 1: the token genuinely reaches this route for alpha.
			allowed := scopeGet(t, srv, pathFor("alpha"), token)
			require.NotEqual(t, http.StatusNotFound, allowed.Code,
				"precondition: the scoped token must reach %s for its OWN server; body: %s", rt.path, allowed.Body.String())

			// Control 2: beta exists and an admin can read it here.
			adminBeta := scopeGet(t, srv, pathFor("beta"), scopeAdminAPIKey)
			require.NotEqual(t, http.StatusNotFound, adminBeta.Code,
				"precondition: beta must exist and be visible to an admin; body: %s", adminBeta.Body.String())

			unentitled := scopeGet(t, srv, pathFor("beta"), token)
			absent := scopeGet(t, srv, pathFor("does-not-exist-at-all"), token)

			assert.Equal(t, http.StatusNotFound, absent.Code,
				"precondition: an absent server must 404, or the parity below is vacuous")
			assert.Equal(t, absent.Code, unentitled.Code,
				"an unentitled server must return the same status as one that does not exist")
			assert.Equal(t, scopeR9Normalize(absent, "does-not-exist-at-all"), scopeR9Normalize(unentitled, "beta"),
				"the two responses must be byte-identical once the echoed name is normalised")
		})
	}
}

// TestServerSubtree_AbsentServerInScopeStill404s pins the EXISTENCE half of the
// gate, which the parity test above cannot reach.
//
// Scope alone already makes an out-of-scope name and an unknown name identical,
// because both fall outside AllowedServers and take the same 404. The case it
// does NOT cover is a name the token IS entitled to that does not exist — the
// handlers behind this subtree answer 200 for that, so the response shape
// (entitled+absent = 200, not entitled = 404) becomes a second, independent
// readout of the boundary that no longer agrees with the first. This asserts
// one answer for both.
func TestServerSubtree_AbsentServerInScopeStill404s(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	// The token names a server the inventory does not contain.
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha", "ghost-server"})

	allowed := scopeGet(t, srv, "/api/v1/servers/alpha/tools", token)
	require.Equal(t, http.StatusOK, allowed.Code,
		"precondition: an entitled, EXISTING server must still read; body: %s", allowed.Body.String())

	ghost := scopeGet(t, srv, "/api/v1/servers/ghost-server/tools", token)
	unentitled := scopeGet(t, srv, "/api/v1/servers/beta/tools", token)

	assert.Equal(t, http.StatusNotFound, ghost.Code,
		"an entitled name that does not exist must 404, not fall through to the handler's 200-on-absent; body: %s", ghost.Body.String())
	assert.Equal(t, unentitled.Code, ghost.Code,
		"absent and unentitled must be one answer")
	assert.Equal(t, scopeR9Normalize(unentitled, "beta"), scopeR9Normalize(ghost, "ghost-server"))
}

// TestServerLogs_ScopedTokenNeverSeesAnotherServersStderr is the sharpest
// single case: upstream stderr routinely echoes the argv and env the process was
// launched with, and nothing redacts a log tail — so this walks around the
// #1158 log-redaction work entirely.
//
// The oracle is NOT "the body omits the secret" alone: that would pass on a 500
// or an empty body. The admin control asserts the same route DOES serve the
// secret, so the scoped 404 is proved to be a denial and not a broken fixture.
func TestServerLogs_ScopedTokenNeverSeesAnotherServersStderr(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	adminBeta := scopeGet(t, srv, "/api/v1/servers/beta/logs", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, adminBeta.Code, "body: %s", adminBeta.Body.String())
	require.Contains(t, adminBeta.Body.String(), betaLogSecret,
		"precondition: this route really does serve the launch secret to an admin")

	allowed := scopeGet(t, srv, "/api/v1/servers/alpha/logs", token)
	require.Equal(t, http.StatusOK, allowed.Code,
		"precondition: the scoped token must still read its OWN server's logs; body: %s", allowed.Body.String())

	denied := scopeGet(t, srv, "/api/v1/servers/beta/logs", token)
	assert.Equal(t, http.StatusNotFound, denied.Code, "body: %s", denied.Body.String())
	assert.NotContains(t, denied.Body.String(), betaLogSecret)
}

// TestServerSubtree_AdminUnaffected is a REGRESSION GUARD, not a bite test: a
// gate that 404s the Web UI is a worse bug than the one being closed. It covers
// the admin key, the tray socket, and the no-AuthContext bootstrap passthrough
// — including the 200-on-absent quirk the gate must NOT extend to admins.
func TestServerSubtree_AdminUnaffected(t *testing.T) {
	newCtrl := func() *scopeController {
		return &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	}

	t.Run("admin reads every server's tools", func(t *testing.T) {
		srv, _ := scopedAgentServer(t, newCtrl(), []string{"alpha"})
		for _, name := range []string{"alpha", "beta"} {
			rec := scopeGet(t, srv, "/api/v1/servers/"+name+"/tools", scopeAdminAPIKey)
			assert.Equal(t, http.StatusOK, rec.Code, "%s: %s", name, rec.Body.String())
		}
	})

	t.Run("no auth context passthrough is unrestricted", func(t *testing.T) {
		srv := NewServer(passthroughController{newCtrl()}, zap.NewNop().Sugar(), nil)
		rec := scopeGet(t, srv, "/api/v1/servers/beta/logs", "")
		assert.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
		assert.Contains(t, rec.Body.String(), betaLogSecret,
			"absence of a token must stay as permissive as it is today")
	})

	t.Run("admin still gets the pre-existing 200 for an absent server", func(t *testing.T) {
		srv, _ := scopedAgentServer(t, newCtrl(), []string{"alpha"})
		rec := scopeGet(t, srv, "/api/v1/servers/does-not-exist-at-all/tools", scopeAdminAPIKey)
		assert.Equal(t, http.StatusOK, rec.Code,
			"the gate must not change admin behaviour on this subtree; body: %s", rec.Body.String())
	})
}

// ---------------------------------------------------------------------------
// G2 — the activity / tool-call / session doors
// ---------------------------------------------------------------------------

func scopeR9ActivityIDs(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	data := scopeDecodeData(t, rec)
	raw, ok := data["activities"].([]interface{})
	require.True(t, ok, "no activities array in %#v", data)
	ids := make([]string, 0, len(raw))
	for _, entry := range raw {
		m, _ := entry.(map[string]interface{})
		id, _ := m["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// TestListActivity_ScopedToAllowedServers. The activity log is strictly wider
// than the enumeration the branch already closed: it carries the ARGUMENTS and
// RESPONSES of every call on every server.
//
// `total` is asserted alongside the array because a post-filter would shrink
// the page while the total kept counting the hidden records — which is a count
// oracle for exactly what was hidden, and a broken pager besides.
func TestListActivity_ScopedToAllowedServers(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	admin := scopeGet(t, srv, "/api/v1/activity", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code, "body: %s", admin.Body.String())
	require.ElementsMatch(t, []string{alphaActivityID, betaActivityID, systemActivityID}, scopeR9ActivityIDs(t, admin),
		"precondition: an admin must see the whole fixture")
	require.Contains(t, admin.Body.String(), betaActivityArg,
		"precondition: this door really does serve another server's arguments")

	agent := scopeGet(t, srv, "/api/v1/activity", token)
	require.Equal(t, http.StatusOK, agent.Code, "body: %s", agent.Body.String())
	assert.Equal(t, []string{alphaActivityID}, scopeR9ActivityIDs(t, agent),
		"a token scoped to alpha must see only alpha's records")
	assert.NotContains(t, agent.Body.String(), betaActivityArg)

	agentData := scopeDecodeData(t, agent)
	assert.Equal(t, float64(1), agentData["total"],
		"total must narrow with the page or it is an exact count oracle for what was hidden")
	adminData := scopeDecodeData(t, admin)
	assert.Equal(t, float64(3), adminData["total"])
}

// TestListActivity_ServerParamCannotWidenScope: ?server= is a user filter that
// narrows WITHIN the entitlement. Naming a server outside it must return
// nothing, not that server's records.
func TestListActivity_ServerParamCannotWidenScope(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	rec := scopeGet(t, srv, "/api/v1/activity?server=beta", token)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, scopeR9ActivityIDs(t, rec))
	assert.Equal(t, float64(0), scopeDecodeData(t, rec)["total"])

	admin := scopeGet(t, srv, "/api/v1/activity?server=beta", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code)
	assert.Equal(t, []string{betaActivityID}, scopeR9ActivityIDs(t, admin),
		"precondition: the same query really does return beta's record for an admin")
}

// TestActivityDetail_UnentitledIsIndistinguishableFromAbsent. This route's 404
// body echoes nothing of the request, so status parity plus the admin positive
// control is the whole oracle.
func TestActivityDetail_UnentitledIsIndistinguishableFromAbsent(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	adminBeta := scopeGet(t, srv, "/api/v1/activity/"+betaActivityID, scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, adminBeta.Code,
		"precondition: the record exists and an admin can read it; body: %s", adminBeta.Body.String())

	allowed := scopeGet(t, srv, "/api/v1/activity/"+alphaActivityID, token)
	require.Equal(t, http.StatusOK, allowed.Code,
		"precondition: the scoped token reaches this route for its OWN record; body: %s", allowed.Body.String())

	unentitled := scopeGet(t, srv, "/api/v1/activity/"+betaActivityID, token)
	absent := scopeGet(t, srv, "/api/v1/activity/01NOSUCHRECORD000000000000", token)

	assert.Equal(t, http.StatusNotFound, absent.Code)
	assert.Equal(t, absent.Code, unentitled.Code)
	assert.Equal(t, scopeR9Normalize(absent, "01NOSUCHRECORD000000000000"), scopeR9Normalize(unentitled, betaActivityID))
	assert.NotContains(t, unentitled.Body.String(), betaActivityArg)
}

// TestActivitySummary_ScopedToAllowedServers covers the counting door, which
// streams the whole window rather than a page.
func TestActivitySummary_ScopedToAllowedServers(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	topServers := func(rec *httptest.ResponseRecorder) []string {
		data := scopeDecodeData(t, rec)
		raw, _ := data["top_servers"].([]interface{})
		names := make([]string, 0, len(raw))
		for _, entry := range raw {
			m, _ := entry.(map[string]interface{})
			name, _ := m["name"].(string)
			names = append(names, name)
		}
		return names
	}

	admin := scopeGet(t, srv, "/api/v1/activity/summary?period=30d", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code, "body: %s", admin.Body.String())
	require.ElementsMatch(t, []string{"alpha", "beta"}, topServers(admin),
		"precondition: the summary really does name every server for an admin")
	require.Equal(t, float64(3), scopeDecodeData(t, admin)["total_count"])

	agent := scopeGet(t, srv, "/api/v1/activity/summary?period=30d", token)
	require.Equal(t, http.StatusOK, agent.Code, "body: %s", agent.Body.String())
	assert.Equal(t, []string{"alpha"}, topServers(agent),
		"a scoped caller must not learn beta exists from the summary, and must still see its own")
	assert.Equal(t, float64(1), scopeDecodeData(t, agent)["total_count"],
		"the counters must be computed over the entitled records only")
}

// TestActivityExport_ScopedToAllowedServers. Export is the widest projection of
// all — it can include full request/response bodies.
func TestActivityExport_ScopedToAllowedServers(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	admin := scopeGet(t, srv, "/api/v1/activity/export?include_bodies=true", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code)
	require.Contains(t, admin.Body.String(), betaActivityArg,
		"precondition: export really does carry another server's arguments for an admin")

	agent := scopeGet(t, srv, "/api/v1/activity/export?include_bodies=true", token)
	require.Equal(t, http.StatusOK, agent.Code)
	assert.NotContains(t, agent.Body.String(), betaActivityArg)
	assert.Contains(t, agent.Body.String(), alphaActivityArg,
		"positive control: its own records must still export")
}

// TestActivityUsage_ScopedToAllowedServers covers the per-tool rollup, and the
// fleet-wide aggregates that are dropped rather than reported wrongly.
func TestActivityUsage_ScopedToAllowedServers(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	toolServers := func(rec *httptest.ResponseRecorder) []string {
		data := scopeDecodeData(t, rec)
		raw, ok := data["tools"].([]interface{})
		require.True(t, ok, "no tools array in %#v", data)
		names := make([]string, 0, len(raw))
		for _, entry := range raw {
			m, _ := entry.(map[string]interface{})
			name, _ := m["server"].(string)
			names = append(names, name)
		}
		return names
	}

	admin := scopeGet(t, srv, "/api/v1/activity/usage?window=all", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code, "body: %s", admin.Body.String())
	require.ElementsMatch(t, []string{"alpha", "beta"}, toolServers(admin),
		"precondition: the usage rollup really does name every server for an admin")
	assert.Equal(t, float64(900), scopeDecodeData(t, admin)["tokens_saved"],
		"precondition: the fleet-wide headline is served to an admin")

	agent := scopeGet(t, srv, "/api/v1/activity/usage?window=all", token)
	require.Equal(t, http.StatusOK, agent.Code, "body: %s", agent.Body.String())
	assert.Equal(t, []string{"alpha"}, toolServers(agent))

	agentData := scopeDecodeData(t, agent)
	assert.Equal(t, float64(0), agentData["tokens_saved"],
		"a fleet-wide aggregate that cannot be re-derived per server is dropped, not reported")
	assert.Empty(t, agentData["timeline"],
		"the global timeline aggregates every server's traffic and has no per-server breakdown")
}

// TestActivityUsage_CacheKeyIncludesCallerScope is the cache-poisoning oracle.
// handleActivityUsage caches by params alone; without the scope term the FIRST
// caller of a given query seeds the entry every other caller then reads for the
// whole TTL. The agent asks first here, so an unscoped key would serve the
// admin the agent's narrowed view.
func TestActivityUsage_CacheKeyIncludesCallerScope(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	agent := scopeGet(t, srv, "/api/v1/activity/usage?window=all", token)
	require.Equal(t, http.StatusOK, agent.Code, "body: %s", agent.Body.String())

	admin := scopeGet(t, srv, "/api/v1/activity/usage?window=all", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code, "body: %s", admin.Body.String())

	data := scopeDecodeData(t, admin)
	raw, ok := data["tools"].([]interface{})
	require.True(t, ok, "no tools array in %#v", data)
	assert.Len(t, raw, 2,
		"the admin must not be served the agent's cached, narrowed view")
	assert.Equal(t, float64(900), data["tokens_saved"],
		"nor the agent's dropped fleet aggregate")
}

func scopeR9ToolCallIDs(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	data := scopeDecodeData(t, rec)
	raw, ok := data["tool_calls"].([]interface{})
	require.True(t, ok, "no tool_calls array in %#v", data)
	ids := make([]string, 0, len(raw))
	for _, entry := range raw {
		m, _ := entry.(map[string]interface{})
		id, _ := m["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// TestToolCalls_ScopedToAllowedServers pins the list door AND the coherence of
// its `total` with the page.
func TestToolCalls_ScopedToAllowedServers(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	admin := scopeGet(t, srv, "/api/v1/tool-calls", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code, "body: %s", admin.Body.String())
	require.ElementsMatch(t, []string{alphaToolCallID, betaToolCallID}, scopeR9ToolCallIDs(t, admin),
		"precondition: an admin sees the whole fixture")
	require.Contains(t, admin.Body.String(), betaToolCallArg,
		"precondition: this door really does serve another server's arguments")

	agent := scopeGet(t, srv, "/api/v1/tool-calls", token)
	require.Equal(t, http.StatusOK, agent.Code, "body: %s", agent.Body.String())
	assert.Equal(t, []string{alphaToolCallID}, scopeR9ToolCallIDs(t, agent))
	assert.NotContains(t, agent.Body.String(), betaToolCallArg)
	assert.Equal(t, float64(1), scopeDecodeData(t, agent)["total"],
		"total must narrow with the page")
	assert.Equal(t, float64(2), scopeDecodeData(t, admin)["total"])
}

// TestToolCallDetail_UnentitledIsIndistinguishableFromAbsent, plus the replay
// twin: replay dispatches a REAL call against the recorded server, so it must
// answer the same question with the same status.
func TestToolCallDetail_UnentitledIsIndistinguishableFromAbsent(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	adminBeta := scopeGet(t, srv, "/api/v1/tool-calls/"+betaToolCallID, scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, adminBeta.Code,
		"precondition: the record exists and an admin can read it; body: %s", adminBeta.Body.String())

	allowed := scopeGet(t, srv, "/api/v1/tool-calls/"+alphaToolCallID, token)
	require.Equal(t, http.StatusOK, allowed.Code,
		"precondition: the scoped token reaches this route for its OWN record; body: %s", allowed.Body.String())

	unentitled := scopeGet(t, srv, "/api/v1/tool-calls/"+betaToolCallID, token)
	absent := scopeGet(t, srv, "/api/v1/tool-calls/no-such-call", token)
	assert.Equal(t, http.StatusNotFound, absent.Code)
	assert.Equal(t, absent.Code, unentitled.Code)
	assert.Equal(t, scopeR9Normalize(absent, "no-such-call"), scopeR9Normalize(unentitled, betaToolCallID))
	assert.NotContains(t, unentitled.Body.String(), betaToolCallArg)
}

func TestReplayToolCall_UnentitledIsIndistinguishableFromAbsent(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	post := func(id, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tool-calls/"+id+"/replay",
			strings.NewReader(`{"arguments":{}}`))
		req.Header.Set("X-API-Key", key)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}

	allowed := post(alphaToolCallID, token)
	require.NotEqual(t, http.StatusNotFound, allowed.Code,
		"precondition: the scoped token reaches replay for its OWN record; body: %s", allowed.Body.String())

	unentitled := post(betaToolCallID, token)
	absent := post("no-such-call", token)
	assert.Equal(t, http.StatusNotFound, unentitled.Code,
		"replay must not dispatch against a server the caller may not see; body: %s", unentitled.Body.String())
	assert.Equal(t, absent.Code, unentitled.Code)
}

// TestSessions_DeniedToAgentTokens. A contracts.MCPSession has no server
// attribution to project — it names the CLIENT and the user's workspace — so
// the door is denied outright, like /api/v1/config.
func TestSessions_DeniedToAgentTokens(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	for _, path := range []string{"/api/v1/sessions", "/api/v1/sessions/session-1"} {
		t.Run(path, func(t *testing.T) {
			admin := scopeGet(t, srv, path, scopeAdminAPIKey)
			require.Equal(t, http.StatusOK, admin.Code, "body: %s", admin.Body.String())
			require.Contains(t, admin.Body.String(), "secret-project",
				"precondition: this door really does serve the workspace name to an admin")

			agent := scopeGet(t, srv, path, token)
			assert.Equal(t, http.StatusForbidden, agent.Code, "body: %s", agent.Body.String())
			assert.NotContains(t, agent.Body.String(), "secret-project")
		})
	}
}

// ---------------------------------------------------------------------------
// G3 — GET /api/v1/stats/tokens
// ---------------------------------------------------------------------------

// TestTokenStats_DeniedToAgentTokens. PerServerToolListSizes is keyed by every
// configured server name: a complete inventory enumeration on a route the
// branch left untouched. Denied rather than projected, because the scalars
// beside the map are fleet-wide and cannot be re-derived per server — see the
// comment on handleGetTokenStats.
func TestTokenStats_DeniedToAgentTokens(t *testing.T) {
	srv, token, _ := scopeR9Server(t)

	admin := scopeGet(t, srv, "/api/v1/stats/tokens", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code, "body: %s", admin.Body.String())
	require.Contains(t, admin.Body.String(), `"beta"`,
		"precondition: the per-server map really does enumerate every server for an admin")

	agent := scopeGet(t, srv, "/api/v1/stats/tokens", token)
	assert.Equal(t, http.StatusForbidden, agent.Code, "body: %s", agent.Body.String())
	assert.NotContains(t, agent.Body.String(), `"beta"`,
		"the inventory must not reach a scoped token")
}

// ---------------------------------------------------------------------------
// G4 — quarantined_servers must be recomputable for a scoped caller
// ---------------------------------------------------------------------------

// TestFilterUpstreamStatsServers_RecomputesQuarantined pins the filter against
// the exact entry shape BOTH producers now emit. Before this, neither wrote a
// per-entry `quarantined` key, so the recomputed scalar was always 0 and a
// scoped caller's security surface read clean while its own server sat
// quarantined.
func TestFilterUpstreamStatsServers_RecomputesQuarantined(t *testing.T) {
	ctx := auth.WithAuthContext(t.Context(),
		&auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}})

	stats := map[string]interface{}{
		"servers": map[string]interface{}{
			"alpha": map[string]interface{}{"connected": true, "quarantined": true, "tool_count": 0},
			"beta":  map[string]interface{}{"connected": true, "quarantined": true, "tool_count": 7},
		},
		"total_servers":       2,
		"connected_servers":   2,
		"quarantined_servers": 2,
		"total_tools":         7,
	}

	out := filterUpstreamStatsServers(ctx, stats)
	assert.Equal(t, 1, out["total_servers"])
	assert.Equal(t, 1, out["quarantined_servers"],
		"the scoped caller's OWN quarantined server must still be counted")
}

// TestConvertUpstreamStatsToServerStats_CountsQuarantined pins the same key on
// the typed converter, which had the identical latent hole.
func TestConvertUpstreamStatsToServerStats_CountsQuarantined(t *testing.T) {
	stats := contracts.ConvertUpstreamStatsToServerStats(map[string]interface{}{
		"servers": map[string]interface{}{
			"alpha": map[string]interface{}{"connected": true, "quarantined": true, "tool_count": 0},
			"beta":  map[string]interface{}{"connected": true, "quarantined": false, "tool_count": 7},
		},
	})
	assert.Equal(t, 1, stats.QuarantinedServers)
}

// ---------------------------------------------------------------------------
// G6 — one mux must not carry two definitions of "not admin"
// ---------------------------------------------------------------------------

// TestRequireAdminRead_DeniesNonAdminUserContext. apiKeyAuthMiddleware installs
// only admin and agent contexts today, so this drives the handler directly with
// the server-edition AuthTypeUser identity that would otherwise have passed an
// AuthTypeAgent-only test and read the whole config document.
//
// The oracle is status parity with the agent identity on the same handler, plus
// the admin positive control proving the handler serves the document at all.
func TestRequireAdminRead_DeniesNonAdminUserContext(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(true), servers: scopeFixtureServers(), withManagement: true}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	call := func(ac *auth.AuthContext) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/config", http.NoBody)
		if ac != nil {
			req = req.WithContext(auth.WithAuthContext(req.Context(), ac))
		}
		rec := httptest.NewRecorder()
		srv.handleGetConfig(rec, req)
		return rec
	}

	adminRec := call(auth.AdminContext())
	require.Equal(t, http.StatusOK, adminRec.Code,
		"precondition: this handler serves the document to an admin; body: %s", adminRec.Body.String())
	require.Contains(t, adminRec.Body.String(), alphaHeaderSecret,
		"precondition: the document really does carry credentials")

	agentRec := call(&auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}})
	require.Equal(t, http.StatusForbidden, agentRec.Code, "body: %s", agentRec.Body.String())

	userRec := call(auth.UserContext("u1", "u@example.com", "U", "google"))
	assert.Equal(t, agentRec.Code, userRec.Code,
		"an OAuth user is not an admin and must be denied exactly as an agent token is")
	assert.NotContains(t, userRec.Body.String(), alphaHeaderSecret)
	assert.NotContains(t, userRec.Body.String(), scopeAdminAPIKey)

	adminUserRec := call(auth.AdminUserContext("u2", "a@example.com", "A", "google"))
	assert.Equal(t, http.StatusOK, adminUserRec.Code,
		"an OAuth ADMIN user must keep admin access; body: %s", adminUserRec.Body.String())
}

// ---------------------------------------------------------------------------
// G7 — status.message leaks the true server count
// ---------------------------------------------------------------------------

// TestGetStatus_MessageDoesNotLeakServerCount. total_servers on this very route
// was recomputed precisely so it would stop being a count oracle; the sibling
// `message` carried "Connected to 1/2 servers" verbatim.
func TestGetStatus_MessageDoesNotLeakServerCount(t *testing.T) {
	base := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, scopeStatusController{base}, []string{"alpha"})

	statusMessage := func(rec *httptest.ResponseRecorder) string {
		data := scopeDecodeData(t, rec)
		status, ok := data["status"].(map[string]interface{})
		require.True(t, ok, "no status object in %#v", data)
		msg, _ := status["message"].(string)
		return msg
	}

	admin := scopeGet(t, srv, "/api/v1/status", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code, "body: %s", admin.Body.String())
	require.Equal(t, scopeStatusMessage, statusMessage(admin),
		"precondition: the message really is served, with its counts, to an admin")

	agent := scopeGet(t, srv, "/api/v1/status", token)
	require.Equal(t, http.StatusOK, agent.Code, "body: %s", agent.Body.String())
	assert.Empty(t, statusMessage(agent),
		"a scoped caller must not be handed the true server count in prose")
	assert.NotContains(t, agent.Body.String(), "1/2 servers")

	// The neighbouring, count-free field must survive — blanking the whole
	// snapshot would be a different bug.
	agentStatus, _ := scopeDecodeData(t, agent)["status"].(map[string]interface{})
	assert.Equal(t, "Ready", agentStatus["phase"])
}

// ---------------------------------------------------------------------------
// The predicates themselves
// ---------------------------------------------------------------------------

func TestActivityFilterAllowedServers(t *testing.T) {
	rec := &storage.ActivityRecord{ServerName: "beta"}
	unattributed := &storage.ActivityRecord{}

	assert.True(t, (&storage.ActivityFilter{}).Matches(rec), "nil means unrestricted")
	assert.True(t, (&storage.ActivityFilter{AllowedServers: []string{"beta"}}).Matches(rec))
	assert.True(t, (&storage.ActivityFilter{AllowedServers: []string{"*"}}).Matches(rec))
	assert.False(t, (&storage.ActivityFilter{AllowedServers: []string{"alpha"}}).Matches(rec))
	assert.False(t, (&storage.ActivityFilter{AllowedServers: []string{}}).Matches(rec),
		"empty-but-non-nil must match NOTHING — it is the shape a token allowed no servers produces")
	assert.False(t, (&storage.ActivityFilter{AllowedServers: []string{"*"}}).Matches(unattributed),
		"an operator-plane record has no server to be entitled to")
}

func TestToolCallScopeAllows(t *testing.T) {
	var unrestricted storage.ToolCallScope
	assert.True(t, unrestricted.Allows("beta"))
	assert.True(t, storage.ToolCallScope{"beta"}.Allows("beta"))
	assert.True(t, storage.ToolCallScope{"*"}.Allows("beta"))
	assert.False(t, storage.ToolCallScope{"alpha"}.Allows("beta"))
	assert.False(t, storage.ToolCallScope{}.Allows("beta"))
	assert.False(t, storage.ToolCallScope{"*"}.Allows(""))
}

func TestScopeAllowedServers(t *testing.T) {
	allowed, scoped := scopeAllowedServers(t.Context())
	assert.False(t, scoped, "no AuthContext is unrestricted, not scoped-to-nothing")
	assert.Nil(t, allowed)

	allowed, scoped = scopeAllowedServers(auth.WithAuthContext(t.Context(), auth.AdminContext()))
	assert.False(t, scoped)
	assert.Nil(t, allowed)

	allowed, scoped = scopeAllowedServers(auth.WithAuthContext(t.Context(),
		&auth.AuthContext{Type: auth.AuthTypeAgent}))
	assert.True(t, scoped)
	assert.NotNil(t, allowed, "a token allowed nothing must produce an EMPTY, non-nil set")
	assert.Empty(t, allowed)

	var nilCtx context.Context
	allowed, scoped = scopeAllowedServers(nilCtx)
	assert.False(t, scoped, "a nil ctx must not panic")
	assert.Nil(t, allowed)
}

// TestUsageCacheKey_SeparatesScopes is the unit twin of the cache test above.
func TestUsageCacheKey_SeparatesScopes(t *testing.T) {
	adminKey := usageParams{window: "all", top: 20, sort: "calls"}.cacheKey()
	alpha := usageParams{window: "all", top: 20, sort: "calls", scoped: true, allowed: []string{"alpha"}}.cacheKey()
	beta := usageParams{window: "all", top: 20, sort: "calls", scoped: true, allowed: []string{"beta"}}.cacheKey()

	assert.NotEqual(t, adminKey, alpha)
	assert.NotEqual(t, alpha, beta)
	assert.Equal(t,
		usageParams{window: "all", top: 20, sort: "calls", scoped: true, allowed: []string{"alpha", "beta"}}.cacheKey(),
		usageParams{window: "all", top: 20, sort: "calls", scoped: true, allowed: []string{"beta", "alpha"}}.cacheKey(),
		"the same entitlement in a different order is the same cache identity")
}

// scopeR9DecodeEnvelope is used where the body is not the standard data envelope.
func scopeR9DecodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body: %s", rec.Body.String())
	return out
}

var _ = scopeR9DecodeEnvelope
