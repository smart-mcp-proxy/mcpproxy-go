package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Issue #1178. Fixing #1166/#1167 took three passes because the /api/v1 GET
// surface was enumerated by hand and each pass missed a door (/servers/{id}/logs
// carrying upstream stdout/stderr, /activity carrying tool-call args). This test
// drops the hand-enumeration. It walks the PRODUCTION chi route table, finds
// every GET under /api/v1, and requires each one to carry a scope classification
// below. A new GET route added without an entry fails the test the moment it is
// registered, so a door cannot ship unclassified.
//
// Each route is one of:
//
//   scopeRefused:  a scoped agent token gets the fixed 403 (a deployment-wide
//     document it may not read). Driven live, it must be 403 AND carry the
//     scope-layer denial body, so an unrelated 403 does not satisfy it.
//   scopeFiltered: reachable, but per-server content is narrowed to the caller's
//     grant downstream. The `why` names the scoped-caller test that proves the
//     narrowing; this file only checks the coarse verdict (never the fixed 403).
//   scopeOpen:     reachable, and carries nothing about a server outside the
//     caller's grant. Allowlisted with a written reason.
//
// Filtered and open both reach the handler for the probe token — a 2xx, never
// the fixed 403. They are kept as separate classes so the `why` on each says
// which it is. A handful reach a documented 404/400/503 BEFORE the gate on the
// synthetic path params the walk uses (no such record, dependency not wired);
// those are enumerated in shortCircuitCodes and their real scoped-caller oracle
// is the named test in `why`, not this walk.

type scopeClass int

const (
	scopeRefused scopeClass = iota
	scopeFiltered
	scopeOpen
)

type routeScope struct {
	class scopeClass
	why   string
}

