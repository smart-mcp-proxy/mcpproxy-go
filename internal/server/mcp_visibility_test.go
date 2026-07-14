package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 085 T009 (FR-006/FR-011, research.md R10) — corrected after the FR-006
// review finding: SEARCH visibility must reproduce the merge-base filter
// semantics exactly (scope → isToolCallable; see the merge-base citations in
// TestRetrieveToolsFullMode_MergeBaseFilterSemantics), while describe_tool's
// predicate (p.toolVisibleToSession) is STRICTLY NARROWER — it adds the
// index-presence, server-quarantine and pending/changed-approval gates its
// contract requires. The security invariant is therefore one-directional:
//
//	predicate-visible ⇒ returned-by-search
//
// (describe_tool never returns a definition search would not), NOT a strict
// IFF: search may return pending/changed/quarantine-lingering tools the
// stricter predicate rejects — exactly what merge-base search returned.

const parityQuery = "parityquery fixture tool"

// seedVisibilityFixture indexes one tool per visibility case. All match the
// same query; distinct description lengths keep BM25 scores tie-free.
func seedVisibilityFixture(t *testing.T, proxy *MCPProxyServer) {
	t.Helper()

	for _, s := range []*config.ServerConfig{
		{Name: "github", Enabled: true},
		{Name: "gitlab", Enabled: true},
		{Name: "quarry", Enabled: true, Quarantined: true},
	} {
		require.NoError(t, proxy.storage.SaveUpstreamServer(s))
	}

	// Approval records: pending / changed / user-disabled (Spec 032/049).
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "pending_tool",
		Status: storage.ToolApprovalStatusPending,
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "changed_tool",
		Status: storage.ToolApprovalStatusChanged,
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "disabled_tool",
		Status: storage.ToolApprovalStatusApproved, Disabled: true,
	}))

	// Indexed tools. quarry:lingering_tool is indexed DESPITE quarantine to
	// model the transient window after a runtime quarantine toggle; the
	// pending/changed tools are indexed to pin the merge-base search verdict
	// (they surface as normal results — isToolCallable ignores Status) against
	// the stricter describe_tool verdict (locked).
	tools := []struct{ name, server, desc string }{
		{"github:visible_tool", "github", parityQuery + " alpha"},
		{"gitlab:scoped_tool", "gitlab", parityQuery + " beta two"},
		{"quarry:lingering_tool", "quarry", parityQuery + " gamma three words"},
		{"github:pending_tool", "github", parityQuery + " delta four words here"},
		{"github:changed_tool", "github", parityQuery + " epsilon five words here now"},
		{"github:disabled_tool", "github", parityQuery + " zeta six words here now again"},
	}
	for _, tool := range tools {
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: tool.name, ServerName: tool.server,
			Description: tool.desc, ParamsJSON: `{"type":"object"}`,
		}))
	}
}

// retrieveNames runs retrieve_tools under ctx and returns the set of returned
// tool names.
func retrieveNames(t *testing.T, proxy *MCPProxyServer, ctx context.Context) map[string]bool {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": parityQuery, "limit": float64(20)}
	result, err := proxy.handleRetrieveTools(ctx, req)
	require.NoError(t, err)
	resp := decodeRetrieve(t, result)
	names := map[string]bool{}
	for _, tl := range resp.Tools {
		names[tl["name"].(string)] = true
	}
	return names
}

