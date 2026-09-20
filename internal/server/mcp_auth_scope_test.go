package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// createTestMCPProxyServer creates a minimal MCPProxyServer for testing.
func createTestMCPProxyServer(t *testing.T) *MCPProxyServer {
	t.Helper()

	tmpDir := t.TempDir()
	logger := zap.NewNop()

	sm, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { sm.Close() })

	idx, err := index.NewManager(tmpDir, logger)
	require.NoError(t, err)
	t.Cleanup(func() { idx.Close() })

	secretResolver := secret.NewResolver()
	cfg := config.DefaultConfig()
	cfg.DataDir = tmpDir
	cfg.ToolsLimit = 20
	cfg.AllowServerAdd = true
	cfg.AllowServerRemove = true

	um := upstream.NewManager(logger, cfg, nil, secretResolver, nil)

	cm, err := cache.NewManager(sm.GetDB(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { cm.Close() })

	tr := truncate.NewTruncator(0)

	proxy := NewMCPProxyServer(sm, idx, um, cm, func() *truncate.Truncator { return tr }, logger, nil, false, cfg, nil)
	return proxy
}

func TestHandleCallToolVariant_AgentScope_ServerBlocked(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	// Create agent context that can only access "github"
	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-bot",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	// Try to call a tool on "gitlab" server — should be blocked
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"name": "gitlab:create_issue",
	}

	result, err := proxy.handleCallToolVariant(ctx, request, contracts.ToolVariantRead)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.IsError, "Should return error for out-of-scope server")

	// Extract error message
	if len(result.Content) > 0 {
		if text, ok := result.Content[0].(mcp.TextContent); ok {
			assert.Contains(t, text.Text, "not in scope")
		}
	}
}

func TestHandleCallToolVariant_AgentScope_PermissionDenied(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	// Create agent context with read-only permissions
	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "readonly-bot",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	// Try to call a write tool — should be blocked
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"name": "github:create_issue",
	}

	result, err := proxy.handleCallToolVariant(ctx, request, contracts.ToolVariantWrite)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.IsError, "Should return error for insufficient permissions")

	if len(result.Content) > 0 {
		if text, ok := result.Content[0].(mcp.TextContent); ok {
			assert.Contains(t, text.Text, "Insufficient permissions")
			assert.Contains(t, text.Text, "write")
		}
	}
}

func TestHandleCallToolVariant_AgentScope_DestructiveDenied(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "rw-bot",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"name": "github:delete_repo",
	}

	result, err := proxy.handleCallToolVariant(ctx, request, contracts.ToolVariantDestructive)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.IsError, "Should return error for destructive without permission")

	if len(result.Content) > 0 {
		if text, ok := result.Content[0].(mcp.TextContent); ok {
			assert.Contains(t, text.Text, "destructive")
		}
	}
}

func TestHandleCallToolVariant_AdminContext_Allowed(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	// Admin context should not be restricted
	adminCtx := auth.AdminContext()
	ctx := auth.WithAuthContext(context.Background(), adminCtx)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"name": "any-server:any-tool",
	}

	// This will fail because the server doesn't exist, but it should NOT fail
	// due to auth scope — it should get past the auth check
	result, err := proxy.handleCallToolVariant(ctx, request, contracts.ToolVariantDestructive)
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// The error should be about missing server, not auth
	if result.IsError && len(result.Content) > 0 {
		if text, ok := result.Content[0].(mcp.TextContent); ok {
			assert.NotContains(t, text.Text, "not in scope")
			assert.NotContains(t, text.Text, "Insufficient permissions")
		}
	}
}

func TestHandleUpstreamServers_AgentBlocked_WriteOps(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-bot",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	blockedOps := []string{"add", "remove", "update", "patch", "enable", "disable", "restart", "refresh"}
	for _, op := range blockedOps {
		t.Run("operation_"+op, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]interface{}{
				"operation": op,
			}

			result, err := proxy.handleUpstreamServers(ctx, request)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.True(t, result.IsError, "Agent should not be able to perform %s", op)

			if len(result.Content) > 0 {
				if text, ok := result.Content[0].(mcp.TextContent); ok {
					assert.Contains(t, text.Text, "Agent tokens cannot perform")
				}
			}
		})
	}
}

func TestHandleUpstreamServers_AgentAllowed_ListOp(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-bot",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"operation": "list",
	}

	result, err := proxy.handleUpstreamServers(ctx, request)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// List should succeed (even if empty results)
	assert.False(t, result.IsError, "Agent should be able to list servers")
}

