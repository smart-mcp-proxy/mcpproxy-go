package server

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 108 (Profiles v3) FR-011/FR-013 (zcode review round 1): a tool that is
// BOTH policy-excluded AND pending/changed approval must stay silently
// invisible, exactly like every other policy-excluded tool — never named as
// a "locked" entry, never counted as one. collectQuarantinedToolMatches
// filters only by server scope, so without filterLockedMatchesByPolicy such
// a tool would be named in the disabled[] array (a TPA-relevant disclosure:
// the response would confirm the tool's existence via a lock reason) and
// double-counted (once in hidden_by_profile, once in droppedCount/the
// zero-result notice).
type quarantinePolicyRetrieveResponse struct {
	Tools           []map[string]interface{}    `json:"tools"`
	Disabled        []contracts.LockedToolEntry `json:"disabled"`
	HiddenByProfile *int                        `json:"hidden_by_profile"`
	Notice          *string                     `json:"notice"`
}

func TestRetrieveTools_ProfileV3_PolicyExcludedPendingToolNeverNamedAsLocked(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)

	// github:create_issue is write-tier, excluded under work-readonly's
	// read cap (FR-010 above_tier_cap) — and, on top of that, its approval
	// record is now pending, so BOTH gates would separately exclude it.
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "create_issue", Status: storage.ToolApprovalStatusPending,
	}))

	ctx := urlProfileCtx(proxy, "work-readonly")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": "create_issue", "limit": float64(10), "include_disabled": true}
	result, err := proxy.handleRetrieveTools(ctx, req)
	require.NoError(t, err)
	require.False(t, result.IsError)

	var resp quarantinePolicyRetrieveResponse
	require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &resp))

	assert.Empty(t, resp.Tools, "the excluded tool must not appear in tools[]")
	for _, d := range resp.Disabled {
		assert.NotContains(t, d.Name, "create_issue",
			"a policy-excluded tool must never be named as a locked entry, even when it also has a pending approval record")
	}
	require.NotNil(t, resp.HiddenByProfile)
	assert.Equal(t, 1, *resp.HiddenByProfile, "the tool is still counted via hidden_by_profile, exactly once")
}
