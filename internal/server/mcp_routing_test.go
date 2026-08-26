package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

func TestParseDirectToolName(t *testing.T) {
	tests := []struct {
		name       string
		directName string
		wantServer string
		wantTool   string
		wantOk     bool
	}{
		{
			name:       "simple tool name",
			directName: "github__create_issue",
			wantServer: "github",
			wantTool:   "create_issue",
			wantOk:     true,
		},
		{
			name:       "tool with underscores",
			directName: "my-server__my_tool_name",
			wantServer: "my-server",
			wantTool:   "my_tool_name",
			wantOk:     true,
		},
		{
			name:       "tool name contains double underscore",
			directName: "server__tool__with__double",
			wantServer: "server",
			wantTool:   "tool__with__double",
			wantOk:     true,
		},
		{
			name:       "no separator",
			directName: "noseparator",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
		{
			name:       "single underscore only",
			directName: "server_tool",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
		{
			name:       "empty string",
			directName: "",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
		{
			name:       "separator at start",
			directName: "__toolname",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
		{
			name:       "separator at end",
			directName: "server__",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
		{
			name:       "just separator",
			directName: "__",
			wantServer: "",
			wantTool:   "",
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, tool, ok := ParseDirectToolName(tt.directName)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.wantServer, server)
			assert.Equal(t, tt.wantTool, tool)
		})
	}
}

func TestFormatDirectToolName(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
		toolName   string
		want       string
	}{
		{
			name:       "simple names",
			serverName: "github",
			toolName:   "create_issue",
			want:       "github__create_issue",
		},
		{
			name:       "server with hyphens",
			serverName: "my-server",
			toolName:   "get_user",
			want:       "my-server__get_user",
		},
		{
			name:       "tool with underscores",
			serverName: "api",
			toolName:   "list_all_items",
			want:       "api__list_all_items",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDirectToolName(tt.serverName, tt.toolName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatDirectPromptName(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
		promptName string
		want       string
	}{
		{"simple names", "github", "setup-issue", "github__setup-issue"},
		{"server with hyphens", "my-server", "greeting", "my-server__greeting"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDirectPromptName(tt.serverName, tt.promptName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildAggregatedServerPrompts(t *testing.T) {
	builtin := mcpserver.ServerPrompt{
		Prompt: mcp.NewPrompt("setup-new-mcp-server"),
		Handler: func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Description: "builtin"}, nil
		},
	}

	upstreamPrompts := []mcp.Prompt{
		{Name: "server-a:greeting", Description: "hi"},
	}

	var gotCtx context.Context
	var gotName string
	var gotArgs map[string]string
	fakeGetPrompt := func(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
		gotCtx = ctx
		gotName = name
		gotArgs = args
		return &mcp.GetPromptResult{Description: "from upstream"}, nil
	}

	all := buildAggregatedServerPrompts([]mcpserver.ServerPrompt{builtin}, upstreamPrompts, fakeGetPrompt, zap.NewNop())

	require.Len(t, all, 2)
	assert.Equal(t, "setup-new-mcp-server", all[0].Prompt.Name)
	assert.Equal(t, "server-a__greeting", all[1].Prompt.Name)
	assert.Equal(t, "hi", all[1].Prompt.Description)

	ctx := context.Background()
	result, err := all[1].Handler(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{Arguments: map[string]string{"k": "v"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "from upstream", result.Description)
	assert.Equal(t, ctx, gotCtx)
	assert.Equal(t, "server-a:greeting", gotName)
	assert.Equal(t, map[string]string{"k": "v"}, gotArgs)
}

func TestBuildAggregatedServerPrompts_SkipsMalformedNames(t *testing.T) {
	upstreamPrompts := []mcp.Prompt{{Name: "no-colon-here"}}
	all := buildAggregatedServerPrompts(nil, upstreamPrompts, nil, zap.NewNop())
	assert.Empty(t, all)
}

func TestDirectToolNameRoundTrip(t *testing.T) {
	// Test that formatting and parsing are inverse operations
	testCases := []struct {
		serverName string
		toolName   string
	}{
		{"github", "create_issue"},
		{"my-server", "list_repos"},
		{"api", "search_files"},
		{"db-server", "query_users_table"},
	}

	for _, tc := range testCases {
		formatted := FormatDirectToolName(tc.serverName, tc.toolName)
		parsedServer, parsedTool, ok := ParseDirectToolName(formatted)
		assert.True(t, ok, "should parse successfully for %s/%s", tc.serverName, tc.toolName)
		assert.Equal(t, tc.serverName, parsedServer)
		assert.Equal(t, tc.toolName, parsedTool)
	}
}

func TestDirectModeToolSeparator(t *testing.T) {
	assert.Equal(t, "__", DirectModeToolSeparator)
}

func TestGetMCPServerForMode(t *testing.T) {
	// Create a minimal MCPProxyServer with mock servers
	proxy := &MCPProxyServer{}

	// Create distinct server instances so we can verify identity
	mainServer := mcpserver.NewMCPServer("main", "1.0.0", mcpserver.WithToolCapabilities(true))
	directServer := mcpserver.NewMCPServer("direct", "1.0.0", mcpserver.WithToolCapabilities(true))
	codeExecServer := mcpserver.NewMCPServer("code_exec", "1.0.0", mcpserver.WithToolCapabilities(true))
	callToolServer := mcpserver.NewMCPServer("call_tool", "1.0.0", mcpserver.WithToolCapabilities(true))

	proxy.server = mainServer
	proxy.directServer = directServer
	proxy.codeExecServer = codeExecServer
	proxy.callToolServer = callToolServer

	tests := []struct {
		name     string
		mode     string
		expected *mcpserver.MCPServer
	}{
		{
			name:     "retrieve_tools returns call tool server",
			mode:     "retrieve_tools",
			expected: callToolServer,
		},
		{
			name:     "direct returns direct server",
			mode:     "direct",
			expected: directServer,
		},
		{
			name:     "code_execution returns code exec server",
			mode:     "code_execution",
			expected: codeExecServer,
		},
		{
			name:     "empty mode returns main server",
			mode:     "",
			expected: mainServer,
		},
		{
			name:     "unknown mode returns main server",
			mode:     "unknown",
			expected: mainServer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := proxy.GetMCPServerForMode(tt.mode)
			assert.Same(t, tt.expected, got)
		})
	}
}

func TestGetMCPServerForMode_NilFallback(t *testing.T) {
	// When routing mode servers are nil, should fall back to main server
	mainServer := mcpserver.NewMCPServer("main", "1.0.0", mcpserver.WithToolCapabilities(true))
	proxy := &MCPProxyServer{
		server: mainServer,
	}

	assert.Same(t, mainServer, proxy.GetMCPServerForMode("direct"))
	assert.Same(t, mainServer, proxy.GetMCPServerForMode("code_execution"))
	assert.Same(t, mainServer, proxy.GetMCPServerForMode("retrieve_tools"))
}

func TestDirectModeHandler_PermissionDenied(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	proxy := &MCPProxyServer{
		logger: logger,
		config: &config.Config{},
	}

	// Create a handler for a read-only tool
	readOnlyHint := true
	annotations := &config.ToolAnnotations{
		ReadOnlyHint: &readOnlyHint,
	}
	handler := proxy.makeDirectModeHandler("github", "list_repos", annotations)

	// Create a context with agent token that only has write permission (no read)
	agentCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-agent",
		AllowedServers: []string{"github"},
		Permissions:    []string{"write"}, // Only write, no read
	})

	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "github__list_repos",
		},
	}

	result, err := handler(agentCtx, request)
	require.NoError(t, err) // Handler returns errors as tool results, not Go errors
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Permission denied")
}

func TestDirectModeHandler_ServerAccessDenied(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	proxy := &MCPProxyServer{
		logger: logger,
		config: &config.Config{},
	}

	handler := proxy.makeDirectModeHandler("gitlab", "list_repos", nil)

	// Create a context with agent token that only has access to github
	agentCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-agent",
		AllowedServers: []string{"github"}, // Only github, not gitlab
		Permissions:    []string{"read"},
	})

	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "gitlab__list_repos",
		},
	}

	result, err := handler(agentCtx, request)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Access denied")
}

