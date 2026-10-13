package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-h T086 / H8 / H9: who may see and call `profiles` (FR-017) and what the
// two global gates do to it. The visibility rule: an administrator credential
// (api_key or socket, never an anonymous one) under no profile or a profile with
// management_tools: true. Everything else - agent tokens, client credentials,
// anonymous callers even unconfined, a profile with the field unset or false -
// neither lists nor can call it, and a hidden call is `unknown tool: profiles`.

// profilesVisibilityRow is one caller in one configuration and whether the tool is
// visible to it.
type profilesVisibilityRow struct {
	name      string
	configure func(*config.Config)
	caller    func(proxy *MCPProxyServer) context.Context
	want      bool
	// attributed: a hidden call by this caller is recorded as a
	// profile_management refusal (an administrator credential the profile hides
	// it from).
	attributed bool
}

func profilesVisibilityRows() []profilesVisibilityRow {
	allPerms := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
	plain := func(ctx context.Context) func(*MCPProxyServer) context.Context {
		return func(*MCPProxyServer) context.Context { return ctx }
	}
	onURL := func(slug string) func(*MCPProxyServer) context.Context {
		return func(proxy *MCPProxyServer) context.Context {
			return profile.WithProfileScope(apiKeyCtx(), proxy.profileScopeForSlug(slug))
		}
	}
	onSession := func(slug string) func(*MCPProxyServer) context.Context {
		return func(proxy *MCPProxyServer) context.Context {
			sid := fmt.Sprintf("vis-%s-%d", slug, time.Now().UnixNano())
			proxy.sessionStore.SetActiveProfile(sid, slug)
			return sessionCtx(apiKeyCtx(), sid)
		}
	}
	return []profilesVisibilityRow{
		{name: "api_key, no profile", caller: plain(apiKeyCtx()), want: true},
		{name: "socket, no profile", caller: plain(socketCtx()), want: true},
		{name: "api_key, session on management_tools:true", configure: withManagementOnProfile, caller: onSession(managementMatrixProfile), want: true},
		{name: "api_key, session on management_tools:false", caller: onSession("work-readonly"), attributed: true},
		{name: "api_key, session on a legacy profile (field unset)", caller: onSession("legacy"), attributed: true},
		{name: "api_key, session on a v3 profile leaving the field unset", caller: onSession("work-full"), attributed: true},
		{name: "api_key, URL profile with management_tools:true", configure: withManagementOnProfile, caller: onURL(managementMatrixProfile), want: true},
		{name: "api_key, URL profile work-readonly", caller: onURL("work-readonly"), attributed: true},
		{name: "anonymous, unconfined", caller: plain(unconfinedAnonymousCtx())},
		{name: "anonymous, no auth context", caller: plain(context.Background())},
		{
			name: "anonymous confined to a management_tools:true profile",
			configure: func(cfg *config.Config) {
				withManagementOnProfile(cfg)
				cfg.AnonymousProfile = managementMatrixProfile
			},
			caller: plain(unconfinedAnonymousCtx()),
		},
		{name: "agent token, unpinned", caller: plain(agentCtx([]string{"*"}, allPerms, ""))},
		{name: "agent token pinned to management_tools:true", configure: withManagementOnProfile,
			caller: plain(agentCtx([]string{"*"}, allPerms, managementMatrixProfile))},
		{name: "client credential under management_tools:true", configure: withManagementOnProfile,
			caller: plain(clientCtx("desktop", managementMatrixProfile, "locked"))},
		{name: "client credential under work-readonly", caller: plain(clientCtx("cursor", "work-readonly", "locked"))},
		{name: "client credential, dangling binding", caller: plain(clientCtx("laptop", "gone", "switchable"))},
		{name: "session principal (cookie)", caller: plain(auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAdminUser, CredentialKind: auth.CredentialKindCookie, Role: "admin"}))},
		{name: "admin with no recorded credential kind", caller: plain(adminCtx())},
		{
			// FR-008a: anonymous under the runtime guard is deny-all; nothing
			// unlocks the admin tool for it.
			name: "anonymous under the binding guard",
			configure: func(cfg *config.Config) {
				cfg.RequireMCPAuth = false
				cfg.AnonymousProfile = ""
			},
			caller: plain(unconfinedAnonymousCtx()),
		},
	}
}

