package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

// TestEnvironment holds all test dependencies
type TestEnvironment struct {
	t           *testing.T
	tempDir     string
	proxyServer *Server
	proxyAddr   string
	mockServers map[string]*MockUpstreamServer
	logger      *zap.Logger
	cleanup     func()
}

// MockUpstreamServer implements a mock MCP server for testing
type MockUpstreamServer struct {
	server     *mcpserver.MCPServer
	tools      []mcp.Tool
	addr       string
	httpServer *http.Server
	stopFunc   func() error
}

// NewTestEnvironment creates a complete test environment
func NewTestEnvironment(t *testing.T) *TestEnvironment {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "mcpproxy-e2e-*")
	require.NoError(t, err)

	// Create logger
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	env := &TestEnvironment{
		t:           t,
		tempDir:     tempDir,
		mockServers: make(map[string]*MockUpstreamServer),
		logger:      logger,
	}

	// Create data directory
	dataDir := filepath.Join(tempDir, "data")
	err = os.MkdirAll(dataDir, 0755)
	require.NoError(t, err)

	// Find available port for test server
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	testPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Create proxy server with test config
	cfg := &config.Config{
		DataDir:           dataDir,
		Listen:            fmt.Sprintf(":%d", testPort),
		ToolResponseLimit: 10000,
		DisableManagement: false,
		ReadOnlyMode:      false,
		AllowServerAdd:    true,
		AllowServerRemove: true,
		EnablePrompts:     true,
		DebugSearch:       true,
	}

	env.proxyServer, err = NewServer(cfg, logger)
	require.NoError(t, err)

	// Start proxy server in background
	ctx := context.Background()
	err = env.proxyServer.StartServer(ctx)
	require.NoError(t, err)

	env.proxyAddr = fmt.Sprintf("http://localhost%s/mcp", env.proxyServer.GetListenAddress())
	require.NotEmpty(t, env.proxyAddr)

	// Wait for server to be ready
	env.waitForServerReady()

	env.cleanup = func() {
		// Stop mock servers
		for _, mockServer := range env.mockServers {
			if mockServer.stopFunc != nil {
				mockServer.stopFunc()
			}
		}

		// Stop proxy server
		env.proxyServer.StopServer()
		env.proxyServer.Shutdown()

		// Remove temp directory
		os.RemoveAll(tempDir)
	}

	return env
}

// Cleanup cleans up all test resources
func (env *TestEnvironment) Cleanup() {
	if env.cleanup != nil {
		env.cleanup()
	}
}

// waitForServerReady waits for the proxy server to be ready
func (env *TestEnvironment) waitForServerReady() {
	timeout := time.After(15 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			status := env.proxyServer.GetStatus()
			env.t.Fatalf("Timeout waiting for server to be ready. Status: %+v", status)
		case <-ticker.C:
			if env.proxyServer.IsRunning() {
				// Give it a bit more time to fully initialize
				time.Sleep(2 * time.Second)
				return
			}
		}
	}
}

// CreateMockUpstreamServer creates and starts a mock upstream MCP server
func (env *TestEnvironment) CreateMockUpstreamServer(name string, tools []mcp.Tool) *MockUpstreamServer {
	// Create MCP server
	mcpServer := mcpserver.NewMCPServer(
		name,
		"1.0.0-test",
		mcpserver.WithToolCapabilities(true),
	)

	mockServer := &MockUpstreamServer{
		server: mcpServer,
		tools:  tools,
	}

	// Register tools
	for _, tool := range tools {
		toolCopy := tool // Capture for closure
		mcpServer.AddTool(toolCopy, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Mock tool implementation
			result := map[string]interface{}{
				"tool":    toolCopy.Name,
				"args":    request.Params.Arguments,
				"server":  name,
				"success": true,
			}

			jsonResult, _ := json.Marshal(result)
			return mcp.NewToolResultText(string(jsonResult)), nil
		})
	}

	// Start HTTP server on random port
	streamableServer := mcpserver.NewStreamableHTTPServer(mcpServer)

	// Find available port
	ln, err := net.Listen("tcp", ":0")
	require.NoError(env.t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	mockServer.addr = fmt.Sprintf("http://localhost:%d", port)

	// Create HTTP server
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: streamableServer,
	}
	mockServer.httpServer = httpServer

	// Start server in background
	go func() {
		err := httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			env.logger.Error("Mock server error", zap.Error(err))
		}
	}()

	// Wait for server to start
	time.Sleep(200 * time.Millisecond)

	mockServer.stopFunc = func() error {
		return httpServer.Shutdown(context.Background())
	}

	env.mockServers[name] = mockServer
	return mockServer
}