func TestHandleUpstreamServers_AdminAllowed_WriteOps(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	adminCtx := auth.AdminContext()
	ctx := auth.WithAuthContext(context.Background(), adminCtx)

	// Admin should be able to attempt write operations
	// (They may fail for other reasons, but not auth)
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "test-server",
		"url":       "https://example.com/mcp",
		"protocol":  "http",
	}

	result, err := proxy.handleUpstreamServers(ctx, request)
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// If there's an error, it should NOT be about agent tokens
	if result.IsError && len(result.Content) > 0 {
		if text, ok := result.Content[0].(mcp.TextContent); ok {
			assert.NotContains(t, text.Text, "Agent tokens cannot perform")
		}
	}
}

func TestHandleListUpstreams_FilteredForAgent(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	// Add some test servers to storage
	servers := []*config.ServerConfig{
		{Name: "github", URL: "https://github.com/mcp", Protocol: "http", Enabled: true},
		{Name: "gitlab", URL: "https://gitlab.com/mcp", Protocol: "http", Enabled: true},
		{Name: "bitbucket", URL: "https://bitbucket.com/mcp", Protocol: "http", Enabled: true},
	}
	for _, s := range servers {
		require.NoError(t, proxy.storage.SaveUpstreamServer(s))
	}

	// Agent with access to only github and gitlab
	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "scoped-bot",
		AllowedServers: []string{"github", "gitlab"},
		Permissions:    []string{auth.PermRead},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	result, err := proxy.handleListUpstreams(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.IsError)

	// Parse the JSON result to check filtering
	if len(result.Content) > 0 {
		if text, ok := result.Content[0].(mcp.TextContent); ok {
			var parsed map[string]interface{}
			err := json.Unmarshal([]byte(text.Text), &parsed)
			require.NoError(t, err)

			total, ok := parsed["total"].(float64)
			require.True(t, ok, "should have total field")
			assert.Equal(t, float64(2), total, "Agent should only see 2 out of 3 servers")

			servers, ok := parsed["servers"].([]interface{})
			require.True(t, ok, "should have servers field")
			assert.Len(t, servers, 2, "Agent should only see 2 servers")

			// Verify only github and gitlab are returned
			serverNames := make(map[string]bool)
			for _, s := range servers {
				if srv, ok := s.(map[string]interface{}); ok {
					if name, ok := srv["name"].(string); ok {
						serverNames[name] = true
					}
				}
			}
			assert.True(t, serverNames["github"], "Should include github")
			assert.True(t, serverNames["gitlab"], "Should include gitlab")
			assert.False(t, serverNames["bitbucket"], "Should NOT include bitbucket")
		}
	}
}

func TestHandleListUpstreams_AdminSeesAll(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	// Add test servers
	servers := []*config.ServerConfig{
		{Name: "github", URL: "https://github.com/mcp", Protocol: "http", Enabled: true},
		{Name: "gitlab", URL: "https://gitlab.com/mcp", Protocol: "http", Enabled: true},
	}
	for _, s := range servers {
		require.NoError(t, proxy.storage.SaveUpstreamServer(s))
	}

	adminCtx := auth.AdminContext()
	ctx := auth.WithAuthContext(context.Background(), adminCtx)

	result, err := proxy.handleListUpstreams(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.IsError)

	if len(result.Content) > 0 {
		if text, ok := result.Content[0].(mcp.TextContent); ok {
			var parsed map[string]interface{}
			err := json.Unmarshal([]byte(text.Text), &parsed)
			require.NoError(t, err)

			total, ok := parsed["total"].(float64)
			require.True(t, ok)
			assert.Equal(t, float64(2), total, "Admin should see all servers")
		}
	}
}