// getRouteScopes classifies every GET route under /api/v1. Keys are chi route
// patterns exactly as chi.Walk reports them (trailing slashes and all). The test
// asserts this map and the walked surface are the same set in BOTH directions:
// an unclassified new route fails, and a stale entry for a removed route fails.
var getRouteScopes = map[string]routeScope{
	// --- Refused: deployment-wide documents an agent token cannot read ---
	"/api/v1/config":            {scopeRefused, "whole config document; TestGetConfig_AgentTokenForbidden_AdminUnaffected"},
	"/api/v1/code/scripts":      {scopeRefused, "stored-script listing is admin-only (scriptsListingDenialMessage)"},
	"/api/v1/onboarding/state":  {scopeRefused, "inventory size + client ids; TestOnboardingState_DeniedToScopedCaller"},
	"/api/v1/secrets/refs":      {scopeRefused, "credential inventory; TestSecretsInventory_DeniedToScopedCaller"},
	"/api/v1/secrets/config":    {scopeRefused, "credential inventory; TestSecretsInventory_DeniedToScopedCaller"},
	"/api/v1/security/overview": {scopeRefused, "fleet scan/finding counts; TestSecurityFleetDoors_DeniedToScopedCaller"},
	"/api/v1/security/queue":    {scopeRefused, "names every queued server; TestSecurityFleetDoors_DeniedToScopedCaller"},
	"/api/v1/sessions":          {scopeRefused, "MCP client/session history; TestSessions_DeniedToAgentTokens"},
	"/api/v1/sessions/{id}":     {scopeRefused, "MCP client/session history; TestSessions_DeniedToAgentTokens"},
	"/api/v1/stats/tokens":      {scopeRefused, "deployment-wide token stats; TestTokenStats_DeniedToAgentTokens"},
	"/api/v1/telemetry/payload": {scopeRefused, "fleet heartbeat counts; TestTelemetryPayload_DeniedToScopedCaller"},
	"/api/v1/tokens/":           {scopeRefused, "agent tokens cannot manage tokens (requireManageTokens)"},
	"/api/v1/tokens/{name}/":    {scopeRefused, "agent tokens cannot manage tokens (requireManageTokens)"},

	// --- Filtered: reachable, per-server content narrowed to the grant ---
	"/api/v1/servers":                       {scopeFiltered, "visibleServers; TestGetServers_AgentTokenSeesOnlyAllowedSubset_ManagementPath"},
	"/api/v1/tools":                         {scopeFiltered, "scoped to grant; TestGetGlobalTools_ScopedToAllowedServers"},
	"/api/v1/index/search":                  {scopeFiltered, "filtered before the cut; TestIndexSearch_HiddenHighRankerDisplacesEntitledHit"},
	"/api/v1/status":                        {scopeFiltered, "upstream_stats scoped; TestGetStatus_UpstreamStatsScoped"},
	"/api/v1/info":                          {scopeFiltered, "admin api key withheld; TestInfo_ScopedCallerNeverReceivesTheAdminAPIKey"},
	"/api/v1/annotations/coverage":          {scopeFiltered, "one row per server, canSeeServer gate (#1166)"},
	"/api/v1/activity":                      {scopeFiltered, "scopeAllowedServers; TestListActivity_ScopedToAllowedServers"},
	"/api/v1/activity/summary":              {scopeFiltered, "scopeAllowedServers; TestActivitySummary_ScopedToAllowedServers"},
	"/api/v1/activity/usage":                {scopeFiltered, "scopeAllowedServers; TestActivityUsage_ScopedToAllowedServers"},
	"/api/v1/activity/export":               {scopeFiltered, "scopeAllowedServers; TestActivityExport_ScopedToAllowedServers"},
	"/api/v1/activity/{id}":                 {scopeFiltered, "unentitled == absent (404); TestActivityDetail_UnentitledIsIndistinguishableFromAbsent"},
	"/api/v1/tool-calls":                    {scopeFiltered, "scopeAllowedServers; TestToolCalls_ScopedToAllowedServers"},
	"/api/v1/tool-calls/{id}":               {scopeFiltered, "unentitled == absent (404); TestToolCallDetail_UnentitledIsIndistinguishableFromAbsent"},
	"/api/v1/security/scans":                {scopeFiltered, "canSeeServer per row (handleListScanHistory)"},
	"/api/v1/security/scans/{jobId}/report": {scopeFiltered, "canSeeServer on report.ServerName; TestSecurityScanReportByJobID_ScopedTo404"},

	// /servers/{id}/**: the whole subtree is gated by scopedServerSubtree, so an
	// unentitled server reads exactly as an absent one (404), never the fixed 403.
	// TestServerSubtree_UnentitledIsIndistinguishableFromAbsent /
	// TestServerLogs_ScopedTokenNeverSeesAnotherServersStderr.
	"/api/v1/servers/{id}/tools":             {scopeFiltered, "scopedServerSubtree gate"},
	"/api/v1/servers/{id}/logs":              {scopeFiltered, "scopedServerSubtree gate (stdout/stderr, the #1166 leak)"},
	"/api/v1/servers/{id}/diagnostics":       {scopeFiltered, "scopedServerSubtree gate; TestServerDiagnostics_UnentitledIsIndistinguishableFromAbsent"},
	"/api/v1/servers/{id}/tool-calls":        {scopeFiltered, "scopedServerSubtree gate"},
	"/api/v1/servers/{id}/tools/export":      {scopeFiltered, "scopedServerSubtree gate"},
	"/api/v1/servers/{id}/tools/{tool}/diff": {scopeFiltered, "scopedServerSubtree gate"},
	"/api/v1/servers/{id}/scan/status":       {scopeFiltered, "scopedServerSubtree gate"},
	"/api/v1/servers/{id}/scan/report":       {scopeFiltered, "scopedServerSubtree gate"},
	"/api/v1/servers/{id}/scan/files":        {scopeFiltered, "scopedServerSubtree gate"},
	"/api/v1/servers/{id}/integrity":         {scopeFiltered, "scopedServerSubtree gate"},

	// --- Open: no per-server identity a scoped token could learn beyond its grant ---
	"/api/v1/profiles":                      {scopeOpen, "profile names, not server inventory"},
	"/api/v1/profiles/active":               {scopeOpen, "active profile name only"},
	"/api/v1/routing":                       {scopeOpen, "available routing modes, no server data"},
	"/api/v1/docker/status":                 {scopeOpen, "Docker daemon availability, no server data"},
	"/api/v1/registries":                    {scopeOpen, "configured registry sources, not upstream servers"},
	"/api/v1/registries/{id}/servers":       {scopeOpen, "searches a remote registry, not the local inventory"},
	"/api/v1/security/scanners":             {scopeOpen, "scanner plugin inventory, not per-server data"},
	"/api/v1/security/scanners/{id}/status": {scopeOpen, "scanner plugin status, not per-server data"},
	"/api/v1/servers/import/paths":          {scopeOpen, "host MCP-client config file locations, no upstream identity"},
	"/api/v1/connect":                       {scopeOpen, "MCP client connect status; reads stay open (server.go connect block)"},
	"/api/v1/connect/{client}":              {scopeOpen, "MCP client connect status; reads stay open"},
	"/api/v1/connect/{client}/preview":      {scopeOpen, "MCP client config preview; reads stay open"},

	// /diagnostics and its /doctor alias aggregate per-server health, on two
	// paths with two separate gates: the management path delegates to
	// internal/management.Doctor, whose CanEnumerateServer drop is proven by
	// TestDoctor_ScopedTokenSeesOnlyAllowedServers (internal/management); the
	// legacy fallback filters through visibleServers in handleGetDiagnostics
	// itself, proven here by TestDiagnostics_LegacyPathScopedCallerDoesNotSeeHiddenServer.
	"/api/v1/diagnostics": {scopeFiltered, "management Doctor (TestDoctor_ScopedTokenSeesOnlyAllowedServers) / legacy visibleServers (TestDiagnostics_LegacyPathScopedCallerDoesNotSeeHiddenServer) drop unentitled servers (#1166)"},
	"/api/v1/doctor":      {scopeFiltered, "alias of /diagnostics; same scope gate"},
}