// CreateProxyClient creates an MCP client connected to the proxy server
func (env *TestEnvironment) CreateProxyClient() *client.Client {
	httpTransport, err := transport.NewStreamableHTTP(env.proxyAddr)
	require.NoError(env.t, err)

	mcpClient := client.NewClient(httpTransport)
	return mcpClient
}

// ConnectClient connects and initializes an MCP client
func (env *TestEnvironment) ConnectClient(mcpClient *client.Client) *mcp.InitializeResult {
	ctx := context.Background()

	err := mcpClient.Start(ctx)
	require.NoError(env.t, err)

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcpproxy-e2e-test",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := mcpClient.Initialize(ctx, initRequest)
	require.NoError(env.t, err)

	return serverInfo
}

// Test: Basic server startup and initialization
func TestE2E_ServerStartup(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	// Test that server is running
	assert.True(t, env.proxyServer.IsRunning())
	assert.NotEmpty(t, env.proxyAddr)
}

// Test: MCP client connection to proxy
func TestE2E_ClientConnection(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	// Create and connect client
	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()

	serverInfo := env.ConnectClient(mcpClient)

	// Verify server info
	assert.Equal(t, "mcpproxy-go", serverInfo.ServerInfo.Name)
	assert.Equal(t, "1.0.0", serverInfo.ServerInfo.Version)
	assert.NotNil(t, serverInfo.Capabilities.Tools)
}

// Test: Tool discovery and listing
func TestE2E_ToolDiscovery(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	// Create mock upstream server with tools
	mockTools := []mcp.Tool{
		{
			Name:        "test_tool_1",
			Description: "A test tool for testing",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"param1": map[string]interface{}{
						"type":        "string",
						"description": "Test parameter",
					},
				},
			},
		},
		{
			Name:        "test_tool_2",
			Description: "Another test tool",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"param2": map[string]interface{}{
						"type":        "number",
						"description": "Numeric parameter",
					},
				},
			},
		},
	}

	mockServer := env.CreateMockUpstreamServer("testserver", mockTools)

	// Connect client to proxy
	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	// Add upstream server to proxy using the same pattern as fixtures_test.go
	ctx := context.Background()
	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "testserver",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}

	result, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Unquarantine the server for testing (bypassing security restrictions)
	serverConfig, err := env.proxyServer.storageManager.GetUpstreamServer("testserver")
	require.NoError(t, err)
	serverConfig.Quarantined = false
	err = env.proxyServer.storageManager.SaveUpstreamServer(serverConfig)
	require.NoError(t, err)

	// Trigger connection to the unquarantined server
	err = env.proxyServer.upstreamManager.ConnectAll(ctx)
	require.NoError(t, err)

	// Wait for connection to establish
	time.Sleep(1 * time.Second)

	// Manually trigger tool discovery and indexing
	env.proxyServer.discoverAndIndexTools(ctx)

	// Wait for tools to be discovered and indexed
	time.Sleep(3 * time.Second)

	// Use retrieve_tools to search for tools
	searchRequest := mcp.CallToolRequest{}
	searchRequest.Params.Name = "retrieve_tools"
	searchRequest.Params.Arguments = map[string]interface{}{
		"query": "test tool",
		"limit": 10,
	}

	searchResult, err := mcpClient.CallTool(ctx, searchRequest)
	require.NoError(t, err)
	assert.False(t, searchResult.IsError)

	// Parse and verify search results
	require.Greater(t, len(searchResult.Content), 0)
	// Content is an array of mcp.Content, get the text from the first one
	var contentText string
	if len(searchResult.Content) > 0 {
		contentBytes, err := json.Marshal(searchResult.Content[0])
		require.NoError(t, err)
		var contentMap map[string]interface{}
		err = json.Unmarshal(contentBytes, &contentMap)
		require.NoError(t, err)
		if text, ok := contentMap["text"].(string); ok {
			contentText = text
		}
	}

	var searchResponse map[string]interface{}
	err = json.Unmarshal([]byte(contentText), &searchResponse)
	require.NoError(t, err)

	tools, ok := searchResponse["tools"].([]interface{})
	require.True(t, ok)
	assert.GreaterOrEqual(t, len(tools), 2) // Should find both tools
}