// Spec 105 PR G, FR-010 gap G1 (T094): the "No client found" hint's
// "Available servers:" list must be filtered through the caller's effective
// scope — the SAME predicate (serverInScope = profile ∩ token) the dispatch
// gates just evaluated to authorize server "a" — so a hidden server's mere
// presence in the fleet is never disclosed through a refusal meant to help
// the caller find its OWN servers. The differential oracle: an a-only token
// calling call_tool_read a:t (server "a" has no client at all) must get
// byte-identical text whether hidden servers "b" and "a__b" are configured
// (unconnected) elsewhere in the fleet or absent entirely, and the text must
// never name them.
func TestHandleCallToolVariant_NoLiveClient_AvailableServersFilteredByScope(t *testing.T) {
	build := func(t *testing.T, hiddenServers ...string) *MCPProxyServer {
		t.Helper()
		proxy := createTestMCPProxyServer(t)
		// "a" is registered in STORAGE (so the target-tier/callability gates
		// see a real, enabled server config and pass through) but
		// deliberately never added to the upstream MANAGER — GetClient("a")
		// must report not-found, landing in the "No client found" branch
		// under test.
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", Enabled: true}))
		// The hidden servers ARE added to the upstream manager (as
		// unconnected clients, exactly as a real fleet would carry servers
		// this token cannot reach) so GetAllServerNames() returns them —
		// proving the assertion below is about FILTERING, not an
		// accidentally-empty list.
		for _, name := range hiddenServers {
			require.NoError(t, proxy.upstreamManager.AddServerConfig(name, &config.ServerConfig{Name: name, Enabled: true}))
		}
		return proxy
	}

	callA := func(t *testing.T, proxy *MCPProxyServer) string {
		t.Helper()
		// Full tier (fullTierAgentOn): this fixture carries no StateView
		// entry for "a", so the FR-009 target-tier gate cannot resolve the
		// tool's annotations and falls back to its deny-by-default
		// "destructive" classification (Spec 104 FR-016f) — a read-only
		// token would be refused for insufficient permission before ever
		// reaching the "no client" branch this test is about. Full
		// permissions is the established workaround for a no-runtime fixture
		// (see TestCallToolVariant_InteriorPaddingPassesAgentScope).
		ctx := fullTierAgentOn("a")
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"name": "a:t"}
		result, err := proxy.handleCallToolVariant(ctx, request, contracts.ToolVariantRead)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, result.IsError, "server 'a' has no client in this fixture")
		require.NotEmpty(t, result.Content)
		return result.Content[0].(mcp.TextContent).Text
	}

	full := build(t, "b", "a__b")
	require.ElementsMatch(t, []string{"b", "a__b"}, full.upstreamManager.GetAllServerNames(),
		"fixture: the hidden servers must actually be present in the raw fleet, or this test proves nothing")
	aOnly := build(t)

	textFull := callA(t, full)
	textAOnly := callA(t, aOnly)

	assert.Equal(t, textAOnly, textFull,
		"an a-only token's refusal text must be identical whether or not hidden servers exist in the fleet")
	assert.Contains(t, textFull, "No client found for server: a.")

	// The hint must report the CALLER's own empty visible set, not the raw
	// fleet: "a" itself has no client (so it cannot appear either), and "b"/
	// "a__b" are outside this token's scope.
	assert.Contains(t, textFull, "Available servers: [(no servers configured)].",
		"a scoped caller with nothing visible must see the same 'nothing configured' hint an actually-empty fleet would show")
	assert.NotContains(t, textFull, "a__b")
	// "b" alone is a risky substring check (it appears inside ordinary
	// words), so assert directly on the bracketed server-list segment
	// instead of scanning the whole message.
	start := strings.Index(textFull, "Available servers: [") + len("Available servers: [")
	end := strings.Index(textFull[start:], "]")
	require.Greater(t, end, -1)
	assert.Equal(t, "(no servers configured)", textFull[start:start+end])
}

// Spec 105 PR G, FR-010 gap G7 (T099): effective scope is profile ∩ token,
// checked in ONE evaluation — a server inside the token's pinned profile but
// outside the token's OWN allowed-server list must be indistinguishable from
// a server that does not exist at all. Token: allowed=[a], pinned to profile
// P={a,b}. call_tool_read on "b:t" (in the pin, not in the token) and on
// "zzz:t" (in neither) must produce byte-identical refusal text.
func TestHandleCallToolVariant_PinWiderThanToken_IndistinguishableFromNonexistent(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	proxy.config.Profiles = []config.ProfileConfig{
		{Name: "P", Servers: []string{"a", "b"}},
	}

	call := func(t *testing.T, target string) string {
		t.Helper()
		ctx := agentCtx([]string{"a"}, []string{auth.PermRead}, "P")
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"name": target + ":t"}
		result, err := proxy.handleCallToolVariant(ctx, request, contracts.ToolVariantRead)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, result.IsError)
		require.NotEmpty(t, result.Content)
		return result.Content[0].(mcp.TextContent).Text
	}

	textB := call(t, "b")
	textZZZ := call(t, "zzz")

	// The message echoes the caller's OWN input (the server name it just
	// typed), which discloses nothing — the caller already knows what it
	// asked for. "Identical" here means the TEMPLATE is identical: normalize
	// the one echoed segment out before comparing, so the assertion is about
	// the refusal's shape, not a coincidence of picking two equal-length
	// names.
	normB := strings.Replace(textB, "'b'", "'<target>'", 1)
	normZZZ := strings.Replace(textZZZ, "'zzz'", "'<target>'", 1)
	assert.Equal(t, normZZZ, normB,
		"a server inside the pin but outside the token's own scope must refuse identically (same template) to a nonexistent server")
	assert.Contains(t, textB, "Server 'b' is not in scope for this agent token")
	assert.Contains(t, textZZZ, "Server 'zzz' is not in scope for this agent token")
}

func TestHandleQuarantineSecurity_AgentBlocked(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-bot",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"operation": "list_quarantined",
	}

	result, err := proxy.handleQuarantineSecurity(ctx, request)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.IsError)

	if len(result.Content) > 0 {
		if text, ok := result.Content[0].(mcp.TextContent); ok {
			assert.Contains(t, text.Text, "Agent tokens cannot perform quarantine")
		}
	}
}
