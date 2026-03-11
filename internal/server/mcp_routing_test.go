package server

import (
	"context"
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
