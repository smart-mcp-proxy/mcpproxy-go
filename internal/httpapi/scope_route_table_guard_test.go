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
// Filtered and open look the same on the wire (not the 403). They are kept as
// separate classes so the `why` on each says which it is.

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

	// /diagnostics and its /doctor alias aggregate per-server health. The
	// management service (internal/management.Doctor) drops servers the caller
	// cannot enumerate before building the report, and the legacy fallback path
	// filters through visibleServers; TestDiagnostics_ScopedCallerDoesNotSeeHiddenServer.
	"/api/v1/diagnostics": {scopeFiltered, "management Doctor / visibleServers drop unentitled servers (#1166)"},
	"/api/v1/doctor":      {scopeFiltered, "alias of /diagnostics; same scope gate"},
}

// minGetRoutes is the vacuity floor the issue asks for: a chi.Walk that matches
// nothing (or almost nothing, after a router refactor changes the mount prefix)
// must not pass forever. The map/route set is pinned exactly by the two-way
// check below; this is the additional "a non-trivial number were found" guard.
// Lower it deliberately, with a diff, if routes are intentionally removed.
const minGetRoutes = 40

// routeParamSubstitutions fills chi path params. {id} on the /servers/{id}
// subtree is the entitled server, so the scope gate lets the request through to
// the handler and the route demonstrates filtering rather than a bare 404. The
// same value on the other {id} routes (activity, registries, tool-calls) is
// harmless, since those are refused or filtered before the value matters.
var routeParamSubstitutions = map[string]string{
	"{id}":     "alpha",
	"{name}":   "alpha",
	"{tool}":   "alpha_tool",
	"{client}": "claude-desktop",
	"{jobId}":  "job-1",
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
			require.NotEqualf(t, http.StatusForbidden, rec.Code,
				"%s is classified %v (%s) — a scoped token must reach it (filtered downstream), not get the fixed 403: body=%s",
				route, rc.class, rc.why, rec.Body.String())
		})
	}
}

// TestDiagnostics_ScopedCallerDoesNotSeeHiddenServer backs the scopeFiltered
// classification of /diagnostics (and /doctor) in getRouteScopes: the door stays
// readable for a scoped caller, but a server outside the token's grant does not
// appear in the diagnosis. The admin control is what makes it an oracle; without
// it the assertion would pass just as happily against an empty report.
func TestDiagnostics_ScopedCallerDoesNotSeeHiddenServer(t *testing.T) {
	srv, token := scopeRound10Server(t, nil)

	admin := scopeGet(t, srv, "/api/v1/diagnostics", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code)
	require.Containsf(t, admin.Body.String(), "beta",
		"positive control: an admin must see the hidden server, or this proves nothing about who was filtered: %s", admin.Body.String())

	scoped := scopeGet(t, srv, "/api/v1/diagnostics", token)
	require.Equal(t, http.StatusOK, scoped.Code, "diagnostics stays readable for a scoped caller, only narrowed")
	body := scoped.Body.String()
	require.Contains(t, body, "alpha", "the caller's own entitled server must still appear")
	require.NotContainsf(t, body, "beta", "a server outside the token's grant must not appear in the diagnosis: %s", body)
}