func TestDirectModeHandler_AgentWithCorrectPermissions(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	proxy := &MCPProxyServer{
		logger: logger,
		config: &config.Config{},
	}

	// A read-only tool requires "read" permission
	readOnlyHint := true
	annotations := &config.ToolAnnotations{
		ReadOnlyHint: &readOnlyHint,
	}
	handler := proxy.makeDirectModeHandler("github", "list_repos", annotations)

	// Agent with read permission and github access should pass auth checks
	agentCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-agent",
		AllowedServers: []string{"github"},
		Permissions:    []string{"read"},
	})

	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "github__list_repos",
		},
	}

	// Will panic due to nil upstreamManager, but we use recover to verify
	// that auth checks passed (if it had failed at auth, result would be returned cleanly)
	func() {
		defer func() {
			r := recover()
			// If we reach here, the auth check passed and we hit the upstream call
			// which panics due to nil manager. This is expected behavior.
			assert.NotNil(t, r, "should panic at upstream call, proving auth checks passed")
		}()
		handler(agentCtx, request)
	}()
}

func TestDirectModeHandler_DestructiveToolNeedsDestructivePermission(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	proxy := &MCPProxyServer{
		logger: logger,
		config: &config.Config{},
	}

	destructiveHint := true
	annotations := &config.ToolAnnotations{
		DestructiveHint: &destructiveHint,
	}
	handler := proxy.makeDirectModeHandler("github", "delete_repo", annotations)

	// Agent with only read+write but no destructive permission
	agentCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-agent",
		AllowedServers: []string{"github"},
		Permissions:    []string{"read", "write"},
	})

	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "github__delete_repo",
		},
	}

	result, err := handler(agentCtx, request)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Permission denied")
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "destructive")
}

