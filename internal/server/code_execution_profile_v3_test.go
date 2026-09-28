package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeExecution_ProfileV3HiddenAndRefused(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	proxy.currentConfig().EnableCodeExecution = true
	urlCtx := urlProfileCtx(proxy, "work-readonly")

	visible := proxy.filterProfileV3Tools(urlCtx, []mcp.Tool{
		{Name: "code_execution"},
		{Name: "call_tool_read"},
	})
	require.Equal(t, []string{"call_tool_read"}, profileV3ToolNames(visible))
	codeList, err := json.Marshal(proxy.codeExecServer.HandleMessage(urlCtx,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	require.NoError(t, err)
	var codeListEnvelope struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(codeList, &codeListEnvelope), string(codeList))
	for _, tool := range codeListEnvelope.Result.Tools {
		assert.NotEqual(t, "code_execution", tool.Name, "the /mcp/code instance must apply profile visibility")
	}

	result, err := proxy.handleCodeExecution(urlCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]interface{}{"code": "1 + 1"}},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	require.Equal(t, "unknown tool: code_execution", resultText(t, result))

	go rt.ActivityService().Start(rt.AppContext(), rt)
	startDeadline := time.Now().Add(5 * time.Second)
	for !rt.ActivityService().Started() && time.Now().Before(startDeadline) {
		time.Sleep(time.Millisecond)
	}
	require.True(t, rt.ActivityService().Started(), "activity service must subscribe before the policy decision is emitted")
	result, err = proxy.handleCodeExecution(urlCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]interface{}{"code": "1 + 1"}},
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		records, _, listErr := rt.StorageManager().ListActivities(storage.ActivityFilter{Limit: 50})
		require.NoError(t, listErr)
		for _, record := range records {
			if record.Type == storage.ActivityTypePolicyDecision && record.ToolName == "code_execution" {
				require.Equal(t, "blocked", record.Status)
				require.Equal(t, string(profile.BlockReasonCodeExecution), record.Metadata[storage.MetadataKeyBlockReason])
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("profile code_execution denial did not persist block_reason")
}

func TestCodeExecution_DanglingProfileHiddenFromDiscovery(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	proxy.currentConfig().EnableCodeExecution = true
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "stale", AllowedServers: []string{"*"},
		Permissions: []string{auth.PermRead}, ProfilePin: "removed-profile",
	})

	visible := proxy.filterProfileV3Tools(ctx, []mcp.Tool{{Name: "code_execution"}, {Name: "call_tool_read"}})
	require.Equal(t, []string{"call_tool_read"}, profileV3ToolNames(visible), "a dangling profile is deny-all and must not advertise code execution")

	result, err := proxy.handleCodeExecution(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]interface{}{"code": "1 + 1"}},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError, "a dangling profile must also refuse direct execution")
	require.Equal(t, "unknown tool: code_execution", resultText(t, result))
}

func TestCodeExecution_ProfileV3ImplicitDefaults(t *testing.T) {
	profiles := []config.ProfileConfig{
		{Name: "read-cap", Servers: []string{"github"}, MaxTier: config.ProfileTierRead},
		{Name: "write-cap", Servers: []string{"github"}, MaxTier: config.ProfileTierWrite},
		{Name: "destructive-cap", Servers: []string{"github"}, MaxTier: config.ProfileTierDestructive},
	}
	proxy, _ := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}}
		cfg.Profiles = profiles
		cfg.EnableCodeExecution = true
	})
	codeTool := mcp.Tool{Name: "code_execution"}
	for _, slug := range []string{"read-cap", "write-cap"} {
		t.Run(slug+" defaults to hidden", func(t *testing.T) {
			ctx := urlProfileCtx(proxy, slug)
			assert.Empty(t, proxy.filterProfileV3Tools(ctx, []mcp.Tool{codeTool}))
			result, err := proxy.handleCodeExecution(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"code": "1 + 1"}}})
			require.NoError(t, err)
			require.True(t, result.IsError)
			assert.Equal(t, "unknown tool: code_execution", resultText(t, result))
		})
	}
	t.Run("destructive cap follows the global gate", func(t *testing.T) {
		ctx := urlProfileCtx(proxy, "destructive-cap")
		assert.Equal(t, []string{"code_execution"}, profileV3ToolNames(proxy.filterProfileV3Tools(ctx, []mcp.Tool{codeTool})))
		result, err := proxy.handleCodeExecution(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"code": "1 + 1"}}})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.False(t, result.IsError)

		proxy.currentConfig().EnableCodeExecution = false
		result, err = proxy.handleCodeExecution(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"code": "1 + 1"}}})
		require.NoError(t, err)
		require.True(t, result.IsError)
		assert.Equal(t, config.CodeExecutionDisabledMessage, resultText(t, result))
	})
}

