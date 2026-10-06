package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 109-k (activity-scope-filters, FR-080a): TestRejectUnsupportedScopeFilters_*
// in scope_filters_gate_test.go exercises rejectUnsupportedScopeFilters as a
// pure function — real, but it proves nothing about whether any of the 8
// call sites (activity.go x4, server.go x3, tokens.go x1) still actually
// calls it. A refactor that moved or deleted a call (the exact version-skew
// FR-080a exists to prevent) would leave every existing test green.
//
// This file drives the PRODUCTION chi route table via (*Server).ServeHTTP —
// the same harness scope_round9_test.go uses — so a regression here means the
// real HTTP surface, not just the helper, stopped gating.
func TestRejectUnsupportedScopeFilters_RealRoutes(t *testing.T) {
	prev := scopeFilterSupportedFilters
	scopeFilterSupportedFilters = nil
	t.Cleanup(func() { scopeFilterSupportedFilters = prev })

	srv, _, _ := scopeR9Server(t)

	gatedRoutes := []string{
		"/api/v1/activity",
		"/api/v1/activity/export",
		"/api/v1/activity/summary",
		"/api/v1/activity/usage",
		"/api/v1/servers",
		"/api/v1/tools",
		"/api/v1/sessions",
		"/api/v1/tokens",
		"/api/v1/clients",
	}

	for _, path := range gatedRoutes {
		t.Run(path, func(t *testing.T) {
			if path == "/api/v1/clients" && !clientRoutesSupported {
				t.Skip("the server edition has no per-client surface")
			}
			rec := scopeGet(t, srv, path+"?profile=work", scopeAdminAPIKey)
			require.Equal(t, http.StatusBadRequest, rec.Code,
				"expected the gate to reject ?profile= before any handler logic; body: %s", rec.Body.String())
			assert.Contains(t, rec.Body.String(), "unsupported_scope_filter")
			assert.Contains(t, rec.Body.String(), "profile")
		})
	}
}

// TestRejectUnsupportedScopeFilters_RealRoutes_ActivityAgentExemption pins the
// one documented exception (GET /activity and /activity/export honour `agent`
// today) against the real routes, alongside the two routes that must NOT
// exempt it (summary, usage — gated exactly like `token`, codex round 4).
func TestRejectUnsupportedScopeFilters_RealRoutes_ActivityAgentExemption(t *testing.T) {
	prev := scopeFilterSupportedFilters
	scopeFilterSupportedFilters = nil
	t.Cleanup(func() { scopeFilterSupportedFilters = prev })

	srv, _, _ := scopeR9Server(t)

	for _, path := range []string{"/api/v1/activity", "/api/v1/activity/export"} {
		t.Run(path+"_agent_exempt", func(t *testing.T) {
			rec := scopeGet(t, srv, path+"?agent=alice", scopeAdminAPIKey)
			assert.NotEqual(t, http.StatusBadRequest, rec.Code,
				"%s must honour ?agent= unchanged; body: %s", path, rec.Body.String())
		})
	}

	for _, path := range []string{"/api/v1/activity/summary", "/api/v1/activity/usage"} {
		t.Run(path+"_agent_gated", func(t *testing.T) {
			rec := scopeGet(t, srv, path+"?agent=alice", scopeAdminAPIKey)
			require.Equal(t, http.StatusBadRequest, rec.Code,
				"%s must gate ?agent= like ?token=; body: %s", path, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "unsupported_scope_filter")
		})
	}
}

// Spec 108-e (T058/T064): with the supported list filled, every FR-031 route
// answers the names it honours, and the routes that do not honour a name yet
// keep answering 400 unsupported_scope_filter.
func TestRealRoutes_ListFilled_HonouredNamesPass(t *testing.T) {
	srv, _, _ := scopeR9Server(t)

	honoured := map[string][]string{
		"/api/v1/activity":         {"profile", "client", "token", "agent"},
		"/api/v1/activity/export":  {"profile", "client", "token", "agent"},
		"/api/v1/activity/summary": {"profile", "client", "token", "agent"},
		"/api/v1/activity/usage":   {"profile", "client", "token", "agent"},
		"/api/v1/sessions":         {"profile", "client", "token", "agent"},
		"/api/v1/servers":          {"profile"},
		// Spec 108-f: the Clients and Tokens lists honour their own filters.
		"/api/v1/tokens":  {"profile", "token"},
		"/api/v1/clients": {"profile", "client"},
	}
	for path, names := range honoured {
		for _, name := range names {
			t.Run(path+"?"+name, func(t *testing.T) {
				rec := scopeGet(t, srv, path+"?"+name+"=-", scopeAdminAPIKey)
				assert.NotContains(t, rec.Body.String(), "unsupported_scope_filter",
					"%s must honour ?%s=; status %d body %s", path, name, rec.Code, rec.Body.String())
			})
		}
	}
	// /tools honours profile and client (the "-" sentinel is a 400 of its own
	// there, so a named value is used).
	for _, name := range []string{"profile", "client"} {
		rec := scopeGet(t, srv, "/api/v1/tools?"+name+"=x", scopeAdminAPIKey)
		assert.NotContains(t, rec.Body.String(), "unsupported_scope_filter", name)
	}
}

func TestRealRoutes_ListFilled_UnhonouredStillRejected(t *testing.T) {
	srv, _, _ := scopeR9Server(t)

	for _, tc := range []struct{ path, param string }{
		{"/api/v1/tools?token=ci-bot", "token"},
		{"/api/v1/tools?agent=ci-bot", "agent"},
		{"/api/v1/servers?client=cursor", "client"},
		{"/api/v1/servers?token=ci-bot", "token"},
		{"/api/v1/tokens?client=x", "client"},
		{"/api/v1/clients?token=x", "token"},
		{"/api/v1/clients/cursor?profile=x", "profile"},
		{"/api/v1/clients/cursor?client=x", "client"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if strings.HasPrefix(tc.path, "/api/v1/clients") && !clientRoutesSupported {
				t.Skip("the server edition has no per-client surface")
			}
			rec := scopeGet(t, srv, tc.path, scopeAdminAPIKey)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "unsupported_scope_filter")
			assert.Contains(t, rec.Body.String(), tc.param)
		})
	}
}

// With the list filled, `agent` on summary, usage and sessions is honoured as
// the alias of token (the nil seam above keeps the old gated expectation).
func TestRealRoutes_ListFilled_AgentIsTokenAliasOnSummaryUsageSessions(t *testing.T) {
	srv, _, _ := scopeR9Server(t)
	for _, path := range []string{"/api/v1/activity/summary", "/api/v1/activity/usage", "/api/v1/sessions"} {
		rec := scopeGet(t, srv, path+"?agent=alice", scopeAdminAPIKey)
		assert.NotEqual(t, http.StatusBadRequest, rec.Code, "%s: %s", path, rec.Body.String())
	}
}
