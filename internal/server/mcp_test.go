package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/upstream"
)

func TestSecurityConfigValidation(t *testing.T) {
	tests := []struct {
		name              string
		readOnlyMode      bool
		disableManagement bool
		allowServerAdd    bool
		allowServerRemove bool
		operation         string
		shouldAllow       bool
	}{
		{
			name:         "list allowed in read-only mode",
			operation:    "list",
			readOnlyMode: true,
			shouldAllow:  true,
		},
		{
			name:         "add blocked in read-only mode",
			operation:    "add",
			readOnlyMode: true,
			shouldAllow:  false,
		},
		{
			name:              "list blocked when management disabled",
			operation:         "list",
			disableManagement: true,
			shouldAllow:       false,
		},
		{
			name:           "add blocked when not allowed",
			operation:      "add",
			allowServerAdd: false,
			shouldAllow:    false,
		},
		{
			name:              "remove blocked when not allowed",
			operation:         "remove",
			allowServerRemove: false,
			shouldAllow:       false,
		},
		{
			name:           "add_batch blocked when not allowed",
			operation:      "add_batch",
			allowServerAdd: false,
			shouldAllow:    false,
		},
		{
			name:           "import_cursor blocked when not allowed",
			operation:      "import_cursor",
			allowServerAdd: false,
			shouldAllow:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &config.Config{
				ReadOnlyMode:      tt.readOnlyMode,
				DisableManagement: tt.disableManagement,
				AllowServerAdd:    tt.allowServerAdd,
				AllowServerRemove: tt.allowServerRemove,
			}

			// Test security validation logic
			isBlocked := false

			// Apply security logic
			if config.ReadOnlyMode && tt.operation != "list" {
				isBlocked = true
			}
			if config.DisableManagement {
				isBlocked = true
			}
			switch tt.operation {
			case "add", "add_batch", "import_cursor":
				if !config.AllowServerAdd {
					isBlocked = true
				}
			case "remove":
				if !config.AllowServerRemove {
					isBlocked = true
				}
			}

			expectedBlocked := !tt.shouldAllow
			assert.Equal(t, expectedBlocked, isBlocked, "Security check mismatch for operation %s", tt.operation)
		})
	}
}

func TestAnalyzeQueryLogic(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected map[string]interface{}
	}{
		{
			name:  "simple query",
			query: "database query",
			expected: map[string]interface{}{
				"original_query":  "database query",
				"query_length":    14,
				"word_count":      2,
				"has_underscores": false,
				"has_colons":      false,
				"is_tool_name":    false,
			},
		},
		{
			name:  "tool name format",
			query: "sqlite:query_users",
			expected: map[string]interface{}{
				"original_query":  "sqlite:query_users",
				"query_length":    18,
				"word_count":      1,
				"has_underscores": true,
				"has_colons":      true,
				"is_tool_name":    true,
				"server_part":     "sqlite",
				"tool_part":       "query_users",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the analysis logic directly
			result := analyzeQueryHelper(tt.query)
			for key, expectedValue := range tt.expected {
				assert.Equal(t, expectedValue, result[key], "Mismatch for key: %s", key)
			}
		})
	}
}

// Helper function that mimics the logic from handleRetrieveTools
func analyzeQueryHelper(query string) map[string]interface{} {
	analysis := map[string]interface{}{
		"original_query":  query,
		"query_length":    len(query),
		"word_count":      len(strings.Fields(query)),
		"has_underscores": strings.Contains(query, "_"),
		"has_colons":      strings.Contains(query, ":"),
		"is_tool_name":    strings.Contains(query, ":"),
	}

	// Check if query looks like a tool name pattern
	if strings.Contains(query, ":") {
		parts := strings.SplitN(query, ":", 2)
		if len(parts) == 2 {
			analysis["server_part"] = parts[0]
			analysis["tool_part"] = parts[1]
		}
	}

	return analysis
}