func TestToolVisibility_RetrieveParity_AgentScope(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedVisibilityFixture(t, proxy)

	// Agent token scoped to github+quarry (gitlab out of scope).
	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "parity-bot",
		AllowedServers: []string{"github", "quarry"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	returned := retrieveNames(t, proxy, ctx)

	// Merge-base search semantics: scope gate drops gitlab, isToolCallable
	// drops the user-disabled tool; pending/changed/quarantine-lingering tools
	// remain normal results (main: internal/server/mcp.go ~:1345-1363 +
	// isToolCallable ~:5306-5357).
	assert.Equal(t, map[string]bool{
		"github:visible_tool":   true,
		"quarry:lingering_tool": true,
		"github:pending_tool":   true,
		"github:changed_tool":   true,
	}, returned)

	// describe_tool's stricter predicate: reasons per contract; and the
	// one-directional invariant — predicate-visible ⇒ returned-by-search.
	cases := []struct {
		server, tool string
		wantVisible  bool
		wantReason   string
	}{
		{"github", "visible_tool", true, ""},
		{"gitlab", "scoped_tool", false, visReasonServerNotInScope},
		{"quarry", "lingering_tool", false, visReasonServerQuarantined},
		{"github", "pending_tool", false, visReasonToolPendingApproval},
		{"github", "changed_tool", false, visReasonToolChangedApproval},
		{"github", "disabled_tool", false, visReasonToolNotCallable},
		{"github", "ghost_tool", false, visReasonNotIndexed},
	}
	for _, c := range cases {
		t.Run(c.server+":"+c.tool, func(t *testing.T) {
			visible, reason := proxy.toolVisibleToSession(ctx, c.server, c.tool)
			assert.Equal(t, c.wantVisible, visible)
			assert.Equal(t, c.wantReason, reason)
			if visible {
				assert.True(t, returned[c.server+":"+c.tool],
					"describe_tool predicate says visible but search would not return it — invariant violated (FR-011)")
			}
		})
	}
}

func TestToolVisibility_RetrieveParity_ProfileScope(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedVisibilityFixture(t, proxy)

	// A profile pinned to github only. The profile machinery resolves servers
	// against cfg.Servers, so the fixture servers must exist in config too.
	proxy.config.Servers = []*config.ServerConfig{
		{Name: "github", Enabled: true},
		{Name: "gitlab", Enabled: true},
		{Name: "quarry", Enabled: true},
	}
	proxy.config.Profiles = []config.ProfileConfig{{Name: "dev", Servers: []string{"github"}}}

	// Mimic the production profile-index sync: the per-profile index
	// physically holds only the profile's servers' tools (Profiles v2).
	pIdx, err := proxy.index.ForProfile("dev")
	require.NoError(t, err)
	for _, tool := range []struct{ name, desc string }{
		{"github:visible_tool", parityQuery + " alpha"},
		{"github:pending_tool", parityQuery + " delta four words here"},
		{"github:changed_tool", parityQuery + " epsilon five words here now"},
		{"github:disabled_tool", parityQuery + " zeta six words here now again"},
	} {
		require.NoError(t, pIdx.IndexTool(&config.ToolMetadata{
			Name: tool.name, ServerName: "github",
			Description: tool.desc, ParamsJSON: `{"type":"object"}`,
		}))
	}

	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "pinned-bot",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead},
		ProfilePin:     "dev",
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	returned := retrieveNames(t, proxy, ctx)
	// Merge-base semantics within the profile: pending/changed surface as
	// results, the user-disabled tool does not; gitlab is outside the profile.
	assert.Equal(t, map[string]bool{
		"github:visible_tool": true,
		"github:pending_tool": true,
		"github:changed_tool": true,
	}, returned)

	// gitlab is allowed by the agent scope (*) but excluded by the profile.
	visible, reason := proxy.toolVisibleToSession(ctx, "gitlab", "scoped_tool")
	assert.False(t, visible)
	assert.Equal(t, visReasonServerNotInScope, reason)
	assert.False(t, returned["gitlab:scoped_tool"], "profile-scoped tool absent from results")

	visible, _ = proxy.toolVisibleToSession(ctx, "github", "visible_tool")
	assert.True(t, visible)
}

// Admin (no auth restriction, no profile): search follows merge-base
// semantics (only user-disabled drops); the stricter describe_tool predicate
// still gates quarantine/approval — one-directionally.
func TestToolVisibility_RetrieveParity_Admin(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedVisibilityFixture(t, proxy)

	ctx := auth.WithAuthContext(context.Background(), auth.AdminContext())
	returned := retrieveNames(t, proxy, ctx)

	assert.Equal(t, map[string]bool{
		"github:visible_tool":   true,
		"gitlab:scoped_tool":    true, // in scope for admin
		"quarry:lingering_tool": true,
		"github:pending_tool":   true,
		"github:changed_tool":   true,
	}, returned)

	for name, want := range map[string]bool{
		"github:visible_tool":   true,
		"gitlab:scoped_tool":    true,
		"quarry:lingering_tool": false,
		"github:pending_tool":   false,
		"github:changed_tool":   false,
		"github:disabled_tool":  false,
	} {
		server, tool, _ := splitServerTool(name)
		visible, _ := proxy.toolVisibleToSession(ctx, server, tool)
		assert.Equal(t, want, visible, "predicate for %s", name)
		if visible {
			assert.True(t, returned[name],
				"predicate-visible %s must be returned by search (FR-011 upper bound)", name)
		}
	}
}