func TestProfilesTool_Visibility(t *testing.T) {
	for _, row := range profilesVisibilityRows() {
		t.Run(row.name, func(t *testing.T) {
			f := newProfilesToolFixture(t, row.configure)
			ctx := row.caller(f.proxy)
			tools := []mcp.Tool{{Name: "profiles"}, {Name: "call_tool_read"}}

			// tools/list filter.
			names := profileV3ToolNames(f.proxy.filterProfileV3Tools(ctx, tools))
			require.Contains(t, names, "call_tool_read")
			assert.Equal(t, row.want, containsName(names, "profiles"), "tools/list")

			// The handler re-checks (defence in depth).
			res, err := f.proxy.handleProfiles(ctx, mcpCallRequest(map[string]any{"operation": "list"}))
			require.NoError(t, err)
			if row.want {
				require.False(t, res.IsError, resultText(t, res))
			} else {
				require.True(t, res.IsError)
				assert.Equal(t, "unknown tool: profiles", resultText(t, res))
			}
		})
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func mcpCallRequest(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = "profiles"
	req.Params.Arguments = args
	return req
}

// TestProfilesTool_VisibilityThroughTheMCPSurfaces runs the same decision through
// a real mcp-go instance on every surface that registers the tool: tools/list,
// and tools/call re-running the filter.
func TestProfilesTool_VisibilityThroughTheMCPSurfaces(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	rpc := func(handle func(context.Context, []byte) any, ctx context.Context, method, params string) map[string]any {
		raw, err := json.Marshal(handle(ctx, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, method, params))))
		require.NoError(t, err)
		var out map[string]any
		require.NoError(t, json.Unmarshal(raw, &out), string(raw))
		return out
	}
	servers := map[string]func(context.Context, []byte) any{
		"default": func(ctx context.Context, m []byte) any { return f.proxy.server.HandleMessage(ctx, m) },
		"call":    func(ctx context.Context, m []byte) any { return f.proxy.callToolServer.HandleMessage(ctx, m) },
		"code":    func(ctx context.Context, m []byte) any { return f.proxy.codeExecServer.HandleMessage(ctx, m) },
		"direct":  func(ctx context.Context, m []byte) any { return f.proxy.directServer.HandleMessage(ctx, m) },
	}

	listed := func(out map[string]any) bool {
		result, _ := out["result"].(map[string]any)
		tools, _ := result["tools"].([]any)
		for _, tool := range tools {
			if tool.(map[string]any)["name"] == "profiles" {
				return true
			}
		}
		return false
	}
	for name, handle := range servers {
		t.Run(name, func(t *testing.T) {
			wantListed := name != "direct" // never on the direct server
			admin := rpc(handle, apiKeyCtx(), "tools/list", `{}`)
			assert.Equal(t, wantListed, listed(admin), "administrator tools/list")
			agent := rpc(handle, agentCtx([]string{"*"}, []string{auth.PermRead}, ""), "tools/list", `{}`)
			assert.False(t, listed(agent), "agent token tools/list")
			anon := rpc(handle, unconfinedAnonymousCtx(), "tools/list", `{}`)
			assert.False(t, listed(anon), "anonymous tools/list")

			if !wantListed {
				return
			}
			// tools/call by an administrator works; by an agent it does not.
			call := rpc(handle, apiKeyCtx(), "tools/call", `{"name":"profiles","arguments":{"operation":"list"}}`)
			result, _ := call["result"].(map[string]any)
			require.NotNil(t, result, call)
			assert.NotEqual(t, true, result["isError"], call)
			denied := rpc(handle, agentCtx([]string{"*"}, []string{auth.PermRead}, ""), "tools/call", `{"name":"profiles","arguments":{"operation":"list"}}`)
			deniedResult, _ := denied["result"].(map[string]any)
			assert.True(t, denied["error"] != nil || (deniedResult != nil && deniedResult["isError"] == true), "a hidden tool must not be callable: %v", denied)
			assert.NotContains(t, toJSONString(denied), "work-full", "a refused call discloses nothing")
		})
	}
}

// --- global gates (H9) ----------------------------------------------------------

var profilesReadArgs = []map[string]any{
	{"operation": "list"},
	{"operation": "get", "name": "work-full"},
	{"operation": "list_clients"},
	{"operation": "effective_tools", "name": "work-full"},
	{"operation": "explain", "profile": "work-full", "tool": "github:list_issues"},
}

var profilesMutatingArgs = []map[string]any{
	{"operation": "create", "name": "gate-tmp", "servers": []any{"github"}},
	{"operation": "update", "name": "work-full", "servers": []any{"github"}},
	{"operation": "delete", "name": "legacy"},
	{"operation": "rename", "name": "legacy", "new_name": "legacy2"},
	{"operation": "classify", "name": "work-full", "tool": "github:search_code", "tier": "read"},
	{"operation": "assign", "client": "cursor", "profile": "work-full"},
}