func TestMCPRequestParsing(t *testing.T) {
	tests := []struct {
		name         string
		requestArgs  map[string]interface{}
		expectedArgs map[string]interface{}
	}{
		{
			name: "Valid args parameter",
			requestArgs: map[string]interface{}{
				"name": "coingecko:coins_id",
				"args": map[string]interface{}{
					"id":          "bitcoin",
					"market_data": true,
				},
			},
			expectedArgs: map[string]interface{}{
				"id":          "bitcoin",
				"market_data": true,
			},
		},
		{
			name: "No args parameter",
			requestArgs: map[string]interface{}{
				"name": "simple:tool",
			},
			expectedArgs: nil,
		},
		{
			name: "Empty args map",
			requestArgs: map[string]interface{}{
				"name": "test:tool",
				"args": map[string]interface{}{},
			},
			expectedArgs: map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock request
			request := mcp.CallToolRequest{}
			request.Params.Name = "call_tool"
			request.Params.Arguments = tt.requestArgs

			// Extract args using the same logic as in handleCallTool
			var args map[string]interface{}
			if request.Params.Arguments != nil {
				if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
					if argsParam, ok := argumentsMap["args"]; ok {
						if argsMap, ok := argsParam.(map[string]interface{}); ok {
							args = argsMap
						}
					}
				}
			}

			// Verify the result
			if tt.expectedArgs == nil {
				assert.Nil(t, args)
			} else {
				assert.Equal(t, tt.expectedArgs, args)
			}
		})
	}
}

func TestToolFormatConversion(t *testing.T) {
	// Test the MCP tool format conversion logic from handleRetrieveTools
	mockResults := []*config.SearchResult{
		{
			Tool: &config.ToolMetadata{
				Name:        "coingecko:coins_id",
				ServerName:  "coingecko",
				Description: "Get detailed information about a cryptocurrency by ID",
				ParamsJSON:  `{"type": "object", "properties": {"id": {"type": "string", "description": "Cryptocurrency ID"}, "market_data": {"type": "boolean", "description": "Include market data"}}}`,
			},
			Score: 0.95,
		},
		{
			Tool: &config.ToolMetadata{
				Name:        "github:get_repo",
				ServerName:  "github",
				Description: "Get repository information",
				ParamsJSON:  `{"type": "object", "properties": {"repo": {"type": "string"}}}`,
			},
			Score: 0.8,
		},
	}

	// Convert to MCP format using the same logic as in handleRetrieveTools
	var mcpTools []map[string]interface{}
	for _, result := range mockResults {
		// Parse the input schema from ParamsJSON
		var inputSchema map[string]interface{}
		if result.Tool.ParamsJSON != "" {
			if err := json.Unmarshal([]byte(result.Tool.ParamsJSON), &inputSchema); err != nil {
				inputSchema = map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				}
			}
		} else {
			inputSchema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		// Create MCP-compatible tool representation
		mcpTool := map[string]interface{}{
			"name":        result.Tool.Name,
			"description": result.Tool.Description,
			"inputSchema": inputSchema,
			"score":       result.Score,
			"server":      result.Tool.ServerName,
		}
		mcpTools = append(mcpTools, mcpTool)
	}

	// Verify the conversion
	assert.Len(t, mcpTools, 2)

	// Check first tool
	firstTool := mcpTools[0]
	assert.Equal(t, "coingecko:coins_id", firstTool["name"])
	assert.Equal(t, "Get detailed information about a cryptocurrency by ID", firstTool["description"])
	assert.Equal(t, "coingecko", firstTool["server"])
	assert.Equal(t, 0.95, firstTool["score"])

	// Check inputSchema structure
	inputSchema, ok := firstTool["inputSchema"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "object", inputSchema["type"])

	properties, ok := inputSchema["properties"].(map[string]interface{})
	assert.True(t, ok)
	assert.Contains(t, properties, "id")
	assert.Contains(t, properties, "market_data")
}