func TestRequiredPermissionForDirectTool_MapsAnnotationsToAuthPermissions(t *testing.T) {
	readOnly := true
	write := false
	destructive := true

	tests := []struct {
		name        string
		annotations *config.ToolAnnotations
		want        string
	}{
		{
			name: "nil annotations default to read",
			want: auth.PermRead,
		},
		{
			name: "read only hint maps to read",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: &readOnly,
			},
			want: auth.PermRead,
		},
		{
			name: "read only false maps to write",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: &write,
			},
			want: auth.PermWrite,
		},
		{
			name: "destructive hint maps to destructive",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &destructive,
			},
			want: auth.PermDestructive,
		},
		{
			name: "destructive hint takes precedence over read only hint",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint:    &readOnly,
				DestructiveHint: &destructive,
			},
			want: auth.PermDestructive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, requiredPermissionForDirectTool(tt.annotations))
		})
	}
}

func TestSetDirectToolPermissions_DefensivelyCopiesMap(t *testing.T) {
	proxy := &MCPProxyServer{}
	toolName := FormatDirectToolName("github", "get_issue")
	perms := map[string]string{
		toolName: auth.PermRead,
	}

	publishPermsCatalog(proxy, perms)
	// Mutating the caller's map after publication must not reach the published
	// snapshot. The catalog is immutable by construction — the entries are built
	// from the map, not backed by it — where the retired directToolPermissions
	// map had to defensively copy to get the same property.
	perms[toolName] = auth.PermDestructive

	entry, decision := proxy.resolveDirectTool(toolName)
	require.Equal(t, directResolveFound, decision)
	require.NotNil(t, entry)
	assert.Equal(t, auth.PermRead, entry.RequiredPermission)
}

func TestFilterDirectModeToolsForAuth_DoesNotMutateInputSlice(t *testing.T) {
	proxy := &MCPProxyServer{}
	allowed := FormatDirectToolName("github", "get_issue")
	denied := FormatDirectToolName("gitlab", "get_issue")
	tools := []mcp.Tool{
		{Name: allowed},
		{Name: denied},
	}
	original := append([]mcp.Tool(nil), tools...)

	publishPermsCatalog(proxy, map[string]string{
		allowed: auth.PermRead,
		denied:  auth.PermRead,
	})

	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-agent",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})

	filtered := proxy.filterDirectModeToolsForAuth(ctx, tools)

	assert.Equal(t, []string{allowed}, directToolNamesForTest(filtered))
	assert.Equal(t, original, tools)
}

func TestFilterDirectModeToolsForAuth_AgentServerAndPermissionScope(t *testing.T) {
	proxy := &MCPProxyServer{}

	githubRead := FormatDirectToolName("github", "get_issue")
	githubWrite := FormatDirectToolName("github", "create_issue")
	githubDestroy := FormatDirectToolName("github", "delete_repo")
	gitlabRead := FormatDirectToolName("gitlab", "get_issue")

	publishPermsCatalog(proxy, map[string]string{
		githubRead:    auth.PermRead,
		githubWrite:   auth.PermWrite,
		githubDestroy: auth.PermDestructive,
		gitlabRead:    auth.PermRead,
	})

	tools := []mcp.Tool{
		{Name: githubRead},
		{Name: githubWrite},
		{Name: githubDestroy},
		{Name: gitlabRead},
	}

	agentCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-agent",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	})

	filtered := proxy.filterDirectModeToolsForAuth(agentCtx, tools)

	assert.Equal(t, []string{githubRead, githubWrite}, directToolNamesForTest(filtered))
}

func TestFilterDirectModeToolsForAuth_NonAgentUnchanged(t *testing.T) {
	proxy := &MCPProxyServer{}
	tools := []mcp.Tool{
		{Name: FormatDirectToolName("github", "get_issue")},
		{Name: FormatDirectToolName("gitlab", "get_issue")},
	}

	assert.Equal(t, tools, proxy.filterDirectModeToolsForAuth(context.Background(), tools))

	adminCtx := auth.WithAuthContext(context.Background(), auth.AdminContext())
	assert.Equal(t, tools, proxy.filterDirectModeToolsForAuth(adminCtx, tools))
}