func TestCallToolRoutingMode_ProfileV3FilterAndEnforcement(t *testing.T) {
	proxy, rt := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) {
		cfg.EnableCodeExecution = true
	})
	indexEnforcementMatrixFixtureTools(t, proxy)
	up := startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"), readSpec("list_issues"))
	ctx := clientCtx("desktop", "work-readonly", "locked")

	listPayload, err := json.Marshal(proxy.callToolServer.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	require.NoError(t, err)
	var listEnvelope struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(listPayload, &listEnvelope), string(listPayload))
	listNames := make([]string, 0, len(listEnvelope.Result.Tools))
	for _, tool := range listEnvelope.Result.Tools {
		listNames = append(listNames, tool.Name)
	}
	assert.Contains(t, listNames, "retrieve_tools")
	assert.NotContains(t, listNames, "code_execution", "the /mcp/call instance must apply the profile tool filter")
	assert.NotContains(t, listNames, "upstream_servers", "management_tools=false must hide management tools on /mcp/call")

	callPayload, err := json.Marshal(proxy.callToolServer.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"retrieve_tools","arguments":{"query":"create_issue","limit":5}}}`)))
	require.NoError(t, err)
	var callEnvelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(callPayload, &callEnvelope), string(callPayload))
	require.NotEmpty(t, callEnvelope.Result.Content)
	var retrieved struct {
		Tools           []map[string]interface{} `json:"tools"`
		HiddenByProfile *int                     `json:"hidden_by_profile"`
	}
	require.NoError(t, json.Unmarshal([]byte(callEnvelope.Result.Content[0].Text), &retrieved))
	assert.Empty(t, retrieved.Tools)
	require.NotNil(t, retrieved.HiddenByProfile)
	assert.Equal(t, 1, *retrieved.HiddenByProfile)

	writePayload, err := json.Marshal(proxy.callToolServer.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"call_tool_write","arguments":{"name":"github:create_issue","args_json":"{}"}}}`)))
	require.NoError(t, err)
	var writeEnvelope struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(writePayload, &writeEnvelope), string(writePayload))
	require.True(t, writeEnvelope.Result.IsError)
	require.NotEmpty(t, writeEnvelope.Result.Content)
	assert.Equal(t, "blocked by profile: github:create_issue is a write tool; this profile allows read tools only", writeEnvelope.Result.Content[0].Text)
	assert.Empty(t, up.dispatched(), "the /mcp/call profile gate must refuse before upstream I/O")
}

func TestManagementTools_ProfileV3HiddenAndRefused(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	ctx := urlProfileCtx(proxy, "work-readonly")
	tools := []mcp.Tool{
		{Name: "upstream_servers"},
		{Name: "quarantine_security"},
		{Name: "call_tool_read"},
	}
	require.Equal(t, []string{"call_tool_read"}, profileV3ToolNames(proxy.filterProfileV3Tools(ctx, tools)))

	for _, name := range []string{"upstream_servers", "quarantine_security"} {
		result, err := proxy.callManagementToolForProfileTest(ctx, name)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, result.IsError)
		require.Equal(t, "unknown tool: "+name, resultText(t, result))
	}
}

func TestManagementTools_ConfinedAnonymousGetsClientOperationSet(t *testing.T) {
	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}, {Name: "filesystem", Enabled: true}, {Name: "outside", Enabled: true}}
		cfg.Profiles = enforcementMatrixProfiles()
		cfg.Profiles[1].ManagementTools = boolPtr(true)
		cfg.AnonymousProfile = "work-full"
	})
	startCountingUpstream(t, proxy, rt, "github", readSpec("list_issues"))
	startCountingUpstream(t, proxy, rt, "filesystem", readSpec("read_text_file"))
	startCountingUpstream(t, proxy, rt, "outside", readSpec("list_files"))
	tools := []mcp.Tool{{Name: "upstream_servers"}, {Name: "quarantine_security"}, {Name: "call_tool_read"}}
	require.Equal(t, []string{"upstream_servers", "call_tool_read"}, profileV3ToolNames(proxy.filterProfileV3Tools(anonCtx(), tools)))
	require.False(t, proxy.profileManagementToolHidden(anonCtx(), "upstream_servers"))
	require.True(t, proxy.profileManagementToolHidden(anonCtx(), "quarantine_security"))
	listed, err := proxy.handleListUpstreams(anonCtx())
	require.NoError(t, err)
	listText := resultText(t, listed)
	require.Contains(t, listText, "github")
	require.Contains(t, listText, "filesystem")
	require.NotContains(t, listText, "outside", "a confined anonymous list must obey the anonymous profile's server scope")

	assertManagementWritesDenied(t, proxy, anonCtx())
	quarantine, err := proxy.handleQuarantineSecurity(anonCtx(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]interface{}{}},
	})
	require.NoError(t, err)
	require.True(t, quarantine.IsError)
	require.Equal(t, "unknown tool: quarantine_security", resultText(t, quarantine))

	// The same confined profile must keep the client credential operation set:
	// it may list/tail but cannot mutate configuration or restart an upstream.
	client := withProfileRequestIndex(clientCtx("desktop", "work-full", "locked"), proxy.profileIndexFor(proxy.currentConfig()))
	require.Equal(t, []string{"upstream_servers", "call_tool_read"}, profileV3ToolNames(proxy.filterProfileV3Tools(client, tools)))
	listedForClient, err := proxy.handleListUpstreams(client)
	require.NoError(t, err)
	require.NotEmpty(t, resultText(t, listedForClient))
	assertManagementWritesDenied(t, proxy, client)

	// A profile-index publication gap must fail closed for scoped callers and
	// never panic or fall back to an administrator-shaped view.
	indexGap := withProfileRequestIndex(client, nil)
	require.True(t, proxy.profileManagementToolHidden(indexGap, "upstream_servers"))
	require.Equal(t, []string{"call_tool_read"}, profileV3ToolNames(proxy.filterProfileV3Tools(indexGap, tools)))
	gapList, err := proxy.handleListUpstreams(indexGap)
	require.NoError(t, err)
	require.NotContains(t, resultText(t, gapList), "github")

	// An agent pinned to a management-enabled profile keeps the established
	// read operation while its mutation refusals remain enforced.
	agent := withProfileRequestIndex(pinnedProfileCtx("work-full"), proxy.profileIndexFor(proxy.currentConfig()))
	listedForAgent, err := proxy.handleListUpstreams(agent)
	require.NoError(t, err)
	require.NotEmpty(t, resultText(t, listedForAgent))
	assertManagementWritesDenied(t, proxy, agent)

	// A legacy profile with no management_tools value is still confined for
	// anonymous callers and therefore exposes no management tool.
	legacy, _ := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{{Name: "github", Enabled: false}}
		cfg.Profiles = []config.ProfileConfig{{Name: "legacy", Servers: []string{"github"}}}
		cfg.AnonymousProfile = "legacy"
	})
	require.Equal(t, []string{"call_tool_read"}, profileV3ToolNames(legacy.filterProfileV3Tools(anonCtx(), tools)))
	_ = rt
}