// minGetRoutes is the vacuity floor the issue asks for: a chi.Walk that matches
// nothing (or almost nothing, after a router refactor changes the mount prefix)
// must not pass forever. The map/route set is pinned exactly by the two-way
// check below; this is the additional "a non-trivial number were found" guard.
// Lower it deliberately, with a diff, if routes are intentionally removed.
const minGetRoutes = 40

// routeParamSubstitutions fills chi path params. {id} on the /servers/{id}
// subtree is the entitled server, so where a record exists the scope gate lets
// the request through to the handler (a 2xx below). Where no record exists for
// the synthetic value — /servers/{id}/scan/{status,report,files} (no scan for
// alpha), the /{id} detail routes (no such activity/tool-call/job), a scanner id
// that is not a configured plugin — the handler answers 404/400/503 BEFORE the
// scope gate runs. Those are enumerated in shortCircuitCodes so the walk asserts
// the exact pre-gate code instead of a loose "not 403", and their real
// scoped-caller oracle is the named test in each getRouteScopes `why`.
var routeParamSubstitutions = map[string]string{
	"{id}":     "alpha",
	"{name}":   "alpha",
	"{tool}":   "alpha_tool",
	"{client}": "claude-desktop",
	"{jobId}":  "job-1",
}

// shortCircuitCodes are the non-refused routes that, driven with the probe token
// and the SYNTHETIC params above, answer before the scope gate is reached. The
// live walk asserts each returns exactly this code — not 2xx — because a scoped
// token never exercises the gate on these inputs; deleting the gate would leave
// the code here unchanged. The gate for each is proven by the test cited in its
// getRouteScopes `why`. A route that STOPS short-circuiting (e.g. a handler that
// begins serving the synthetic id) trips this and must be re-examined.
var shortCircuitCodes = map[string]int{
	"/api/v1/index/search":                  http.StatusBadRequest,         // no ?q= on the probe request
	"/api/v1/connect":                       http.StatusServiceUnavailable, // connect manager not wired in the fixture
	"/api/v1/connect/{client}":              http.StatusServiceUnavailable,
	"/api/v1/connect/{client}/preview":      http.StatusServiceUnavailable,
	"/api/v1/activity/{id}":                 http.StatusNotFound, // synthetic id absent → 404 before canSeeServer
	"/api/v1/tool-calls/{id}":               http.StatusNotFound, // synthetic id absent → 404 before canSeeServer
	"/api/v1/security/scans/{jobId}/report": http.StatusNotFound, // synthetic jobId absent → 404 before canSeeServer
	"/api/v1/servers/{id}/scan/status":      http.StatusNotFound, // no scan record for alpha in the fixture
	"/api/v1/servers/{id}/scan/report":      http.StatusNotFound, // no scan record for alpha in the fixture
	"/api/v1/servers/{id}/scan/files":       http.StatusNotFound, // no scan record for alpha in the fixture
	"/api/v1/security/scanners/{id}/status": http.StatusNotFound, // "alpha" is not a configured scanner plugin
}