func TestFilterDirectModeToolsForAuth_FailsClosedOnMissingPermissionMetadata(t *testing.T) {
	proxy := &MCPProxyServer{}

	visible := FormatDirectToolName("github", "get_issue")
	missing := FormatDirectToolName("github", "unknown")
	publishPermsCatalog(proxy, map[string]string{
		visible: auth.PermRead,
	})

	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-agent",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})

	filtered := proxy.filterDirectModeToolsForAuth(ctx, []mcp.Tool{
		{Name: visible},
		{Name: missing},
	})

	assert.Equal(t, []string{visible}, directToolNamesForTest(filtered))
}

func TestFilterDirectModeToolsForAuth_KeepsNonDirectTools(t *testing.T) {
	proxy := &MCPProxyServer{}

	direct := FormatDirectToolName("github", "get_issue")
	nonDirect := "retrieve_tools"
	publishPermsCatalog(proxy, map[string]string{
		direct: auth.PermRead,
	})

	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-agent",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})

	filtered := proxy.filterDirectModeToolsForAuth(ctx, []mcp.Tool{
		{Name: direct},
		{Name: nonDirect},
	})

	assert.Equal(t, []string{direct, nonDirect}, directToolNamesForTest(filtered))
}

func directToolNamesForTest(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestDirectModeHandler_NoAuthContext(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	proxy := &MCPProxyServer{
		logger: logger,
		config: &config.Config{},
	}

	handler := proxy.makeDirectModeHandler("github", "list_repos", nil)

	// No auth context in context - should pass auth checks (backward compatible)
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "github__list_repos",
		},
	}

	// Will panic due to nil upstreamManager, proving auth checks passed
	func() {
		defer func() {
			r := recover()
			assert.NotNil(t, r, "should panic at upstream call, proving auth checks passed")
		}()
		handler(context.Background(), request)
	}()
}

// TestRetrieveToolsInstructions_CodeExecutionMode verifies that handleRetrieveToolsWithMode
// returns code_execution-specific usage_instructions when called with RoutingModeCodeExecution.
func TestRetrieveToolsInstructions_CodeExecutionMode(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	request := mcp.CallToolRequest{}
	request.Params.Name = "retrieve_tools"
	request.Params.Arguments = map[string]interface{}{
		"query": "test query",
	}

	result, err := proxy.handleRetrieveToolsWithMode(context.Background(), request, config.RoutingModeCodeExecution)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	// Parse the response JSON to extract usage_instructions
	responseText := result.Content[0].(mcp.TextContent).Text
	var response map[string]interface{}
	err = json.Unmarshal([]byte(responseText), &response)
	require.NoError(t, err, "response should be valid JSON")

	instructions, ok := response["usage_instructions"].(string)
	require.True(t, ok, "usage_instructions should be a string")

	// Code execution mode: should mention code_execution and call_tool()
	assert.Contains(t, instructions, "code_execution",
		"code_execution mode should mention 'code_execution' tool")
	assert.Contains(t, instructions, "call_tool(",
		"code_execution mode should mention call_tool() JavaScript function")

	// Code execution mode: should NOT recommend call_tool_read/write/destructive as tools to use.
	// Note: the instructions may mention them in a "Do NOT use" warning, which is acceptable.
	// What they must NOT contain is the retrieve_tools-mode decision rules that tell the LLM
	// to use these as tool variants.
	assert.NotContains(t, instructions, "DECISION RULES BY TOOL NAME",
		"code_execution mode should NOT contain call_tool variant decision rules")
	assert.NotContains(t, instructions, "(1) READ (call_tool_read)",
		"code_execution mode should NOT recommend call_tool_read as a variant")
	assert.NotContains(t, instructions, "(2) WRITE (call_tool_write)",
		"code_execution mode should NOT recommend call_tool_write as a variant")
	assert.NotContains(t, instructions, "(3) DESTRUCTIVE (call_tool_destructive)",
		"code_execution mode should NOT recommend call_tool_destructive as a variant")
}