func assertManagementWritesDenied(t *testing.T, proxy *MCPProxyServer, ctx context.Context) {
	t.Helper()
	before, err := proxy.storage.ListUpstreamServers()
	require.NoError(t, err)
	beforeNames := make([]string, len(before))
	for i, server := range before {
		beforeNames[i] = server.Name
	}
	for _, op := range []string{"add", "add_from_registry", "remove", "update", "patch", "restart", "enable", "disable", "refresh"} {
		result, err := proxy.handleUpstreamServers(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Arguments: map[string]interface{}{"operation": op, "name": "github"}},
		})
		require.NoError(t, err, op)
		require.True(t, result.IsError, op)
		require.Contains(t, resultText(t, result), "Agent tokens cannot perform", op)
	}
	after, err := proxy.storage.ListUpstreamServers()
	require.NoError(t, err)
	afterNames := make([]string, len(after))
	for i, server := range after {
		afterNames[i] = server.Name
	}
	require.Equal(t, beforeNames, afterNames, "denied management operations must not change the configured upstreams")
}

func (p *MCPProxyServer) callManagementToolForProfileTest(ctx context.Context, name string) (*mcp.CallToolResult, error) {
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{}}}
	if name == "upstream_servers" {
		return p.handleUpstreamServers(ctx, request)
	}
	return p.handleQuarantineSecurity(ctx, request)
}

func TestCodeExecution_ProfileV3NestedCallBlockedBeforeUpstream(t *testing.T) {
	proxy, rt := newProfilesV3Fixture(t)
	updated := *rt.Config()
	updated.Profiles = append([]config.ProfileConfig(nil), updated.Profiles...)
	updated.Profiles[0].CodeExecution = boolPtr(true)
	rt.UpdateConfig(&updated, "")
	up := startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"))

	result, err := proxy.handleCodeExecution(urlProfileCtx(proxy, "work-readonly"), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]interface{}{
			"code": `call_tool("github", "create_issue", {})`,
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "a denied nested call is returned as an error envelope to the script; result=%s", resultText(t, result))
	require.Contains(t, resultText(t, result), "blocked by profile: github:create_issue is a write tool; this profile allows read tools only")
	require.Empty(t, up.dispatched(), "nested profile denial must happen before upstream I/O")
	calls, total, listErr := rt.GetToolCalls(50, 0, nil)
	require.NoError(t, listErr)
	require.GreaterOrEqual(t, total, 1, "the parent execution record is persisted")
	var parentID string
	for _, call := range calls {
		if call.ServerName == "mcpproxy" && call.ToolName == "code_execution" {
			parentID = call.ID
		}
	}
	require.NotEmpty(t, parentID, "parent code_execution record is persisted")
	serverConfig, configErr := rt.StorageManager().GetUpstreamServer("github")
	require.NoError(t, configErr)
	serverCalls, callsErr := rt.StorageManager().GetServerToolCalls(storage.GenerateServerID(serverConfig), 1000)
	require.NoError(t, callsErr)
	var childParentID string
	for _, call := range serverCalls {
		if call.ServerName == "github" && call.ToolName == "create_issue" {
			childParentID = call.ParentCallID
		}
	}
	require.NotEmpty(t, childParentID, "profile-refused nested call is persisted")
	require.Equal(t, parentID, childParentID, "the refused child record links to its parent execution")
}

func profileV3ToolNames(tools []mcp.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}
