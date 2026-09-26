package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 108 (Profiles v3) T018: describe_tool on a profile-excluded tool must
// answer the SAME uniform not-found response a nonexistent tool id gets
// (Spec 105 FR-010 comparison; contracts/mcp-tools.md "describe_tool" — "No
// shape change"), so a caller can never distinguish "excluded by policy"
// from "does not exist".
func TestDescribeTool_ProfileV3_ExcludedEqualsNonexistent(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)

	ctx := urlProfileCtx(proxy, "work-readonly")

	excluded := callDescribe(t, proxy, ctx, []interface{}{"github:create_issue"})
	require.Empty(t, excluded.Definitions, "an excluded tool must never render its definition")
	require.Len(t, excluded.Errors, 1)

	nonexistent := callDescribe(t, proxy, ctx, []interface{}{"github:does_not_exist_at_all"})
	require.Empty(t, nonexistent.Definitions)
	require.Len(t, nonexistent.Errors, 1)

	// Compare after substituting the echoed id — everything else (error
	// code, remediation text) must be byte-identical.
	excluded.Errors[0]["id"] = "SUBSTITUTED"
	nonexistent.Errors[0]["id"] = "SUBSTITUTED"
	assert.Equal(t, nonexistent.Errors[0], excluded.Errors[0],
		"an excluded tool's describe_tool error must be byte-identical to a nonexistent one's")

	t.Run("admitted tool under the same profile still describes normally", func(t *testing.T) {
		resp := callDescribe(t, proxy, ctx, []interface{}{"github:list_issues"})
		require.Empty(t, resp.Errors)
		require.Len(t, resp.Definitions, 1)
		assert.Equal(t, "github:list_issues", resp.Definitions[0]["name"])
	})

	t.Run("legacy profile: excluded-vs-nonexistent distinction does not apply — nothing is policy-excluded", func(t *testing.T) {
		legacyCtx := urlProfileCtx(proxy, "legacy")
		resp := callDescribe(t, proxy, legacyCtx, []interface{}{"github:create_issue"})
		require.Empty(t, resp.Errors)
		require.Len(t, resp.Definitions, 1, "legacy has no policy field set — create_issue is simply admitted")
	})

	t.Run("a tool that is BOTH policy-excluded and pending approval still answers the uniform not-found, never the pending lock (zcode review round 1)", func(t *testing.T) {
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "github", ToolName: "create_issue", Status: storage.ToolApprovalStatusPending,
		}))
		t.Cleanup(func() {
			require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "github", ToolName: "create_issue", Status: storage.ToolApprovalStatusApproved,
			}))
		})

		resp := callDescribe(t, proxy, ctx, []interface{}{"github:create_issue"})
		require.Empty(t, resp.Definitions)
		require.Len(t, resp.Errors, 1)
		resp.Errors[0]["id"] = "SUBSTITUTED"
		assert.Equal(t, nonexistent.Errors[0], resp.Errors[0],
			"a policy exclusion must win over the pending-approval lock's own, more specific reason — describe_tool must never confirm the tool exists")
	})

	t.Run("pin source: the same uniform not-found, without ever naming the profile", func(t *testing.T) {
		resp := callDescribe(t, proxy, pinnedProfileCtx("work-readonly"), []interface{}{"github:create_issue"})
		require.Len(t, resp.Errors, 1)
		for _, v := range resp.Errors[0] {
			if s, ok := v.(string); ok {
				assert.NotContains(t, s, "work-readonly", "the refusal must never name the profile")
			}
		}
	})
}