// TestRetrieveToolsInstructions_RetrieveToolsMode verifies that handleRetrieveToolsWithMode
// returns call_tool_*-specific usage_instructions when called with RoutingModeRetrieveTools.
func TestRetrieveToolsInstructions_RetrieveToolsMode(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	request := mcp.CallToolRequest{}
	request.Params.Name = "retrieve_tools"
	request.Params.Arguments = map[string]interface{}{
		"query": "test query",
	}

	result, err := proxy.handleRetrieveToolsWithMode(context.Background(), request, config.RoutingModeRetrieveTools)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	// Parse the response JSON to extract usage_instructions
	responseText := result.Content[0].(mcp.TextContent).Text
	var response map[string]interface{}
	err = json.Unmarshal([]byte(responseText), &response)
	require.NoError(t, err, "response should be valid JSON")

	instructions, ok := response["usage_instructions"].(string)
	require.True(t, ok, "usage_instructions should be a string")

	// Retrieve tools mode: should mention call_tool_read/write/destructive
	assert.Contains(t, instructions, "call_tool_read",
		"retrieve_tools mode should mention call_tool_read")
	assert.Contains(t, instructions, "call_tool_write",
		"retrieve_tools mode should mention call_tool_write")
	assert.Contains(t, instructions, "call_tool_destructive",
		"retrieve_tools mode should mention call_tool_destructive")
	assert.Contains(t, instructions, "INTENT TRACKING",
		"retrieve_tools mode should mention intent tracking")
}

// TestRetrieveToolsInstructions_DefaultMode verifies that handleRetrieveToolsWithMode
// with empty routingMode returns the same instructions as retrieve_tools mode.
func TestRetrieveToolsInstructions_DefaultMode(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	request := mcp.CallToolRequest{}
	request.Params.Name = "retrieve_tools"
	request.Params.Arguments = map[string]interface{}{
		"query": "test query",
	}

	result, err := proxy.handleRetrieveToolsWithMode(context.Background(), request, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	// Parse the response JSON to extract usage_instructions
	responseText := result.Content[0].(mcp.TextContent).Text
	var response map[string]interface{}
	err = json.Unmarshal([]byte(responseText), &response)
	require.NoError(t, err, "response should be valid JSON")

	instructions, ok := response["usage_instructions"].(string)
	require.True(t, ok, "usage_instructions should be a string")

	// Default mode should use the same instructions as retrieve_tools mode
	assert.Contains(t, instructions, "call_tool_read",
		"default mode should contain call_tool_read instructions")
	assert.Contains(t, instructions, "call_tool_write",
		"default mode should contain call_tool_write instructions")
}

// TestHandleRetrieveToolsForMode_ClosureReturnsDifferentInstructions verifies that
// handleRetrieveToolsForMode creates closures that produce different instructions per mode.
func TestHandleRetrieveToolsForMode_ClosureReturnsDifferentInstructions(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	request := mcp.CallToolRequest{}
	request.Params.Name = "retrieve_tools"
	request.Params.Arguments = map[string]interface{}{
		"query": "search for tools",
	}

	// Get handler for code_execution mode
	codeExecHandler := proxy.handleRetrieveToolsForMode(config.RoutingModeCodeExecution)
	codeExecResult, err := codeExecHandler(context.Background(), request)
	require.NoError(t, err)

	// Get handler for retrieve_tools mode
	retrieveHandler := proxy.handleRetrieveToolsForMode(config.RoutingModeRetrieveTools)
	retrieveResult, err := retrieveHandler(context.Background(), request)
	require.NoError(t, err)

	// Parse both results
	var codeExecResponse, retrieveResponse map[string]interface{}
	err = json.Unmarshal([]byte(codeExecResult.Content[0].(mcp.TextContent).Text), &codeExecResponse)
	require.NoError(t, err)
	err = json.Unmarshal([]byte(retrieveResult.Content[0].(mcp.TextContent).Text), &retrieveResponse)
	require.NoError(t, err)

	codeExecInstructions := codeExecResponse["usage_instructions"].(string)
	retrieveInstructions := retrieveResponse["usage_instructions"].(string)

	// They should be different
	assert.NotEqual(t, codeExecInstructions, retrieveInstructions,
		"code_execution and retrieve_tools modes should produce different usage_instructions")

	// Code exec should mention code_execution, retrieve should mention call_tool_read
	assert.Contains(t, codeExecInstructions, "code_execution")
	assert.Contains(t, retrieveInstructions, "call_tool_read")
}

func TestSafeTruncateBytes(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		limit int
		want  int
	}{
		{"ascii cut mid-string", "hello world", 5, 5},
		{"limit beyond length", "hi", 10, 2},
		{"limit equals length", "hello", 5, 5},
		{"zero limit", "hello", 0, 0},
		{"negative limit", "hello", -1, 0},
		// "héllo": 'é' is 2 bytes (0xC3 0xA9) at indices 1-2. A raw cut at 2
		// would split it; safeTruncateBytes backs up to 1.
		{"cut inside 2-byte rune backs up", "héllo", 2, 1},
		{"cut at 2-byte rune end is kept", "héllo", 3, 3},
		// "😀x": emoji is 4 bytes (indices 0-3). Cuts inside it back up to 0.
		{"cut inside 4-byte rune backs up to 0", "😀x", 2, 0},
		{"cut at emoji boundary kept", "😀x", 4, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeTruncateBytes(tt.s, tt.limit)
			if got != tt.want {
				t.Fatalf("safeTruncateBytes(%q, %d) = %d, want %d", tt.s, tt.limit, got, tt.want)
			}
			// The truncated prefix must always be valid UTF-8.
			if !utf8.ValidString(tt.s[:got]) {
				t.Errorf("prefix %q is not valid UTF-8", tt.s[:got])
			}
		})
	}
}