func assertGatedOps(t *testing.T, f *profilesToolFixture, wantText string) {
	t.Helper()
	before := f.configBytes()
	for _, caller := range []context.Context{apiKeyCtx(), socketCtx()} {
		names := profileV3ToolNames(f.proxy.filterProfileV3Tools(caller, []mcp.Tool{{Name: "profiles"}, {Name: "upstream_servers"}}))
		assert.Contains(t, names, "profiles", "the tool stays listed for an administrator")
		for _, args := range profilesReadArgs {
			if args["operation"] == "list_clients" && !clientsEdition {
				continue
			}
			_, isErr, text := f.call(caller, args)
			assert.False(t, isErr, "%v must run: %s", args["operation"], text)
		}
		for _, args := range profilesMutatingArgs {
			body := f.refused(caller, args)
			assert.Equal(t, wantText, body["error"], "%v", args["operation"])
			assert.Len(t, body, 1, "the gate refusal is {error} only")
		}
	}
	assert.Equal(t, before, f.configBytes(), "a gated mutation writes nothing")
}

// TestProfilesTool_ReadOnlyMode_ListedReadsRunMutationsRefused fails if the tool
// is registered through buildManagementTools(), which returns nothing under the
// gates.
func TestProfilesTool_ReadOnlyMode_ListedReadsRunMutationsRefused(t *testing.T) {
	f := newProfilesToolFixture(t, func(cfg *config.Config) { cfg.ReadOnlyMode = true })
	assertGatedOps(t, f, "Operation not allowed in read-only mode")
	// upstream_servers is what read_only_mode removes from the registered set.
	assert.Empty(t, f.proxy.buildManagementTools(), "buildManagementTools() returns nothing under read_only_mode")
	assert.Contains(t, registeredToolNames(f.proxy.buildCallToolModeTools()), "profiles")
	assert.Contains(t, registeredToolNames(f.proxy.buildCodeExecModeTools()), "profiles")
	assert.Contains(t, toolMapNames(f.proxy.server.ListTools()), "profiles")
}

func TestProfilesTool_DisableManagement_Same(t *testing.T) {
	f := newProfilesToolFixture(t, func(cfg *config.Config) { cfg.DisableManagement = true })
	assertGatedOps(t, f, "Server management is disabled for security")
	assert.Empty(t, f.proxy.buildManagementTools())
	assert.Contains(t, registeredToolNames(f.proxy.buildCallToolModeTools()), "profiles")
}

// TestProfilesTool_ReadOnlyHotReload: the gates are read per call from the live
// config, so a reload applies without re-registering the tool.
func TestProfilesTool_ReadOnlyHotReload(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	mutate := map[string]any{"operation": "create", "name": "hot", "servers": []any{"github"}}
	f.ok(mutate)
	require.NotNil(t, f.profileByName("hot"))

	live := f.proxy.currentConfig()
	live.ReadOnlyMode = true
	body := f.refused(apiKeyCtx(), map[string]any{"operation": "delete", "name": "hot"})
	assert.Equal(t, "Operation not allowed in read-only mode", body["error"])
	f.ok(map[string]any{"operation": "get", "name": "hot"})
	live.ReadOnlyMode = false

	live.DisableManagement = true
	body = f.refused(apiKeyCtx(), map[string]any{"operation": "delete", "name": "hot"})
	assert.Equal(t, "Server management is disabled for security", body["error"])
	live.DisableManagement = false

	f.ok(map[string]any{"operation": "delete", "name": "hot"})
	assert.Nil(t, f.profileByName("hot"))
}

func registeredToolNames(tools []mcpserver.ServerTool) []string {
	names := make([]string, 0, len(tools))
	for _, st := range tools {
		names = append(names, st.Tool.Name)
	}
	return names
}

func toolMapNames[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}

// TestProfilesTool_HiddenCallRecordsAttributableRefusalsOnly: an administrator
// credential the profile hides the tool from gets a blocked record with
// block_reason=profile_management; a caller with nothing to attribute gets none.
func TestProfilesTool_HiddenCallRecordsAttributableRefusalsOnly(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	for _, row := range profilesVisibilityRows() {
		if row.name != "api_key, session on management_tools:false" {
			continue
		}
		res, err := f.proxy.handleProfiles(row.caller(f.proxy), mcpCallRequest(map[string]any{"operation": "list"}))
		require.NoError(t, err)
		require.True(t, res.IsError)
	}
	f.waitBlocked("", "profiles", profile.BlockReasonManagement)

	before := len(f.rowsFor("profiles"))
	for _, ctx := range []context.Context{unconfinedAnonymousCtx(), agentCtx([]string{"*"}, []string{auth.PermRead}, ""), clientCtx("cursor", "work-readonly", "locked")} {
		res, err := f.proxy.handleProfiles(ctx, mcpCallRequest(map[string]any{"operation": "list"}))
		require.NoError(t, err)
		require.True(t, res.IsError)
	}
	f.settle()
	assert.Len(t, f.rowsFor("profiles"), before, "nothing names who was refused, so nothing is recorded")
}

var _ = runtime.ErrEvaluatorUnavailable
