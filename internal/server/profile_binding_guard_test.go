package server

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func bindingGuardTestProxy(t *testing.T, anonymousProfile string, profiles []config.ProfileConfig, mode, pin string) (*MCPProxyServer, *profileIndex) {
	t.Helper()
	proxy, rt := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "a", Enabled: true}}, func(cfg *config.Config) {
		cfg.RequireMCPAuth = false
		cfg.AnonymousProfile = anonymousProfile
		cfg.Profiles = profiles
	})
	_, err := rt.StorageManager().MintClientCredential(
		"cursor", "mcp_cli_binding_guard_test", []byte("binding-guard-test-key"),
		mode, pin, time.Now().Add(time.Hour),
	)
	require.NoError(t, err)
	return proxy, proxy.profileIndexFor(proxy.currentConfig())
}

func TestResolveProfileV3_AnonymousDeniedWhenNamedBindingCanBeBypassed(t *testing.T) {
	proxy, idx := bindingGuardTestProxy(t, "", []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}}, auth.ProfileModeLocked, "P")

	got := proxy.ResolveProfileV3(context.Background(), idx)

	require.Equal(t, string(profile.SourceAnonymous), got.Source)
	require.Nil(t, got.Policy)
	require.NotNil(t, got.Scope)
	require.False(t, got.Scope.Allows("a"), "unconfined anonymous access must be deny-all while a named client binding can be bypassed")
	require.True(t, got.BindingGuarded)
	require.True(t, proxy.profileManagementToolHidden(context.Background(), "upstream_servers"), "the deny-all guard must also remove administrator-shaped management access")
	codeExec, err := proxy.handleCodeExecution(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "code_execution"}})
	require.NoError(t, err)
	require.NotNil(t, codeExec)
	require.True(t, codeExec.IsError, "the deny-all guard must refuse code_execution even with no named anonymous profile")
}

func TestResolveProfileV3_AnonymousBindingGuardUsesCurrentToolPolicy(t *testing.T) {
	proxy, idx := bindingGuardTestProxy(t, "Q", []config.ProfileConfig{
		{Name: "P", Servers: []string{"a"}, Tools: &config.ProfileToolRules{Deny: []string{"a:write_tool"}}},
		{Name: "Q", Servers: []string{"a"}},
	}, auth.ProfileModeLocked, "P")
	proxy.mainServer.runtime.Supervisor().StateView().UpdateServer("a", func(status *stateview.ServerStatus) {
		status.ToolsDiscovered = true
		status.Tools = []stateview.ToolInfo{{Name: "write_tool", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)}}}
	})

	got := proxy.ResolveProfileV3(context.Background(), idx)

	require.Equal(t, string(profile.SourceAnonymous), got.Source)
	require.Nil(t, got.Policy)
	require.NotNil(t, got.Scope)
	require.False(t, got.Scope.Allows("a"), "anonymous Q admits a live tool denied by P, so the binding guard must deny the whole anonymous profile")
}

func TestResolveProfileV3_EqualAnonymousProfileDoesNotTripBindingGuard(t *testing.T) {
	proxy, idx := bindingGuardTestProxy(t, "P", []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}}, auth.ProfileModeLocked, "P")

	got := proxy.ResolveProfileV3(context.Background(), idx)

	require.Equal(t, string(profile.SourceAnonymous), got.Source)
	require.Equal(t, "P", got.Name)
	require.NotNil(t, got.Scope)
	require.True(t, got.Scope.Allows("a"), "an anonymous profile equal to the locked binding is not bypassable")
}

func TestBindingGuard_DirectDescribeMatchesHiddenTools(t *testing.T) {
	proxy, _ := bindingGuardTestProxy(t, "", []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}}, auth.ProfileModeLocked, "P")
	tool := &config.ToolMetadata{
		ServerName: "a", Name: "read_tool", ParamsJSON: `{"type":"object"}`,
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
	}
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a", ToolName: "read_tool", Status: storage.ToolApprovalStatusApproved,
	}))
	proxy.publishDirectCatalog(buildDirectCatalog([]*config.ToolMetadata{tool}, nil))

	ctx := anonCtx()
	listed := proxy.filterDirectModeToolsForAuth(ctx, []mcp.Tool{directStampedTool("a", "read_tool", "read")})
	require.Empty(t, listed, "the active binding guard hides the direct tool from tools/list")
	_, visible := proxy.resolveDirectDescribeID(ctx, "a__read_tool")
	require.False(t, visible, "describe_tool must not disclose a tool hidden by the FR-008a guard")

	cat := proxy.loadDirectCatalog()
	plan := proxy.planDirectCheck(ctx, cat, nil, []string{"a__read_tool"})
	require.Empty(t, plan.refs, "check:true must not ask the evaluator about a guard-hidden tool")
	_, gated := plan.gated["a__read_tool"]
	require.True(t, gated, "check:true must answer the hidden id as not_found")
}