// =============================================================================
// Spec 090 (T016): direct-routing blocks carry a request id
// =============================================================================

// Direct mode reads its request id from the transport context, which is empty
// for anything that did not arrive over HTTP with a correlation header. A block
// emitted with an empty id is indistinguishable from a legacy record, so the
// handler has to fall back to a minted one.
func TestDirectModeHandler_CallabilityBlock_CarriesRequestID(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{
		// Disabled, so directToolCallabilityBlock refuses the call.
		{Name: "github", Enabled: false},
	})
	probe := watchPolicyDecisions(t, rt)

	handler := proxy.makeDirectModeHandler("github", "list_repos", nil)
	result, err := handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "github__list_repos"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError, "a disabled server must not be callable in direct mode")

	payload := probe.awaitOne(t)
	assert.Equal(t, "blocked", payload["decision"])
	assert.Equal(t, "github", payload["server_name"])
	assert.Equal(t, "list_repos", payload["tool_name"])
	requestIDOf(t, payload)
}

// When the transport DID supply a correlation id, that id must be used as-is —
// the fallback is a fallback, not a replacement, or the block would stop
// correlating with the HTTP access log it belongs to.
func TestDirectModeHandler_CallabilityBlock_PrefersContextRequestID(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: false},
	})
	probe := watchPolicyDecisions(t, rt)

	ctx := reqcontext.WithRequestID(context.Background(), "req-from-transport")
	handler := proxy.makeDirectModeHandler("github", "list_repos", nil)
	_, err := handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "github__list_repos"},
	})
	require.NoError(t, err)

	payload := probe.awaitOne(t)
	assert.Equal(t, "req-from-transport", requestIDOf(t, payload))
}

// =============================================================================
// RefreshPrompts (prompt aggregation)
// =============================================================================

// newTestRefreshPromptsUpstream builds a real in-process MCP server that
// advertises Capabilities.Prompts and serves one "greeting" prompt.
func newTestRefreshPromptsUpstream(t *testing.T) *mcpserver.MCPServer {
	t.Helper()
	srv := mcpserver.NewMCPServer("test-upstream", "0.0.1",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithPromptCapabilities(true),
	)
	srv.AddPrompt(
		mcp.NewPrompt("greeting", mcp.WithPromptDescription("Say hello")),
		func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{
				Messages: []mcp.PromptMessage{
					{Role: mcp.RoleAssistant, Content: mcp.TextContent{Type: "text", Text: "hello"}},
				},
			}, nil
		},
	)
	return srv
}

func TestRefreshPrompts_Disabled_NoOp(t *testing.T) {
	// createTestProxyWithRuntime uses config.DefaultConfig(), which has
	// EnablePrompts: true, so registerPrompts() already ran at construction.
	// Clear that baseline so this test can prove RefreshPrompts adds nothing
	// once prompts are (re-)disabled, rather than merely tolerating leftovers.
	proxy, _ := createTestProxyWithRuntime(t, nil)
	existing := proxy.server.ListPrompts()
	names := make([]string, 0, len(existing))
	for name := range existing {
		names = append(names, name)
	}
	proxy.server.DeletePrompts(names...)

	proxy.config.EnablePrompts = false
	proxy.RefreshPrompts()

	assert.Empty(t, proxy.server.ListPrompts(), "disabled prompts must not populate the server's prompt set")
}

func TestRefreshPrompts_AggregatesBuiltinsAndUpstream(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	proxy, _ := createTestProxyWithRuntime(t, nil)
	proxy.config.EnablePrompts = true
	proxy.config.AggregateUpstreamPrompts = true // opt in to upstream aggregation
	qOff := false
	proxy.config.QuarantineEnabled = &qOff // spec 100: these tests verify aggregation, not the rug-pull baseline

	upstreamSrv := newTestRefreshPromptsUpstream(t)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstreamSrv)
	t.Cleanup(testServer.Close)

	um := upstream.NewManager(zap.NewNop(), proxy.config, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { um.DisconnectAll() })
	require.NoError(t, um.AddServerConfig("srv-a", &config.ServerConfig{
		Name:     "server-a",
		Protocol: "streamable-http",
		URL:      testServer.URL,
		Enabled:  true,
	}))
	client, ok := um.GetClient("srv-a")
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))
	proxy.upstreamManager = um

	proxy.RefreshPrompts()

	prompts := proxy.server.ListPrompts()
	require.Contains(t, prompts, "setup-new-mcp-server", "built-in prompts must still be registered")
	require.Contains(t, prompts, "server-a__greeting", "aggregated upstream prompt must be registered under its direct name")
}

