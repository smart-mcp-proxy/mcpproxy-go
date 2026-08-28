package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/jsruntime"
)

// A profile pin is only a security boundary if EVERY dispatch surface resolves
// it. These tests cover the three surfaces that read the resolver (or used to
// skip it) besides retrieve_tools/call_tool: the code_execution sandbox,
// direct-routing mode, and the set_profile response.
//
// Each case is exercised twice — with the pinned profile present (it narrows)
// and after it has been deleted (it denies) — because a surface that ignores
// profiles entirely passes the second assertion for the wrong reason.

func pinnedProxy(t *testing.T, profiles []config.ProfileConfig) (*MCPProxyServer, *config.Config) {
	t.Helper()
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "research-srv", Enabled: true},
			{Name: "deploy-srv", Enabled: true},
		},
		Profiles: profiles,
	}
	return &MCPProxyServer{config: cfg, sessionStore: NewSessionStore(zap.NewNop()), logger: zap.NewNop()}, cfg
}

// pinnedAgentContext is a token scoped to BOTH servers but pinned to one
// profile — the pin, not the token scope, is what must bound it.
func pinnedAgentContext(pin string) context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "pinned-agent",
		ProfilePin:     pin,
		AllowedServers: []string{"research-srv", "deploy-srv"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	})
}

// The sandbox used to read the URL-injected scope only, so a token pinned on
// the BASE /mcp endpoint (no URL scope) ran with its full token scope.
func TestCodeExecutionHonorsTokenProfilePin(t *testing.T) {
	proxy, cfg := pinnedProxy(t, []config.ProfileConfig{
		{Name: "research", Servers: []string{"research-srv"}},
	})
	ctx := pinnedAgentContext("research")

	opts := jsruntime.ExecutionOptions{}
	proxy.applyProfileScopeToExecution(ctx, &opts)
	assert.True(t, opts.RestrictToAllowed, "an active pin must restrict the sandbox")
	assert.Equal(t, []string{"research-srv"}, opts.AllowedServers,
		"the sandbox must see the pinned profile's servers, not the token's wider scope")

	// A caller-supplied allow-list can only narrow further, never widen.
	opts = jsruntime.ExecutionOptions{AllowedServers: []string{"research-srv", "deploy-srv"}}
	proxy.applyProfileScopeToExecution(ctx, &opts)
	assert.Equal(t, []string{"research-srv"}, opts.AllowedServers)

	// The operator deletes the pinned profile: deny-all, and RestrictToAllowed
	// must stay set — the jsruntime reads an empty allow-list as "allow all".
	cfg.Profiles = nil
	opts = jsruntime.ExecutionOptions{}
	proxy.applyProfileScopeToExecution(ctx, &opts)
	assert.True(t, opts.RestrictToAllowed)
	assert.Empty(t, opts.AllowedServers, "a stale pin must leave the sandbox with no reachable server")

	// An unpinned, unprofiled request is untouched (allow-all stays allow-all).
	opts = jsruntime.ExecutionOptions{}
	proxy.applyProfileScopeToExecution(context.Background(), &opts)
	assert.False(t, opts.RestrictToAllowed)
	assert.Nil(t, opts.AllowedServers)

	// nil options is a no-op, not a panic.
	assert.NotPanics(t, func() { proxy.applyProfileScopeToExecution(ctx, nil) })
}

// Direct-routing mode registers upstream tools as server__tool and enforced only
// the token's allowed_servers — no profile at all. A pinned token therefore saw
// and could call every server in its token scope.
func TestDirectModeHonorsTokenProfilePin(t *testing.T) {
	proxy, cfg := pinnedProxy(t, []config.ProfileConfig{
		{Name: "research", Servers: []string{"research-srv"}},
	})
	publishPermsCatalog(proxy, map[string]string{
		"research-srv__search": auth.PermRead,
		"deploy-srv__ship":     auth.PermRead,
	})
	tools := []mcp.Tool{{Name: "research-srv__search"}, {Name: "deploy-srv__ship"}}
	ctx := pinnedAgentContext("research")

	names := func(filtered []mcp.Tool) []string {
		out := make([]string, 0, len(filtered))
		for _, tool := range filtered {
			out = append(out, tool.Name)
		}
		return out
	}

	assert.Equal(t, []string{"research-srv__search"}, names(proxy.filterDirectModeToolsForAuth(ctx, tools)),
		"direct-mode discovery must drop tools outside the pinned profile")

	handler := proxy.makeDirectModeHandler(&directCatalogEntry{ServerName: "deploy-srv", ToolName: "ship", DisplayName: FormatDirectToolName("deploy-srv", "ship"), Annotations: nil})
	result, err := handler(ctx, mcp.CallToolRequest{})
	require.NoError(t, err)
	require.True(t, result.IsError, "a call outside the pinned profile must be refused")
	assert.Contains(t, resultText(t, result), "is not in profile 'research'")

	// Profile deleted → deny-all on both discovery and dispatch.
	cfg.Profiles = nil
	assert.Empty(t, proxy.filterDirectModeToolsForAuth(ctx, tools),
		"a stale pin must hide every direct-mode tool")

	handler = proxy.makeDirectModeHandler(&directCatalogEntry{ServerName: "research-srv", ToolName: "search", DisplayName: FormatDirectToolName("research-srv", "search"), Annotations: nil})
	result, err = handler(ctx, mcp.CallToolRequest{})
	require.NoError(t, err)
	require.True(t, result.IsError, "a stale pin must refuse even the formerly pinned server")

	// An admin (no auth context, no profile) is unaffected.
	assert.Len(t, proxy.filterDirectModeToolsForAuth(context.Background(), tools), 2)
}

// set_profile("") clears the session tier, which a pin outranks anyway. The
// response must describe the scope that remains, not the full server list.
func TestSetProfileClearReportsPinnedScope(t *testing.T) {
	proxy, cfg := pinnedProxy(t, []config.ProfileConfig{
		{Name: "research", Servers: []string{"research-srv"}},
	})
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(pinnedAgentContext("research"), &fakeClientSession{id: "sess-pin-clear"})

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{"profile": ""}

	result, err := proxy.handleSetProfile(ctx, request)
	require.NoError(t, err)
	require.False(t, result.IsError)
	payload := decodeSetProfilePayload(t, result)
	assert.Equal(t, "research", payload["active_profile"])
	assert.Equal(t, []interface{}{"research-srv"}, payload["servers"],
		"clearing must not advertise servers the pin still denies")

	// With the pinned profile deleted, the honest answer is an empty list.
	cfg.Profiles = nil
	result, err = proxy.handleSetProfile(ctx, request)
	require.NoError(t, err)
	require.False(t, result.IsError)
	payload = decodeSetProfilePayload(t, result)
	assert.Equal(t, "research", payload["active_profile"])
	assert.Empty(t, payload["servers"])
}

func decodeSetProfilePayload(t *testing.T, result *mcp.CallToolResult) map[string]interface{} {
	t.Helper()
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
	return payload
}
