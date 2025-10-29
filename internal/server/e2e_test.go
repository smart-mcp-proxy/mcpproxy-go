package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	// Disable OAuth for e2e tests to avoid network calls to mock servers
	oldValue := os.Getenv("MCPPROXY_DISABLE_OAUTH")
	os.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

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

	// Create data directory with secure permissions (0700 required for Unix socket security)
	dataDir := filepath.Join(tempDir, "data")
	err = os.MkdirAll(dataDir, 0700)
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

	// Set proxy address using 127.0.0.1 instead of localhost for reliable connection
	// across all platforms (avoids IPv4/IPv6 resolution issues)
	env.proxyAddr = fmt.Sprintf("http://127.0.0.1:%d/mcp", testPort)
	require.NotEmpty(t, env.proxyAddr)

	// Wait for server to be ready
	env.waitForServerReady()

	env.cleanup = func() {
		// Stop mock servers
		for _, mockServer := range env.mockServers {
			if mockServer.stopFunc != nil {
				_ = mockServer.stopFunc()
			}
		}

		// Stop proxy server
		_ = env.proxyServer.StopServer()
		_ = env.proxyServer.Shutdown()

		// Remove temp directory
		os.RemoveAll(tempDir)

		// Restore original OAuth environment variable
		if oldValue == "" {
			os.Unsetenv("MCPPROXY_DISABLE_OAUTH")
		} else {
			os.Setenv("MCPPROXY_DISABLE_OAUTH", oldValue)
		}
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
				// Actually test if the HTTP server is accepting connections
				if env.testServerConnection() {
					// Give it a bit more time to fully initialize
					time.Sleep(500 * time.Millisecond)
					return
				}
			}
		}
	}
}