// TestRefreshPrompts_RugPullBaseline_WithholdsFirstSeen (spec 100) proves the
// full RefreshPrompts path withholds a first-seen prompt on a quarantine-
// enforced server, and that approving it registers it on the next refresh.
func TestRefreshPrompts_RugPullBaseline_WithholdsFirstSeen(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	proxy, _ := createTestProxyWithRuntime(t, []*config.ServerConfig{
		// Server present in config as manual trust + quarantine on (the default).
		{Name: "server-a", Protocol: "streamable-http", Enabled: true, TrustMode: string(config.TrustModeManual)},
	})
	proxy.config.EnablePrompts = true
	proxy.config.AggregateUpstreamPrompts = true

	upstreamSrv := newTestRefreshPromptsUpstream(t)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstreamSrv)
	t.Cleanup(testServer.Close)

	um := upstream.NewManager(zap.NewNop(), proxy.config, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { um.DisconnectAll() })
	require.NoError(t, um.AddServerConfig("srv-a", &config.ServerConfig{
		Name: "server-a", Protocol: "streamable-http", URL: testServer.URL, Enabled: true,
	}))
	client, ok := um.GetClient("srv-a")
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))
	proxy.upstreamManager = um

	proxy.RefreshPrompts()
	prompts := proxy.server.ListPrompts()
	require.Contains(t, prompts, "setup-new-mcp-server", "built-ins are always registered")
	require.NotContains(t, prompts, "server-a__greeting", "a first-seen prompt on a manual server is withheld (rug-pull baseline)")

	// Approve it → it registers on the triggered refresh.
	require.NoError(t, proxy.ApprovePrompt("server-a", "greeting", "tester"))
	prompts = proxy.server.ListPrompts()
	require.Contains(t, prompts, "server-a__greeting", "an approved prompt is registered")
}

// TestRefreshPrompts_AggregationDisabled_BuiltinsOnly verifies the default
// safe posture (PR #973 review): with EnablePrompts on but the opt-in
// aggregate_upstream_prompts flag off, RefreshPrompts serves ONLY the built-ins
// and never touches upstream servers.
func TestRefreshPrompts_AggregationDisabled_BuiltinsOnly(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	proxy, _ := createTestProxyWithRuntime(t, nil)
	proxy.config.EnablePrompts = true
	proxy.config.AggregateUpstreamPrompts = false // the default — safe posture

	upstreamSrv := newTestRefreshPromptsUpstream(t)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstreamSrv)
	t.Cleanup(testServer.Close)

	um := upstream.NewManager(zap.NewNop(), proxy.config, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { um.DisconnectAll() })
	require.NoError(t, um.AddServerConfig("srv-a", &config.ServerConfig{
		Name:     "server-a",
		Protocol: "streamable-http",
		URL:      testServer.URL,
		Enabled:  true,
	}))
	client, ok := um.GetClient("srv-a")
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))
	proxy.upstreamManager = um

	proxy.RefreshPrompts()

	prompts := proxy.server.ListPrompts()
	assert.Contains(t, prompts, "setup-new-mcp-server", "built-ins still register when aggregation is off")
	assert.NotContains(t, prompts, "server-a__greeting", "upstream prompt must NOT be aggregated when the opt-in flag is off")
	assert.Len(t, prompts, 2, "only the two built-in prompts are present")
}

// TestRefreshPrompts_ReadsLiveAggregateFlag is the F4 regression: RefreshPrompts
// must honor the LIVE config snapshot (currentConfig() -> runtime), not the
// construction-time p.config, so an aggregate_upstream_prompts toggle takes
// effect on hot-reload without a restart. We deliberately give proxy.config a
// stale snapshot that DISAGREES with the live one; if RefreshPrompts read the
// boot snapshot it would skip aggregation and the assertion would fail.
func TestRefreshPrompts_ReadsLiveAggregateFlag(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	proxy, rt := createTestProxyWithRuntime(t, nil)

	// Live snapshot (what currentConfig() -> rt.Config() returns) OPTS IN.
	live := rt.Config()
	live.EnablePrompts = true
	live.AggregateUpstreamPrompts = true
	qOff := false
	live.QuarantineEnabled = &qOff // spec 100: verify aggregation, not the rug-pull baseline

	// Construction-time snapshot DISAGREES (aggregation off). If RefreshPrompts
	// read p.config it would skip aggregation — the assertion below would fail.
	boot := *live
	boot.AggregateUpstreamPrompts = false
	proxy.config = &boot

	upstreamSrv := newTestRefreshPromptsUpstream(t)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstreamSrv)
	t.Cleanup(testServer.Close)

	um := upstream.NewManager(zap.NewNop(), live, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { um.DisconnectAll() })
	require.NoError(t, um.AddServerConfig("srv-a", &config.ServerConfig{
		Name:     "server-a",
		Protocol: "streamable-http",
		URL:      testServer.URL,
		Enabled:  true,
	}))
	client, ok := um.GetClient("srv-a")
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))
	proxy.upstreamManager = um

	proxy.RefreshPrompts()

	prompts := proxy.server.ListPrompts()
	require.Contains(t, prompts, "server-a__greeting",
		"RefreshPrompts must honor the LIVE aggregate flag, not the boot snapshot (F4)")
}

