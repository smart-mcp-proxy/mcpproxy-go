package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 108 FR-011: usageStatEligible (retrieve_tools' include_stats filter)
// must apply the same v3 tool-policy check its two sibling resolvers
// (toolVisibleToSession, indexedToolVisible) already receive, so a
// policy-hidden tool can never be NAMED in usage_summary.top_tools — even
// though tools[] correctly omits it and hidden_by_profile correctly counts
// it as hidden. Reproduces zcode review finding F1.
func TestRetrieveTools_ProfileV3_UsageStatsExcludePolicyHiddenTool(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)

	// github:create_issue is excluded from "work-readonly" by the profile's
	// MaxTier:"read" cap (create_issue is a write-tier tool) — an EARLIER
	// caller's usage (fleet-wide stats: IncrementToolUsage is keyed only by
	// tool name, not by caller) leaves a usage record behind.
	require.NoError(t, proxy.storage.IncrementToolUsage("github:create_issue"))
	require.NoError(t, proxy.storage.IncrementToolUsage("github:list_issues"))

	ctx := pinnedProfileCtx("work-readonly")
	resp := callRetrieveScoped(t, proxy, ctx, map[string]interface{}{
		"query": "list_issues", "include_stats": true,
	})

	require.NotNil(t, resp.UsageSummary.TopTools)
	for _, stat := range resp.UsageSummary.TopTools {
		assert.NotEqual(t, "github:create_issue", stat["tool_name"],
			"FR-011: usage_summary.top_tools must never name a tool the profile policy hides, "+
				"even though tools[] omits it and hidden_by_profile counts it")
	}

	// Sanity control: the caller really is scoped (IsScopedCaller true),
	// otherwise the assertion above would pass vacuously via the admin
	// (unfiltered GetToolStats) branch.
	require.True(t, auth.IsScopedCaller(ctx))
}