func fillRouteParams(pattern string) string {
	out := pattern
	for placeholder, value := range routeParamSubstitutions {
		out = strings.ReplaceAll(out, placeholder, value)
	}
	return out
}

// scopeDenialMarker is the fixed substring every scope-layer 403 body carries
// (config, activity, sessions, secrets, telemetry, tokens, and so on). Asserting on it
// pins the refusal to the scope layer, so an unrelated 403 cannot satisfy a
// scopeRefused route.
const scopeDenialMarker = "Agent tokens cannot"

// TestScopeRouteTableGuard walks the production /api/v1 GET surface and holds
// every route to its classification for a real, read-only agent token scoped to
// one server.
func TestScopeRouteTableGuard(t *testing.T) {
	sec := &mockSecurityController{
		scanners:      []*scanner.ScannerPlugin{{ID: "ramparts", Name: "Ramparts", Status: scanner.ScannerStatusConfigured}},
		overview:      &scanner.SecurityOverview{},
		queueProgress: &scanner.QueueProgress{},
	}
	srv, token := scopeRound10Server(t, sec)

	seen := map[string]bool{}
	err := chi.Walk(srv.Router(), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != http.MethodGet || !strings.HasPrefix(route, "/api/v1") {
			return nil
		}
		seen[route] = true
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, seen, "chi.Walk found no /api/v1 GET routes — the route table changed shape")
	require.GreaterOrEqualf(t, len(seen), minGetRoutes,
		"walk found only %d GET routes under /api/v1 (< %d) — the walk likely matched a stale prefix; a near-empty walk must not pass",
		len(seen), minGetRoutes)

	// Every walked route must be classified. This is the guard: a new GET route
	// added without an entry above fails here until someone classifies it.
	for route := range seen {
		if _, ok := getRouteScopes[route]; !ok {
			t.Errorf("GET %s is a new /api/v1 route with no scope classification — add it to "+
				"getRouteScopes as scopeRefused / scopeFiltered / scopeOpen, with a written reason "+
				"(and a scoped-caller test if it exposes per-server data)", route)
		}
	}

	// Every classified route must still exist, so a removed route's stale entry
	// (and its now-meaningless reason) does not linger.
	for route := range getRouteScopes {
		require.Truef(t, seen[route], "getRouteScopes lists %s but the walk did not find it — remove the stale entry", route)
	}

	// Drive each route with the scoped token and hold it to its verdict.
	for route := range seen {
		rc := getRouteScopes[route]
		path := fillRouteParams(route)
		t.Run(route, func(t *testing.T) {
			rec := scopeGet(t, srv, path, token)
			if rc.class == scopeRefused {
				require.Equalf(t, http.StatusForbidden, rec.Code,
					"%s is classified scopeRefused (%s) but did not answer 403: got %d body=%s",
					route, rc.why, rec.Code, rec.Body.String())
				require.Containsf(t, rec.Body.String(), scopeDenialMarker,
					"%s answered 403 but not the scope-layer denial body — is this the right refusal?", route)
				return
			}
			// Routes that short-circuit before the gate on synthetic params:
			// hold the exact pre-gate code, so a route that starts reaching the
			// handler (and thus the gate) is caught and re-examined.
			if want, ok := shortCircuitCodes[route]; ok {
				require.Equalf(t, want, rec.Code,
					"%s is a documented pre-gate short-circuit (%s) expected to answer %d, got %d body=%s — "+
						"if it now reaches the handler, drop it from shortCircuitCodes and require 2xx",
					route, rc.why, want, rec.Code, rec.Body.String())
				return
			}
			// Everything else must actually reach the handler: a 2xx, never the
			// fixed 403 and never a 4xx/5xx that died before the gate. "not 403"
			// alone let a 404/503 masquerade as a reached-and-filtered route.
			require.GreaterOrEqualf(t, rec.Code, http.StatusOK,
				"%s is classified %v (%s) — a scoped token must reach the handler (2xx, filtered downstream), got %d body=%s",
				route, rc.class, rc.why, rec.Code, rec.Body.String())
			require.Lessf(t, rec.Code, http.StatusMultipleChoices,
				"%s is classified %v (%s) — a scoped token must reach the handler (2xx, filtered downstream), got %d body=%s. "+
					"If this is a legitimate pre-gate short-circuit, add it to shortCircuitCodes with a reason",
				route, rc.class, rc.why, rec.Code, rec.Body.String())
		})
	}
}