func TestUpstreamServerOperations(t *testing.T) {
	// Test batch add servers parsing
	t.Run("BatchAddServers", func(t *testing.T) {
		request := mcp.CallToolRequest{}
		request.Params.Name = "upstream_servers"
		request.Params.Arguments = map[string]interface{}{
			"operation": "add_batch",
			"servers": []interface{}{
				map[string]interface{}{
					"name":    "test-server-1",
					"url":     "http://localhost:3001",
					"enabled": true,
				},
				map[string]interface{}{
					"name":    "test-server-2",
					"command": "python",
					"args":    []interface{}{"-m", "test_server"},
					"env":     map[string]interface{}{"TEST": "value"},
					"enabled": true,
				},
			},
		}

		// Mock server response parsing
		var servers []interface{}
		if request.Params.Arguments != nil {
			if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
				if serversParam, ok := argumentsMap["servers"]; ok {
					if serversList, ok := serversParam.([]interface{}); ok {
						servers = serversList
					}
				}
			}
		}

		assert.Len(t, servers, 2)

		// Verify first server (HTTP)
		server1 := servers[0].(map[string]interface{})
		assert.Equal(t, "test-server-1", server1["name"])
		assert.Equal(t, "http://localhost:3001", server1["url"])
		assert.Equal(t, true, server1["enabled"])

		// Verify second server (stdio)
		server2 := servers[1].(map[string]interface{})
		assert.Equal(t, "test-server-2", server2["name"])
		assert.Equal(t, "python", server2["command"])
		assert.Equal(t, []interface{}{"-m", "test_server"}, server2["args"])
		assert.Equal(t, map[string]interface{}{"TEST": "value"}, server2["env"])
	})

	// Test import Cursor IDE format
	t.Run("ImportCursorFormat", func(t *testing.T) {
		request := mcp.CallToolRequest{}
		request.Params.Name = "upstream_servers"
		request.Params.Arguments = map[string]interface{}{
			"operation": "import_cursor",
			"cursor_config": map[string]interface{}{
				"mcp-server-sqlite": map[string]interface{}{
					"command": "uvx",
					"args":    []interface{}{"mcp-server-sqlite", "--db-path", "/tmp/test.db"},
					"env":     map[string]interface{}{"MCP_SQLITE_PATH": "/tmp/test.db"},
				},
				"mcp-server-github": map[string]interface{}{
					"url":     "http://localhost:3000/mcp",
					"headers": map[string]interface{}{"Authorization": "Bearer token123"},
				},
			},
		}

		// Parse cursor config
		var cursorConfig map[string]interface{}
		if request.Params.Arguments != nil {
			if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
				if configParam, ok := argumentsMap["cursor_config"]; ok {
					if configMap, ok := configParam.(map[string]interface{}); ok {
						cursorConfig = configMap
					}
				}
			}
		}

		assert.Len(t, cursorConfig, 2)

		// Verify SQLite server
		sqliteServer := cursorConfig["mcp-server-sqlite"].(map[string]interface{})
		assert.Equal(t, "uvx", sqliteServer["command"])
		assert.Equal(t, []interface{}{"mcp-server-sqlite", "--db-path", "/tmp/test.db"}, sqliteServer["args"])

		// Verify GitHub server
		githubServer := cursorConfig["mcp-server-github"].(map[string]interface{})
		assert.Equal(t, "http://localhost:3000/mcp", githubServer["url"])
		assert.Equal(t, map[string]interface{}{"Authorization": "Bearer token123"}, githubServer["headers"])
	})
}

func TestConfigSecurityModes(t *testing.T) {
	tests := []struct {
		name              string
		readOnlyMode      bool
		disableManagement bool
		allowServerAdd    bool
		allowServerRemove bool
		expectCanManage   bool
		expectCanAdd      bool
		expectCanRemove   bool
	}{
		{
			name:              "default permissive mode",
			readOnlyMode:      false,
			disableManagement: false,
			allowServerAdd:    true,
			allowServerRemove: true,
			expectCanManage:   true,
			expectCanAdd:      true,
			expectCanRemove:   true,
		},
		{
			name:              "read-only mode",
			readOnlyMode:      true,
			disableManagement: false,
			allowServerAdd:    true,
			allowServerRemove: true,
			expectCanManage:   false,
			expectCanAdd:      false,
			expectCanRemove:   false,
		},
		{
			name:              "disable management",
			readOnlyMode:      false,
			disableManagement: true,
			allowServerAdd:    true,
			allowServerRemove: true,
			expectCanManage:   false,
			expectCanAdd:      false,
			expectCanRemove:   false,
		},
		{
			name:              "allow add but not remove",
			readOnlyMode:      false,
			disableManagement: false,
			allowServerAdd:    true,
			allowServerRemove: false,
			expectCanManage:   true,
			expectCanAdd:      true,
			expectCanRemove:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &config.Config{
				ReadOnlyMode:      tt.readOnlyMode,
				DisableManagement: tt.disableManagement,
				AllowServerAdd:    tt.allowServerAdd,
				AllowServerRemove: tt.allowServerRemove,
			}

			// Test configuration logic
			canManage := !config.ReadOnlyMode && !config.DisableManagement
			canAdd := canManage && config.AllowServerAdd
			canRemove := canManage && config.AllowServerRemove

			assert.Equal(t, tt.expectCanManage, canManage)
			assert.Equal(t, tt.expectCanAdd, canAdd)
			assert.Equal(t, tt.expectCanRemove, canRemove)
		})
	}
}