// Test: Tool calling through proxy
func TestE2E_ToolCalling(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	// Create mock upstream server
	mockTools := []mcp.Tool{
		{
			Name:        "echo_tool",
			Description: "Echoes back the input",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"message": map[string]interface{}{
						"type":        "string",
						"description": "Message to echo",
					},
				},
			},
		},
	}

	mockServer := env.CreateMockUpstreamServer("echoserver", mockTools)

	// Connect client and add upstream server
	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	// Add upstream server
	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "echoserver",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}

	_, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)

	// Unquarantine the server for testing (bypassing security restrictions)
	serverConfig, err := env.proxyServer.storageManager.GetUpstreamServer("echoserver")
	require.NoError(t, err)
	serverConfig.Quarantined = false
	err = env.proxyServer.storageManager.SaveUpstreamServer(serverConfig)
	require.NoError(t, err)

	// Trigger connection to the unquarantined server
	err = env.proxyServer.upstreamManager.ConnectAll(ctx)
	require.NoError(t, err)

	// Wait for connection to establish
	time.Sleep(1 * time.Second)

	// Manually trigger tool discovery and indexing
	env.proxyServer.discoverAndIndexTools(ctx)

	// Wait for tools to be discovered and indexed
	time.Sleep(3 * time.Second)

	// Call tool through proxy
	callRequest := mcp.CallToolRequest{}
	callRequest.Params.Name = "call_tool"
	callRequest.Params.Arguments = map[string]interface{}{
		"name": "echoserver:echo_tool",
		"args": map[string]interface{}{
			"message": "Hello from e2e test!",
		},
	}

	callResult, err := mcpClient.CallTool(ctx, callRequest)
	require.NoError(t, err)
	assert.False(t, callResult.IsError)

	// Verify result contains expected data
	require.Greater(t, len(callResult.Content), 0)
	// Extract text content
	var contentText string
	if len(callResult.Content) > 0 {
		contentBytes, err := json.Marshal(callResult.Content[0])
		require.NoError(t, err)
		var contentMap map[string]interface{}
		err = json.Unmarshal(contentBytes, &contentMap)
		require.NoError(t, err)
		if text, ok := contentMap["text"].(string); ok {
			contentText = text
		}
	}

	// The content is an array of content objects, we need to extract the text from the first one
	var contentArray []map[string]interface{}
	err = json.Unmarshal([]byte(contentText), &contentArray)
	require.NoError(t, err)
	require.Greater(t, len(contentArray), 0)

	// Extract the actual JSON response from the text field
	actualResponseText, ok := contentArray[0]["text"].(string)
	require.True(t, ok)

	var response map[string]interface{}
	err = json.Unmarshal([]byte(actualResponseText), &response)
	require.NoError(t, err)

	assert.Equal(t, "echo_tool", response["tool"])
	assert.Equal(t, "echoserver", response["server"])
	assert.Equal(t, true, response["success"])
}