func TestHandleSetProfile_AnonymousBindingGuardRefusesSelectionButAllowsClear(t *testing.T) {
	proxy, _ := bindingGuardTestProxy(t, "", []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}}, auth.ProfileModeLocked, "P")
	ctx := sessionCtx(context.Background(), "anonymous-bound-session")
	call := func(slug string) *mcp.CallToolResult {
		t.Helper()
		request := mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name: "set_profile", Arguments: map[string]interface{}{"profile": slug},
		}}
		result, err := proxy.handleSetProfile(ctx, request)
		require.NoError(t, err)
		return result
	}

	refused := call("P")
	require.True(t, refused.IsError, "anonymous callers cannot set a profile that would bypass a named client binding")
	require.Equal(t, "unknown profile 'P'", setProfileResultText(t, refused))
	require.Empty(t, proxy.sessionStore.GetActiveProfile("anonymous-bound-session"), "the refused selection must not mutate the session")

	cleared := call("")
	require.False(t, cleared.IsError, "clearing a stored selection remains admitted for every caller")
	require.Empty(t, proxy.sessionStore.GetActiveProfile("anonymous-bound-session"))
}

func TestBindingBypassable_FR008aReachabilityMatrix(t *testing.T) {
	switchToR := []string{"R"}
	falseVal, trueVal := false, true
	tool := []bindingGuardTool{{server: "a", tool: "write_tool", tier: profile.TierWrite}}
	tests := []struct {
		name      string
		anonymous string
		profiles  []config.ProfileConfig
		mode      string
		pin       string
		tools     []bindingGuardTool
		want      bool
	}{
		{
			name: "equal locked profile is safe", anonymous: "P", pin: "P", mode: auth.ProfileModeLocked,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}},
		},
		{
			name: "unconfined anonymous is always wider", anonymous: "", pin: "P", mode: auth.ProfileModeLocked,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}}, want: true,
		},
		{
			name: "wider effective server set", anonymous: "Q", pin: "P", mode: auth.ProfileModeLocked,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}, {Name: "Q", Servers: []string{"a", "b"}}}, want: true,
		},
		{
			name: "wider tier cap", anonymous: "Q", pin: "P", mode: auth.ProfileModeLocked,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}, MaxTier: config.ProfileTierRead}, {Name: "Q", Servers: []string{"a"}, MaxTier: config.ProfileTierWrite}}, want: true,
		},
		{
			name: "unset cap and destructive cap admit the same tier set", anonymous: "Q", pin: "P", mode: auth.ProfileModeLocked,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}, MaxTier: config.ProfileTierDestructive}, {Name: "Q", Servers: []string{"a"}}},
		},
		{
			name: "more permissive unannotated handling", anonymous: "Q", pin: "P", mode: auth.ProfileModeLocked,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}, Unannotated: config.ProfileUnannotatedDeny}, {Name: "Q", Servers: []string{"a"}, Unannotated: config.ProfileUnannotatedAsRead}}, want: true,
		},
		{
			name: "tool deny omitted on anonymous reach", anonymous: "Q", pin: "P", mode: auth.ProfileModeLocked, tools: tool, want: true,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}, Tools: &config.ProfileToolRules{Deny: []string{"a:write_tool"}}}, {Name: "Q", Servers: []string{"a"}}},
		},
		{
			name: "locked binding cannot reach anonymous switchable target", anonymous: "P", pin: "P", mode: auth.ProfileModeLocked, want: true,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}, SwitchableTo: &switchToR}, {Name: "R", Servers: []string{"b"}, MaxTier: config.ProfileTierDestructive}},
		},
		{
			name: "switchable binding reaches same one-hop target", anonymous: "P", pin: "P", mode: auth.ProfileModeSwitchable,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}, SwitchableTo: &switchToR}, {Name: "R", Servers: []string{"b"}, MaxTier: config.ProfileTierDestructive}},
		},
		{
			name: "code execution capability wider", anonymous: "Q", pin: "P", mode: auth.ProfileModeLocked, want: true,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}, CodeExecution: &falseVal}, {Name: "Q", Servers: []string{"a"}, CodeExecution: &trueVal}},
		},
		{
			name: "management capability wider", anonymous: "Q", pin: "P", mode: auth.ProfileModeLocked, want: true,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}, ManagementTools: &falseVal}, {Name: "Q", Servers: []string{"a"}, ManagementTools: &trueVal}},
		},
		{
			name: "dangling bound base is already deny all", anonymous: "Q", pin: "missing", mode: auth.ProfileModeLocked,
			profiles: []config.ProfileConfig{{Name: "Q", Servers: []string{"a", "b"}}},
		},
		{
			name: "dangling anonymous base is deny all", anonymous: "missing", pin: "P", mode: auth.ProfileModeLocked,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: true}, {Name: "b", Enabled: true}}
			cfg.AnonymousProfile = tt.anonymous
			cfg.Profiles = tt.profiles
			idx := newProfileIndex(cfg)
			binding := &auth.AgentToken{Kind: auth.KindClient, ProfileMode: tt.mode, ProfilePin: tt.pin}
			require.Equal(t, tt.want, bindingBypassable(idx, cfg, binding, tt.tools))
		})
	}
}