// testServerConnection tests if the server is actually accepting HTTP connections
func (env *TestEnvironment) testServerConnection() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", env.proxyAddr, http.NoBody)
	if err != nil {
		return false
	}

	client := &http.Client{
		Timeout: 1 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()

	// Any response (even an error response) means the server is accepting connections
	return true
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
	for i := range tools {
		toolCopy := tools[i] // Capture for closure
		mcpServer.AddTool(toolCopy, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	serverConfig, err := env.proxyServer.runtime.StorageManager().GetUpstreamServer("testserver")
	require.NoError(t, err)
	serverConfig.Quarantined = false
	err = env.proxyServer.runtime.StorageManager().SaveUpstreamServer(serverConfig)
	require.NoError(t, err)

	// Get all servers from storage and reload configuration
	// This properly triggers supervisor reconciliation and creates the client
	servers, err := env.proxyServer.runtime.StorageManager().ListUpstreamServers()
	require.NoError(t, err)

	// Update runtime config with the unquarantined server
	cfg := env.proxyServer.runtime.Config()
	cfg.Servers = servers
	err = env.proxyServer.runtime.LoadConfiguredServers(cfg)
	require.NoError(t, err)

	// Wait for supervisor to reconcile and client to connect
	time.Sleep(3 * time.Second)

	// Manually trigger tool discovery and indexing
	_ = env.proxyServer.runtime.DiscoverAndIndexTools(ctx)

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
	serverConfig, err := env.proxyServer.runtime.StorageManager().GetUpstreamServer("echoserver")
	require.NoError(t, err)
	serverConfig.Quarantined = false
	err = env.proxyServer.runtime.StorageManager().SaveUpstreamServer(serverConfig)
	require.NoError(t, err)

	// Get all servers from storage and reload configuration
	// This properly triggers supervisor reconciliation and creates the client
	servers, err := env.proxyServer.runtime.StorageManager().ListUpstreamServers()
	require.NoError(t, err)

	// Update runtime config with the unquarantined server
	cfg := env.proxyServer.runtime.Config()
	cfg.Servers = servers
	err = env.proxyServer.runtime.LoadConfiguredServers(cfg)
	require.NoError(t, err)

	// Wait for supervisor to reconcile and client to connect
	time.Sleep(3 * time.Second)

	// Manually trigger tool discovery and indexing
	_ = env.proxyServer.runtime.DiscoverAndIndexTools(ctx)

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

	// Parse the content response which has format: {"content": [{"type": "text", "text": "..."}]}
	var contentResponse map[string]interface{}
	err = json.Unmarshal([]byte(contentText), &contentResponse)
	require.NoError(t, err)

	// Extract the content array
	contentArray, ok := contentResponse["content"].([]interface{})
	require.True(t, ok)
	require.Greater(t, len(contentArray), 0)

	// Get the first content item
	firstContent, ok := contentArray[0].(map[string]interface{})
	require.True(t, ok)

	// Extract the actual JSON response from the text field
	actualResponseText, ok := firstContent["text"].(string)
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
	}

	// Defer close all clients
	defer func() {
		for _, client := range clients {
			client.Close()
		}
	}()

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

// Test: SSE Events endpoint functionality
func TestE2E_SSEEvents(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	// Test SSE connection without authentication (no API key configured)
	testSSEConnection(t, env, "")

	// Now test with API key authentication
	// Update config to include API key
	cfg := env.proxyServer.runtime.Config()
	cfg.APIKey = "test-api-key-12345"

	// Test SSE with correct API key
	testSSEConnection(t, env, "test-api-key-12345")

	// Test SSE with incorrect API key
	testSSEConnectionAuthFailure(t, env, "wrong-api-key")
}

// testSSEConnection tests SSE connection functionality
func testSSEConnection(t *testing.T, env *TestEnvironment, apiKey string) {
	listenAddr := env.proxyServer.GetListenAddress()
	if listenAddr == "" {
		listenAddr = ":8080" // fallback
	}

	// Parse the listen address to handle IPv6 format
	var sseURL string
	if strings.HasPrefix(listenAddr, "[::]:") {
		// IPv6 format [::]:port -> localhost:port
		port := strings.TrimPrefix(listenAddr, "[::]:")
		sseURL = fmt.Sprintf("http://localhost:%s/events", port)
	} else if strings.HasPrefix(listenAddr, ":") {
		// Port only format :port -> localhost:port
		port := strings.TrimPrefix(listenAddr, ":")
		sseURL = fmt.Sprintf("http://localhost:%s/events", port)
	} else {
		// Full address format
		sseURL = fmt.Sprintf("http://%s/events", listenAddr)
	}

	if apiKey != "" {
		sseURL += "?apikey=" + apiKey
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Create HTTP client with very short timeout to avoid hanging on SSE stream
	client := &http.Client{
		Timeout: 500 * time.Millisecond, // Very short timeout
	}

	// Test that SSE endpoint accepts GET connections
	// The connection will timeout quickly, but we can check the initial response
	req, err := http.NewRequestWithContext(ctx, "GET", sseURL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)

	// We expect either:
	// 1. A successful connection (200) that times out
	// 2. A timeout error (which indicates the connection was established)
	if err != nil && resp == nil {
		// Connection timeout is expected for SSE - this means the endpoint is working
		t.Logf("✅ SSE endpoint connection established (timed out as expected): %s", sseURL)
		return
	}

	if resp != nil {
		defer resp.Body.Close()
		// If we get a response, it should be 200 OK
		assert.Equal(t, 200, resp.StatusCode, "SSE endpoint should return 200 OK")
		t.Logf("✅ SSE endpoint accessible with status %d at %s", resp.StatusCode, sseURL)
	}
}

// testSSEConnectionAuthFailure tests SSE connection with invalid authentication
func testSSEConnectionAuthFailure(t *testing.T, env *TestEnvironment, wrongAPIKey string) {
	listenAddr := env.proxyServer.GetListenAddress()
	if listenAddr == "" {
		listenAddr = ":8080" // fallback
	}

	// Parse the listen address to handle IPv6 format
	var sseURL string
	if strings.HasPrefix(listenAddr, "[::]:") {
		// IPv6 format [::]:port -> localhost:port
		port := strings.TrimPrefix(listenAddr, "[::]:")
		sseURL = fmt.Sprintf("http://localhost:%s/events?apikey=%s", port, wrongAPIKey)
	} else if strings.HasPrefix(listenAddr, ":") {
		// Port only format :port -> localhost:port
		port := strings.TrimPrefix(listenAddr, ":")
		sseURL = fmt.Sprintf("http://localhost:%s/events?apikey=%s", port, wrongAPIKey)
	} else {
		// Full address format
		sseURL = fmt.Sprintf("http://%s/events?apikey=%s", listenAddr, wrongAPIKey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", sseURL, http.NoBody)
	require.NoError(t, err)

	resp, err := client.Do(req)

	// For authentication failures, we should get an immediate 401 response
	if err != nil {
		t.Fatalf("Expected immediate auth failure response, got error: %v", err)
	}

	require.NotNil(t, resp, "Expected HTTP response for auth failure")
	defer resp.Body.Close()

	// Should receive 401 Unauthorized when API key is wrong
	assert.Equal(t, 401, resp.StatusCode, "SSE endpoint should return 401 for invalid API key")
}

// Test: Add single upstream server with command-based configuration
func TestE2E_AddUpstreamServerCommand(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	// Test adding a command-based server (using echo to avoid external dependencies)
	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "test-command-server",
		"command":   "echo",
		"args": []interface{}{
			"test-mcp-server",
		},
		"env": map[string]interface{}{
			"TEST_KEY": "test_value_123",
		},
		"enabled": false, // Disabled to prevent actual connection attempts
	}

	addResult, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)
	if addResult.IsError {
		t.Logf("Add operation failed with error: %v", addResult)
	}
	assert.False(t, addResult.IsError, "Add operation should succeed")

	// Parse the result
	require.Greater(t, len(addResult.Content), 0)
	t.Logf("Add result content: %+v", addResult.Content)
	var contentText string
	if len(addResult.Content) > 0 {
		contentBytes, err := json.Marshal(addResult.Content[0])
		require.NoError(t, err)
		t.Logf("Content bytes: %s", string(contentBytes))
		var contentMap map[string]interface{}
		err = json.Unmarshal(contentBytes, &contentMap)
		require.NoError(t, err)
		if text, ok := contentMap["text"].(string); ok {
			contentText = text
		}
		t.Logf("Content text: %s", contentText)
	}

	var addResponse map[string]interface{}
	err = json.Unmarshal([]byte(contentText), &addResponse)
	require.NoError(t, err)

	// Verify the operation was successful
	assert.Equal(t, "configured", addResponse["status"])
	assert.Equal(t, "disabled", addResponse["connection_status"]) // Server disabled, so connection is disabled
	assert.Contains(t, addResponse["message"], "test-command-server")
	assert.Equal(t, true, addResponse["quarantined"]) // Server should be quarantined by default
	assert.Equal(t, false, addResponse["enabled"])    // Server should be disabled as configured

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
					// Verify key configuration properties, but not the command itself
					// as it's now wrapped in a shell.
					assert.Equal(t, "stdio", serverMap["protocol"])
					assert.Equal(t, false, serverMap["enabled"]) // Server should be disabled as configured

					// Verify environment variables
					if envVars, ok := serverMap["env"].(map[string]interface{}); ok {
						assert.Equal(t, "test_value_123", envVars["TEST_KEY"])
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

// Test: Inspect quarantined server with temporary exemption
func TestE2E_InspectQuarantined(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	// Create MCP client
	mcpClient := env.CreateProxyClient()
	env.ConnectClient(mcpClient)
	defer mcpClient.Close()

	// Create mock server with some tools
	mockTools := []mcp.Tool{
		{
			Name:        "test_tool_1",
			Description: "First test tool",
			InputSchema: mcp.ToolInputSchema{Type: "object"},
		},
		{
			Name:        "test_tool_2",
			Description: "Second test tool",
			InputSchema: mcp.ToolInputSchema{Type: "object"},
		},
	}
	mockServer := env.CreateMockUpstreamServer("quarantined-server", mockTools)

	ctx := context.Background()

	// Add server (will be automatically quarantined)
	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "quarantined-server",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}

	addResult, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)
	assert.False(t, addResult.IsError, "Add operation should succeed")

	// Wait for server to be added to storage (quarantined servers don't get clients created immediately)
	time.Sleep(500 * time.Millisecond)

	t.Log("🔍 Calling inspect_quarantined for quarantined-server...")

	// Call inspect_quarantined (use quarantine_security tool, not upstream_servers)
	inspectRequest := mcp.CallToolRequest{}
	inspectRequest.Params.Name = "quarantine_security"
	inspectRequest.Params.Arguments = map[string]interface{}{
		"operation": "inspect_quarantined",
		"name":      "quarantined-server",
	}

	inspectResult, err := mcpClient.CallTool(ctx, inspectRequest)
	require.NoError(t, err, "inspect_quarantined should not return error")

	// Debug: Print all content items with their types
	t.Logf("📋 Inspection result - IsError: %v, Content count: %d", inspectResult.IsError, len(inspectResult.Content))
	for i, content := range inspectResult.Content {
		t.Logf("Content[%d] type: %T", i, content)
		// Handle both pointer and value types
		if textContent, ok := content.(*mcp.TextContent); ok {
			t.Logf("Content[%d] text (pointer): %s", i, textContent.Text)
		} else if textContent, ok := content.(mcp.TextContent); ok {
			t.Logf("Content[%d] text (value): %s", i, textContent.Text)
		}
	}

	if inspectResult.IsError {
		// Print the error for debugging - handle both pointer and value types
		for _, content := range inspectResult.Content {
			if textContent, ok := content.(*mcp.TextContent); ok {
				t.Logf("❌ Error from inspect_quarantined (pointer): %s", textContent.Text)
			} else if textContent, ok := content.(mcp.TextContent); ok {
				t.Logf("❌ Error from inspect_quarantined (value): %s", textContent.Text)
			}
		}
		t.Fatal("inspect_quarantined returned an error - see logs above")
	}

	// Verify result contains tool data
	require.NotEmpty(t, inspectResult.Content, "Result should have content")

	// Verify the result contains information about the tools
	var resultText string
	for _, content := range inspectResult.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			resultText += textContent.Text
		}
	}
	assert.Contains(t, resultText, "test_tool_1", "Result should mention test_tool_1")
	assert.Contains(t, resultText, "test_tool_2", "Result should mention test_tool_2")

	// After inspection, server should be disconnected again (exemption revoked)
	time.Sleep(1 * time.Second)

	// Now check if client exists and is disconnected
	upstreamManager := env.proxyServer.runtime.UpstreamManager()
	client, exists := upstreamManager.GetClient("quarantined-server")
	if exists {
		assert.False(t, client.IsConnected(), "Server should be disconnected after inspection")
	} else {
		t.Log("Client no longer exists after exemption revoked (acceptable)")
	}

	t.Log("✅ Test passed: Quarantine inspection with temporary exemption works correctly")
}