// Test: Server management operations
func TestE2E_ServerManagement(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	// Test list servers (should be empty initially)
	listRequest := mcp.CallToolRequest{}
	listRequest.Params.Name = "upstream_servers"
	listRequest.Params.Arguments = map[string]interface{}{
		"operation": "list",
	}

	listResult, err := mcpClient.CallTool(ctx, listRequest)
	require.NoError(t, err)
	assert.False(t, listResult.IsError)

	// Test add server
	mockServer := env.CreateMockUpstreamServer("testmgmt", []mcp.Tool{})

	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "testmgmt",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}

	addResult, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)
	assert.False(t, addResult.IsError)

	// Test list servers again (should contain added server)
	listResult2, err := mcpClient.CallTool(ctx, listRequest)
	require.NoError(t, err)
	assert.False(t, listResult2.IsError)

	// Test remove server
	removeRequest := mcp.CallToolRequest{}
	removeRequest.Params.Name = "upstream_servers"
	removeRequest.Params.Arguments = map[string]interface{}{
		"operation": "remove",
		"name":      "testmgmt",
	}

	removeResult, err := mcpClient.CallTool(ctx, removeRequest)
	require.NoError(t, err)
	assert.False(t, removeResult.IsError)
}

// Test: Error handling and recovery
func TestE2E_ErrorHandling(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	// Test calling non-existent tool
	callRequest := mcp.CallToolRequest{}
	callRequest.Params.Name = "call_tool"
	callRequest.Params.Arguments = map[string]interface{}{
		"name": "nonexistent:tool",
		"args": map[string]interface{}{},
	}

	callResult, err := mcpClient.CallTool(ctx, callRequest)
	require.NoError(t, err)
	// Should return error but not crash
	assert.True(t, callResult.IsError || len(callResult.Content) > 0)

	// Test invalid server management operation
	invalidRequest := mcp.CallToolRequest{}
	invalidRequest.Params.Name = "upstream_servers"
	invalidRequest.Params.Arguments = map[string]interface{}{
		"operation": "invalid_operation",
	}

	invalidResult, err := mcpClient.CallTool(ctx, invalidRequest)
	require.NoError(t, err)
	assert.True(t, invalidResult.IsError)
}

// Test: Concurrent client operations
func TestE2E_ConcurrentOperations(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	// Create multiple clients
	clients := make([]*client.Client, 3)
	for i := range clients {
		clients[i] = env.CreateProxyClient()
		env.ConnectClient(clients[i])
		defer clients[i].Close()
	}

	// Create mock server
	mockTools := []mcp.Tool{
		{
			Name:        "concurrent_tool",
			Description: "Tool for concurrent testing",
			InputSchema: mcp.ToolInputSchema{Type: "object"},
		},
	}
	mockServer := env.CreateMockUpstreamServer("concurrent", mockTools)

	ctx := context.Background()

	// Add server from first client
	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "concurrent",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}

	_, err := clients[0].CallTool(ctx, addRequest)
	require.NoError(t, err)

	time.Sleep(1 * time.Second)

	// Perform concurrent operations
	done := make(chan bool, len(clients))

	for i, mcpClient := range clients {
		go func(clientIdx int, c *client.Client) {
			defer func() { done <- true }()

			// Each client performs retrieve_tools
			searchRequest := mcp.CallToolRequest{}
			searchRequest.Params.Name = "retrieve_tools"
			searchRequest.Params.Arguments = map[string]interface{}{
				"query": "concurrent",
				"limit": 5,
			}

			result, err := c.CallTool(ctx, searchRequest)
			assert.NoError(t, err, "Client %d search failed", clientIdx)
			assert.False(t, result.IsError, "Client %d search returned error", clientIdx)
		}(i, mcpClient)
	}

	// Wait for all operations to complete
	for i := 0; i < len(clients); i++ {
		select {
		case <-done:
			// Success
		case <-time.After(10 * time.Second):
			t.Fatal("Timeout waiting for concurrent operations")
		}
	}
}