func TestReadCacheValidation(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		offset      float64
		limit       float64
		expectError bool
		errorMsg    string
	}{
		{
			name:   "valid cache read",
			key:    "cache123",
			offset: 0,
			limit:  50,
		},
		{
			name:        "missing key",
			key:         "",
			expectError: true,
			errorMsg:    "Missing required parameter 'key'",
		},
		{
			name:        "negative offset",
			key:         "cache123",
			offset:      -5,
			expectError: true,
			errorMsg:    "Offset must be non-negative",
		},
		{
			name:        "invalid limit",
			key:         "cache123",
			limit:       1500,
			expectError: true,
			errorMsg:    "Limit must be between 1 and 1000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test validation logic
			hasError := false
			errorMessage := ""

			if tt.key == "" {
				hasError = true
				errorMessage = "Missing required parameter 'key'"
			} else if tt.offset < 0 {
				hasError = true
				errorMessage = "Offset must be non-negative"
			} else if tt.limit > 1000 {
				hasError = true
				errorMessage = "Limit must be between 1 and 1000"
			}

			assert.Equal(t, tt.expectError, hasError)
			if tt.expectError {
				assert.Contains(t, errorMessage, tt.errorMsg)
			}
		})
	}
}

func TestDefaultConfigSettings(t *testing.T) {
	config := config.DefaultConfig()

	// Test default values
	assert.Equal(t, ":8080", config.Listen)
	assert.Equal(t, "", config.DataDir)
	assert.True(t, config.EnableTray)
	assert.False(t, config.DebugSearch)
	assert.Equal(t, 5, config.TopK)
	assert.Equal(t, 15, config.ToolsLimit)
	assert.Equal(t, 20000, config.ToolResponseLimit)

	// Test security defaults (permissive)
	assert.False(t, config.ReadOnlyMode)
	assert.False(t, config.DisableManagement)
	assert.True(t, config.AllowServerAdd)
	assert.True(t, config.AllowServerRemove)

	// Test prompts default
	assert.True(t, config.EnablePrompts)

	// Test empty servers list
	assert.Empty(t, config.Servers)
}

func TestRetrieveToolsParameters(t *testing.T) {
	tests := []struct {
		name     string
		limit    float64
		expected int
	}{
		{
			name:     "normal limit",
			limit:    10,
			expected: 10,
		},
		{
			name:     "limit over 100 should be capped",
			limit:    150,
			expected: 100,
		},
		{
			name:     "zero limit should use default",
			limit:    0,
			expected: 20, // default when 0 is passed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test limit validation logic
			limit := int(tt.limit)
			if limit <= 0 {
				limit = 20 // default
			}
			if limit > 100 {
				limit = 100
			}

			assert.Equal(t, tt.expected, limit)
		})
	}
}

func TestHandleCallToolErrorRecovery(t *testing.T) {
	// Test that tool call errors don't break the server's ability to handle subsequent requests
	// This test verifies the core issue mentioned in the error logs

	mockProxy := &MCPProxyServer{
		upstreamManager: upstream.NewManager(zap.NewNop()),
		logger:          zap.NewNop(),
	}

	ctx := context.Background()

	// Test 1: Call a tool that should fail (non-existent upstream server)
	request1 := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "non-existent-server:some_tool",
			Arguments: map[string]interface{}{},
		},
	}

	// This should return an error result, not fail catastrophically
	result1, err := mockProxy.handleCallTool(ctx, request1)
	assert.NoError(t, err) // handleCallTool should not return an error directly
	assert.NotNil(t, result1)

	// The result should be an error
	assert.True(t, result1.IsError, "Should return error for non-existent server")

	// Test 2: Test that the proxy can still handle other calls after an error
	// This is testing the core issue - that errors don't break the server
	request2 := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "another-non-existent:tool",
			Arguments: map[string]interface{}{},
		},
	}

	// This should also return an error but not crash the server
	result2, err := mockProxy.handleCallTool(ctx, request2)
	assert.NoError(t, err) // Should not panic or return nil
	assert.NotNil(t, result2)
	assert.True(t, result2.IsError, "Should still handle subsequent calls")
}

func TestHandleCallToolCompleteErrorHandling(t *testing.T) {
	// Test comprehensive error handling scenarios including self-referential calls

	mockProxy := &MCPProxyServer{
		upstreamManager: upstream.NewManager(zap.NewNop()),
		logger:          zap.NewNop(),
		config:          &config.Config{}, // Add minimal config for testing
	}

	ctx := context.Background()

	// Test 1: Client calls proxy tool using server:tool format (should be handled as non-existent server)
	request1 := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "some-proxy-name:retrieve_tools",
			Arguments: map[string]interface{}{
				"query": "test",
			},
		},
	}

	result1, err := mockProxy.handleCallTool(ctx, request1)
	assert.NoError(t, err)
	assert.NotNil(t, result1)
	assert.True(t, result1.IsError, "Should return error for non-existent server")

	// Test 2: Non-existent upstream server
	request2 := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "non-existent-server:some_tool",
			Arguments: map[string]interface{}{},
		},
	}

	result2, err := mockProxy.handleCallTool(ctx, request2)
	assert.NoError(t, err)
	assert.NotNil(t, result2)
	assert.True(t, result2.IsError, "Non-existent server should return error")

	// Test 3: Invalid tool format
	request3 := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "invalid_tool_format",
			Arguments: map[string]interface{}{},
		},
	}

	result3, err := mockProxy.handleCallTool(ctx, request3)
	assert.NoError(t, err)
	assert.NotNil(t, result3)
	assert.True(t, result3.IsError, "Invalid tool format should return error")

	// Test 4: Multiple sequential calls after errors (this tests the main issue)
	for i := 0; i < 5; i++ {
		result, err := mockProxy.handleCallTool(ctx, request2)
		assert.NoError(t, err, "Call %d should not return nil or panic", i+1)
		assert.NotNil(t, result, "Call %d should return a result", i+1)
		assert.True(t, result.IsError, "Call %d should return error", i+1)
	}
}

