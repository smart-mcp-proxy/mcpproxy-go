package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 PR G, FR-010 gap G2 (T095): describe_tool's not-found response,
// including its case-correction suggestion, must be computed over the
// AUTHORIZED corpus only — never change shape merely because a HIDDEN
// document happens to occupy the exact (server, tool) pair the caller asked
// for. Before this fix, toolVisibleToSession checked index presence before
// scope: a caller-supplied id that resolved to a hidden document (reason
// server_not_in_scope) never attempted the case-correction suggestion
// (gated on reason == visReasonNotIndexed), while the identical id with no
// hidden document at that exact pair (reason not_indexed) did — silently
// telling the two fixtures apart by whether the suggestion was present.
//
// Fixture: server "B" (authorized) with tool "read", indexed as "B:read".
// Fixture A additionally indexes a HIDDEN server "b" (case-only difference,
// outside the token's scope) with its own tool "read", carrying a sentinel
// in its description. A "B"-only token asks for the lowercase id "b:read" —
// which is never itself visible (case is never folded on a resolution path)
// — and must get the identical not-found body, WITH the "B:read"
// case-correction suggestion, whether or not the hidden "b" server exists.
func buildDescribeToolScopeFixture(t *testing.T, includeHidden bool) *MCPProxyServer {
	t.Helper()
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "B", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "B", ToolName: "read", Status: storage.ToolApprovalStatusApproved,
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "B:read", ServerName: "B", Description: "authorized read tool",
		ParamsJSON: `{"type":"object"}`,
	}))

	if includeHidden {
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "b", Enabled: true}))
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "b", ToolName: "read", Status: storage.ToolApprovalStatusApproved,
		}))
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: "b:read", ServerName: "b", Description: "HIDDEN_SENTINEL read tool on hidden server",
			ParamsJSON: `{"type":"object"}`,
		}))
	}

	return proxy
}

func describeToolScopedCtx() context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "b-scoped",
		AllowedServers: []string{"B"},
		Permissions:    []string{auth.PermRead},
	})
}

func TestDescribeTool_HiddenCaseCollision_SuggestionUnaffectedByHiddenExistence(t *testing.T) {
	call := func(t *testing.T, proxy *MCPProxyServer) map[string]interface{} {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"tool_ids": []interface{}{"b:read"}}
		result, err := proxy.handleDescribeTool(describeToolScopedCtx(), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Content)

		var resp describeToolResponse
		require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &resp))
		require.Empty(t, resp.Definitions, "a lowercase id must never resolve — case is never folded on a resolution path")
		require.Len(t, resp.Errors, 1)
		return resp.Errors[0]
	}

	withHidden := call(t, buildDescribeToolScopeFixture(t, true))
	withoutHidden := call(t, buildDescribeToolScopeFixture(t, false))

	assert.Equal(t, withoutHidden, withHidden,
		"the response must be byte-identical whether or not a hidden case-collision exists")
	assert.Equal(t, describeErrNotFound, withHidden["error"])
	assert.Contains(t, withHidden["remediation"], "B:read",
		"the case-correction suggestion must survive even though a hidden collision exists")

	for _, resp := range []map[string]interface{}{withHidden, withoutHidden} {
		for _, v := range resp {
			if s, ok := v.(string); ok {
				assert.NotContains(t, s, "HIDDEN_SENTINEL", "no hidden content may leak into the response")
			}
		}
	}
}

// The plain-mode golden corpus (describe_plain_corpus_test.go) does not
// exercise a hidden-collision id, so it stays byte-identical through this
// change; TestDescribeToolPlainCorpus_ByteIdenticalWithOneEnumeratedDelta
// (run separately) is the control for that claim.

// Spec 105 PR G, FR-010 gap G2 — codex round-1 review MUST-FIX: a
// PROFILE-SCOPED ADMINISTRATOR is not a scoped caller (auth.IsScopedCaller
// is false for it — only Type=="admin"/"admin_user" is ever mixed with an
// active profile this way) and is not named as an SC-005 exception for
// FR-010, so its describe_tool response must stay EXACTLY what it was
// before this PR: the case-correction suggestion is attempted only when the
// reason is visReasonNotIndexed, never for visReasonServerNotInScope. That
// is a DIFFERENT (pre-existing, out-of-scope-for-this-gap) asymmetry
// between the two fixtures for an admin — proven here by asserting each
// fixture's admin response independently, not by asserting the two
// fixtures agree (which, unlike the agent-token control in
// TestDescribeTool_HiddenCaseCollision_SuggestionUnaffectedByHiddenExistence,
// they correctly do NOT).
func TestDescribeTool_ProfileScopedAdmin_HiddenCollisionBehaviorUnchanged(t *testing.T) {
	profileScopedAdmin := func() context.Context {
		return profile.WithProfileScope(
			auth.WithAuthContext(context.Background(), auth.AdminContext()),
			profile.NewProfileScope("P", []string{"B"}),
		)
	}

	call := func(t *testing.T, proxy *MCPProxyServer) map[string]interface{} {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"tool_ids": []interface{}{"b:read"}}
		result, err := proxy.handleDescribeTool(profileScopedAdmin(), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Content)

		var resp describeToolResponse
		require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &resp))
		require.Empty(t, resp.Definitions, "a lowercase id must never resolve")
		require.Len(t, resp.Errors, 1)
		return resp.Errors[0]
	}

	// Fixture B (no hidden collision): "b:read" is simply not indexed under
	// that exact pair, so the PRE-fix rule (suggest on visReasonNotIndexed)
	// still fires — the admin control here is that this positive case keeps
	// working, not merely that nothing broke.
	withoutHidden := call(t, buildDescribeToolScopeFixture(t, false))
	assert.Equal(t, describeErrNotFound, withoutHidden["error"])
	assert.Contains(t, withoutHidden["remediation"], "B:read",
		"control: a profile-scoped admin must still get the pre-fix suggestion when there is no hidden collision")

	// Fixture A (hidden collision): the admin is not a scoped caller, so the
	// widened reason set does not apply to it — no suggestion, exactly as
	// before this PR.
	withHidden := call(t, buildDescribeToolScopeFixture(t, true))
	assert.Equal(t, describeErrNotFound, withHidden["error"])
	assert.Equal(t, describeNotFoundRemediation, withHidden["remediation"],
		"a profile-scoped admin must keep the PRE-fix plain remediation for a hidden-collision id — FR-010's agent-only fix must not change this")
}
