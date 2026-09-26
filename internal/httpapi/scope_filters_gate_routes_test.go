package httpapi

import (
	"net/http"
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
	}

	for _, path := range gatedRoutes {
		t.Run(path, func(t *testing.T) {
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