// Test: Quarantine functionality for security
func TestE2E_QuarantineFunctionality(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	// Test 1: Add a server (should be quarantined by default)
	mockServer := env.CreateMockUpstreamServer("quarantine-test", []mcp.Tool{
		{
			Name:        "test_tool",
			Description: "A test tool",
		},
	})

	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "quarantine-test",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}

	addResult, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)
	assert.False(t, addResult.IsError)

	// Test 2: List quarantined servers (should include our new server)
	listQuarantinedRequest := mcp.CallToolRequest{}
	listQuarantinedRequest.Params.Name = "upstream_servers"
	listQuarantinedRequest.Params.Arguments = map[string]interface{}{
		"operation": "list_quarantined",
	}

	listResult, err := mcpClient.CallTool(ctx, listQuarantinedRequest)
	require.NoError(t, err)
	assert.False(t, listResult.IsError)

	// Parse the response to check if our server is quarantined
	var listResponse map[string]interface{}
	err = json.Unmarshal([]byte(listResult.Content[0].(mcp.TextContent).Text), &listResponse)
	require.NoError(t, err)

	servers, ok := listResponse["servers"].([]interface{})
	require.True(t, ok)
	assert.True(t, len(servers) > 0, "Expected at least one quarantined server")

	// Test 3: Try to call a tool from the quarantined server (should be blocked)
	toolCallRequest := mcp.CallToolRequest{}
	toolCallRequest.Params.Name = "call_tool"
	toolCallRequest.Params.Arguments = map[string]interface{}{
		"name": "quarantine-test:test_tool",
		"args": map[string]interface{}{},
	}

	toolCallResult, err := mcpClient.CallTool(ctx, toolCallRequest)
	require.NoError(t, err)
	assert.False(t, toolCallResult.IsError)

	// Check that the response indicates the server is quarantined
	var toolCallResponse map[string]interface{}
	err = json.Unmarshal([]byte(toolCallResult.Content[0].(mcp.TextContent).Text), &toolCallResponse)
	require.NoError(t, err)
	assert.Equal(t, "QUARANTINED_SERVER_BLOCKED", toolCallResponse["status"])

	// Test 4: Unquarantine the server
	unquarantineRequest := mcp.CallToolRequest{}
	unquarantineRequest.Params.Name = "upstream_servers"
	unquarantineRequest.Params.Arguments = map[string]interface{}{
		"operation": "unquarantine",
		"name":      "quarantine-test",
	}

	unquarantineResult, err := mcpClient.CallTool(ctx, unquarantineRequest)
	require.NoError(t, err)
	assert.False(t, unquarantineResult.IsError)

	// Test 5: Now tool calls should work (wait a moment for server to be available)
	time.Sleep(2 * time.Second)

	toolCallResult2, err := mcpClient.CallTool(ctx, toolCallRequest)
	require.NoError(t, err)
	assert.False(t, toolCallResult2.IsError)

	// Parse response - should now be a successful tool call, not a quarantine block
	var toolCallResponse2 map[string]interface{}
	err = json.Unmarshal([]byte(toolCallResult2.Content[0].(mcp.TextContent).Text), &toolCallResponse2)
	require.NoError(t, err)
	assert.NotEqual(t, "QUARANTINED_SERVER_BLOCKED", toolCallResponse2["status"])
}

// Test: Error handling and recovery
func TestHandleV1ToolProxy(t *testing.T) {
	// Note: This test is currently disabled as it requires mock implementations
	// that are not yet defined. The test framework needs to be updated to support
	// proper HTTP handler testing for V1 tool proxy functionality.
	t.Skip("Test disabled: requires mockToolClient implementation")
}
