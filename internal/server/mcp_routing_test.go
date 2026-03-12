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