// TestDiagnostics_LegacyPathScopedCallerDoesNotSeeHiddenServer backs the
// scopeFiltered classification of /diagnostics (and /doctor) on the LEGACY path.
//
// withManagement:false selects the fallback branch of handleGetDiagnostics,
// whose own visibleServers() call is the gate under test — deleting that call
// leaks the hidden server here. The management path is a separate gate in
// internal/management.Doctor, proven by TestDoctor_ScopedTokenSeesOnlyAllowedServers;
// driving the httpapi handler with the management mock (as the earlier version of
// this test did) only exercised the mock's own filter loop, so dropping the real
// CanEnumerateServer check left it green. The legacy branch had no scoped-caller
// coverage before this.
func TestDiagnostics_LegacyPathScopedCallerDoesNotSeeHiddenServer(t *testing.T) {
	// Both servers must carry an error so each surfaces in the legacy
	// upstream-errors list when visible: beta already has one; give alpha one in
	// this local copy so "alpha still present" is a real positive control and the
	// beta assertion cannot pass against an empty report. The shared fixture is
	// left untouched.
	servers := scopeFixtureServers()
	for i := range servers {
		if servers[i].LastError == "" {
			servers[i].LastError = "fixture error for " + servers[i].Name
		}
	}
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: servers, withManagement: false}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	admin := scopeGet(t, srv, "/api/v1/diagnostics", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code)
	require.Containsf(t, admin.Body.String(), "beta",
		"positive control: an admin must see the hidden server on the legacy path, or this proves nothing about who was filtered: %s", admin.Body.String())

	scoped := scopeGet(t, srv, "/api/v1/diagnostics", token)
	require.Equal(t, http.StatusOK, scoped.Code, "diagnostics stays readable for a scoped caller, only narrowed")
	body := scoped.Body.String()
	require.Contains(t, body, "alpha", "the caller's own entitled server must still appear")
	require.NotContainsf(t, body, "beta", "visibleServers must drop a server outside the token's grant from the legacy diagnostics path: %s", body)
}
