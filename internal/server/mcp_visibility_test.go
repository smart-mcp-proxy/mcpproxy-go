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

// Spec 085 T009 (FR-011, research.md R10): p.toolVisibleToSession is the ONE
// visibility predicate. For a fixed session/agent-token, a tool is returned
// by retrieve_tools IFF the predicate says visible — across agent-scoped,
// profile-scoped, quarantined, pending/changed, and disabled cases. This
// parity is what lets describe_tool (US2) reuse the predicate without
// re-deriving search's rules.

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
	// pending/changed tools are indexed to pin the predicate's verdict even
	// when lifecycle exclusion hasn't caught up.
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

	// Exactly the plain visible tool survives every gate.
	assert.Equal(t, map[string]bool{"github:visible_tool": true}, returned)

	// Parity: returned-by-search IFF visible-by-predicate, for every fixture
	// tool plus one that was never indexed.
	cases := []struct {
		server, tool string
		wantReason   string
	}{
		{"github", "visible_tool", ""},
		{"gitlab", "scoped_tool", visReasonServerNotInScope},
		{"quarry", "lingering_tool", visReasonServerQuarantined},
		{"github", "pending_tool", visReasonToolPendingApproval},
		{"github", "changed_tool", visReasonToolChangedApproval},
		{"github", "disabled_tool", visReasonToolNotCallable},
		{"github", "ghost_tool", visReasonNotIndexed},
	}
	for _, c := range cases {
		t.Run(c.server+":"+c.tool, func(t *testing.T) {
			visible, reason := proxy.toolVisibleToSession(ctx, c.server, c.tool)
			assert.Equal(t, returned[c.server+":"+c.tool], visible,
				"retrieve_tools result set and toolVisibleToSession disagree")
			assert.Equal(t, c.wantReason, reason)
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
	assert.Equal(t, map[string]bool{"github:visible_tool": true}, returned)

	// gitlab is allowed by the agent scope (*) but excluded by the profile.
	visible, reason := proxy.toolVisibleToSession(ctx, "gitlab", "scoped_tool")
	assert.False(t, visible)
	assert.Equal(t, visReasonServerNotInScope, reason)
	assert.False(t, returned["gitlab:scoped_tool"], "parity: profile-scoped tool absent from results")

	visible, _ = proxy.toolVisibleToSession(ctx, "github", "visible_tool")
	assert.True(t, visible)
}

// Admin (no auth restriction, no profile): everything callable is visible;
// quarantine/approval/disable still gate.
func TestToolVisibility_RetrieveParity_Admin(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedVisibilityFixture(t, proxy)

	ctx := auth.WithAuthContext(context.Background(), auth.AdminContext())
	returned := retrieveNames(t, proxy, ctx)

	assert.Equal(t, map[string]bool{
		"github:visible_tool": true,
		"gitlab:scoped_tool":  true, // in scope for admin
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
		assert.Equal(t, returned[name], visible, "parity for %s", name)
	}
}