// Test: Add single upstream server with command-based configuration
func TestE2E_AddUpstreamServerCommand(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	// Test adding a command-based server (which should use stdio transport)
	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "test-command-server",
		"command":   "npx",
		"args": []interface{}{
			"-y",
			"@modelcontextprotocol/server-brave-search",
		},
		"env": map[string]interface{}{
			"BRAVE_API_KEY": "test_key_123",
		},
		"enabled": true,
	}

	addResult, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)
	assert.False(t, addResult.IsError, "Add operation should succeed")

	// Parse the result
	require.Greater(t, len(addResult.Content), 0)
	var contentText string
	if len(addResult.Content) > 0 {
		contentBytes, err := json.Marshal(addResult.Content[0])
		require.NoError(t, err)
		var contentMap map[string]interface{}
		err = json.Unmarshal(contentBytes, &contentMap)
		require.NoError(t, err)
		if text, ok := contentMap["text"].(string); ok {
			contentText = text
		}
	}

	var addResponse map[string]interface{}
	err = json.Unmarshal([]byte(contentText), &addResponse)
	require.NoError(t, err)

	// Verify the operation was successful
	assert.Equal(t, "configured", addResponse["status"])
	assert.Contains(t, addResponse["message"], "test-command-server")

	// Verify the server configuration by listing
	listRequest := mcp.CallToolRequest{}
	listRequest.Params.Name = "upstream_servers"
	listRequest.Params.Arguments = map[string]interface{}{
		"operation": "list",
	}

	listResult, err := mcpClient.CallTool(ctx, listRequest)
	require.NoError(t, err)
	assert.False(t, listResult.IsError)

	// Parse list result
	var listContentText string
	if len(listResult.Content) > 0 {
		contentBytes, err := json.Marshal(listResult.Content[0])
		require.NoError(t, err)
		var contentMap map[string]interface{}
		err = json.Unmarshal(contentBytes, &contentMap)
		require.NoError(t, err)
		if text, ok := contentMap["text"].(string); ok {
			listContentText = text
		}
	}

	var listResponse map[string]interface{}
	err = json.Unmarshal([]byte(listContentText), &listResponse)
	require.NoError(t, err)

	// Find and verify the server
	if servers, ok := listResponse["servers"].([]interface{}); ok {
		found := false
		for _, server := range servers {
			if serverMap, ok := server.(map[string]interface{}); ok {
				if name, ok := serverMap["name"].(string); ok && name == "test-command-server" {
					found = true
					// Verify key configuration properties
					assert.Equal(t, "npx", serverMap["command"])
					assert.Equal(t, "stdio", serverMap["protocol"]) // Should be stdio for command-based
					assert.Equal(t, true, serverMap["enabled"])

					// Verify args array
					if args, ok := serverMap["args"].([]interface{}); ok {
						assert.Contains(t, args, "-y")
						assert.Contains(t, args, "@modelcontextprotocol/server-brave-search")
					}

					// Verify environment variables
					if env_vars, ok := serverMap["env"].(map[string]interface{}); ok {
						assert.Equal(t, "test_key_123", env_vars["BRAVE_API_KEY"])
					}
					break
				}
			}
		}
		assert.True(t, found, "test-command-server should be found in the list")
	}

	// Test removal of the server
	removeRequest := mcp.CallToolRequest{}
	removeRequest.Params.Name = "upstream_servers"
	removeRequest.Params.Arguments = map[string]interface{}{
		"operation": "remove",
		"name":      "test-command-server",
	}

	removeResult, err := mcpClient.CallTool(ctx, removeRequest)
	require.NoError(t, err)
	assert.False(t, removeResult.IsError, "Remove operation should succeed")
}