func TestRefreshPrompts_NoUpstreamClients_RegistersOnlyBuiltins(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, nil)
	proxy.config.EnablePrompts = true
	proxy.upstreamManager = upstream.NewManager(zap.NewNop(), proxy.config, nil, secret.NewResolver(), nil)

	proxy.RefreshPrompts()

	prompts := proxy.server.ListPrompts()
	assert.Contains(t, prompts, "setup-new-mcp-server", "built-ins still register when there are no upstream prompts")
	assert.Len(t, prompts, 2, "only the two built-in prompts should be present")
}

// TestRefreshPrompts_PopulatesRoutingModeServers is the regression test for
// PR #973 review's P1 finding: prompts were only ever set on p.server, but
// /mcp is served via GetMCPServerForMode(cfg.RoutingMode), which after
// config.Validate() normalizes routing_mode to retrieve_tools almost never
// resolves back to p.server — so the aggregated prompts feature was
// unreachable over Streamable HTTP in every non-default routing mode.
func TestRefreshPrompts_PopulatesRoutingModeServers(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	proxy, _ := createTestProxyWithRuntime(t, nil)
	proxy.config.EnablePrompts = true
	proxy.config.AggregateUpstreamPrompts = true // opt in to upstream aggregation
	qOff := false
	proxy.config.QuarantineEnabled = &qOff // spec 100: these tests verify aggregation, not the rug-pull baseline

	upstreamSrv := newTestRefreshPromptsUpstream(t)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstreamSrv)
	t.Cleanup(testServer.Close)

	um := upstream.NewManager(zap.NewNop(), proxy.config, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { um.DisconnectAll() })
	require.NoError(t, um.AddServerConfig("srv-a", &config.ServerConfig{
		Name:     "server-a",
		Protocol: "streamable-http",
		URL:      testServer.URL,
		Enabled:  true,
	}))
	client, ok := um.GetClient("srv-a")
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))
	proxy.upstreamManager = um

	proxy.RefreshPrompts()

	for _, mode := range []string{config.RoutingModeDirect, config.RoutingModeCodeExecution, config.RoutingModeRetrieveTools} {
		srv := proxy.GetMCPServerForMode(mode)
		require.NotNil(t, srv, "routing mode %s must have a server instance", mode)
		prompts := srv.ListPrompts()
		assert.Contains(t, prompts, "setup-new-mcp-server", "mode %s: built-in prompts must be registered", mode)
		assert.Contains(t, prompts, "server-a__greeting", "mode %s: aggregated upstream prompt must be registered", mode)
	}
}

// TestBuildAggregatedServerPrompts_CollisionKeepsFirst covers Finding F7: two
// distinct (server,prompt) pairs that flatten to the same "server__prompt"
// display name must be resolved deterministically (first-writer-wins), not
// silently overwritten, and the drop must be logged.
func TestBuildAggregatedServerPrompts_CollisionKeepsFirst(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	// "gh" + "issue__create" and "gh__issue" + "create" both flatten to
	// "gh__issue__create".
	upstreamPrompts := []mcp.Prompt{
		{Name: "gh:issue__create", Description: "first"},
		{Name: "gh__issue:create", Description: "second (collides)"},
	}
	fakeGetPrompt := func(_ context.Context, _ string, _ map[string]string) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{}, nil
	}

	all := buildAggregatedServerPrompts(nil, upstreamPrompts, fakeGetPrompt, logger)

	names := make([]string, len(all))
	for i, p := range all {
		names[i] = p.Prompt.Name
	}
	assert.Equal(t, []string{"gh__issue__create"}, names, "colliding display name must appear once (first kept)")
	require.Equal(t, 1, logs.FilterMessage("dropping upstream prompt: display-name collision (kept first)").Len())
}
