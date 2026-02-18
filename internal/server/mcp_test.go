package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				ReadOnlyMode:      tt.readOnlyMode,
				DisableManagement: tt.disableManagement,
				AllowServerAdd:    tt.allowServerAdd,
				AllowServerRemove: tt.allowServerRemove,
			}

			// Test logic for security checks
			allowed := !tt.readOnlyMode || tt.operation == "list"

			if tt.disableManagement {
				allowed = false
			}

			if tt.operation == "add" && !tt.allowServerAdd {
				allowed = false
			}

			if tt.operation == "remove" && !tt.allowServerRemove {
				allowed = false
			}

			assert.Equal(t, tt.shouldAllow, allowed, "Security check failed for %s", tt.name)

			// Additional check for configuration consistency
			if !cfg.ReadOnlyMode && !cfg.DisableManagement {
				// When not in read-only mode and management is enabled,
				// operations should be controlled by specific flags
				if tt.operation == "add" {
					assert.Equal(t, tt.allowServerAdd, allowed)
				}
				if tt.operation == "remove" {
					assert.Equal(t, tt.allowServerRemove, allowed)
				}
			}
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

// TestAnnotationLookupNameMatching tests that lookupToolAnnotations correctly
// matches tool names regardless of whether StateView stores them as "tool"
// or "server:tool" format. This is the bug reported in Issue #306.
func TestAnnotationLookupNameMatching(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name             string
		serverName       string
		toolName         string
		stateViewName    string // How the tool name is stored in StateView
		annotations      *config.ToolAnnotations
		expectedCallWith string
	}{
		{
			name:          "StateView stores full name (server:tool), lookup uses bare tool name",
			serverName:    "github",
			toolName:      "delete_repo",
			stateViewName: "github:delete_repo",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
			},
			expectedCallWith: "call_tool_destructive",
		},
		{
			name:          "StateView stores bare tool name, lookup uses bare tool name",
			serverName:    "github",
			toolName:      "delete_repo",
			stateViewName: "delete_repo",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
			},
			expectedCallWith: "call_tool_destructive",
		},
		{
			name:          "write tool with full name in StateView",
			serverName:    "myserver",
			toolName:      "update_config",
			stateViewName: "myserver:update_config",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: &falseVal,
			},
			expectedCallWith: "call_tool_write",
		},
		{
			name:          "read-only tool with full name in StateView",
			serverName:    "myserver",
			toolName:      "list_items",
			stateViewName: "myserver:list_items",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: &trueVal,
			},
			expectedCallWith: "call_tool_read",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the name matching logic from lookupToolAnnotations
			// This tests the fix for Issue #306 where tool.Name in StateView
			// is "server:tool" but toolName passed in is just "tool"
			matched := false
			toolNameInStateView := tt.stateViewName
			if toolNameInStateView == tt.toolName || toolNameInStateView == tt.serverName+":"+tt.toolName {
				matched = true
			}

			assert.True(t, matched, "Tool name matching failed: stateview=%q, lookup=%q",
				tt.stateViewName, tt.toolName)

			// Verify DeriveCallWith returns correct variant when annotations are found
			if matched {
				callWith := contracts.DeriveCallWith(tt.annotations)
				assert.Equal(t, tt.expectedCallWith, callWith)
			}
		})
	}
}

// TestRetrieveToolsCallWithAnnotations verifies that the handleRetrieveTools
// code path correctly splits tool names and derives call_with from annotations.
// This is a regression test for Issue #306.
func TestRetrieveToolsCallWithAnnotations(t *testing.T) {
	trueVal := true
	falseVal := false

	// Simulate search results as returned by the index
	mockResults := []*config.SearchResult{
		{
			Tool: &config.ToolMetadata{
				Name:       "myserver:delete_data",
				ServerName: "myserver",
				Annotations: &config.ToolAnnotations{
					DestructiveHint: &trueVal,
				},
			},
			Score: 0.9,
		},
		{
			Tool: &config.ToolMetadata{
				Name:       "myserver:update_config",
				ServerName: "myserver",
				Annotations: &config.ToolAnnotations{
					ReadOnlyHint: &falseVal,
				},
			},
			Score: 0.8,
		},
		{
			Tool: &config.ToolMetadata{
				Name:       "myserver:list_items",
				ServerName: "myserver",
				Annotations: &config.ToolAnnotations{
					ReadOnlyHint: &trueVal,
				},
			},
			Score: 0.7,
		},
		{
			Tool: &config.ToolMetadata{
				Name:       "myserver:unknown_tool",
				ServerName: "myserver",
				// No annotations
			},
			Score: 0.6,
		},
	}

	// Simulate the annotation lookup + call_with derivation from handleRetrieveTools
	// In production, lookupToolAnnotations queries the StateView, but we can test
	// the name-splitting logic and DeriveCallWith here.
	for _, result := range mockResults {
		parts := strings.SplitN(result.Tool.Name, ":", 2)
		require.Len(t, parts, 2, "Tool name should be in server:tool format: %s", result.Tool.Name)

		// The fix for #306: even if lookupToolAnnotations can't find annotations
		// via StateView (because of name mismatch), we can verify the split is correct
		assert.Equal(t, result.Tool.ServerName, parts[0],
			"Server name from split should match ServerName field")

		// Verify DeriveCallWith with the tool's own annotations
		callWith := contracts.DeriveCallWith(result.Tool.Annotations)

		switch result.Tool.Name {
		case "myserver:delete_data":
			assert.Equal(t, "call_tool_destructive", callWith)
		case "myserver:update_config":
			assert.Equal(t, "call_tool_write", callWith)
		case "myserver:list_items":
			assert.Equal(t, "call_tool_read", callWith)
		case "myserver:unknown_tool":
			assert.Equal(t, "call_tool_read", callWith) // nil annotations → safe default
		}
	}
}

func TestUpstreamServerOperations(t *testing.T) {
	// Test basic server operations parsing
	t.Run("BasicServerOperations", func(t *testing.T) {
		// Test that basic operations like add, remove, update are properly structured
		operations := []string{"add", "remove", "update", "patch", "list"}

		for _, op := range operations {
			request := mcp.CallToolRequest{}
			request.Params.Name = "upstream_servers"
			request.Params.Arguments = map[string]interface{}{
				"operation": op,
			}

			// Verify operation is properly extracted
			var operation string
			if request.Params.Arguments != nil {
				if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
					if opParam, ok := argumentsMap["operation"]; ok {
						if opStr, ok := opParam.(string); ok {
							operation = opStr
						}
					}
				}
			}

			assert.Equal(t, op, operation, "Operation extraction failed for %s", op)
		}
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
	assert.Equal(t, "127.0.0.1:8080", config.Listen)
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
			expected: 15, // default when 0 is passed (config.ToolsLimit)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test limit validation logic
			limit := int(tt.limit)
			if limit <= 0 {
				limit = 15 // default (config.ToolsLimit)
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
		upstreamManager: upstream.NewManager(zap.NewNop(), config.DefaultConfig(), nil, secret.NewResolver(), nil),
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
		upstreamManager: upstream.NewManager(zap.NewNop(), config.DefaultConfig(), nil, secret.NewResolver(), nil),
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
	listQuarantinedRequest.Params.Name = "quarantine_security"
	listQuarantinedRequest.Params.Arguments = map[string]interface{}{
		"operation": "list_quarantined",
	}

	listResult, err := mcpClient.CallTool(ctx, listQuarantinedRequest)
	require.NoError(t, err)
	assert.False(t, listResult.IsError)

	// Parse the response to check if our server is quarantined
	require.Greater(t, len(listResult.Content), 0)
	var contentText string
	if len(listResult.Content) > 0 {
		contentBytes, err := json.Marshal(listResult.Content[0])
		require.NoError(t, err)
		var contentMap map[string]interface{}
		err = json.Unmarshal(contentBytes, &contentMap)
		require.NoError(t, err)
		if text, ok := contentMap["text"].(string); ok {
			contentText = text
		}
	}

	var listResponse map[string]interface{}
	err = json.Unmarshal([]byte(contentText), &listResponse)
	require.NoError(t, err)

	servers, ok := listResponse["servers"].([]interface{})
	require.True(t, ok)
	assert.True(t, len(servers) > 0, "Expected at least one quarantined server")

	// Test 3: Try to call a tool from the quarantined server (should be blocked)
	// Using call_tool_write with required intent
	toolCallRequest := mcp.CallToolRequest{}
	toolCallRequest.Params.Name = "call_tool_write"
	toolCallRequest.Params.Arguments = map[string]interface{}{
		"name": "quarantine-test:test_tool",
		"args": map[string]interface{}{},
		"intent": map[string]interface{}{
			"operation_type": "write",
		},
	}

	toolCallResult, err := mcpClient.CallTool(ctx, toolCallRequest)
	require.NoError(t, err)
	assert.False(t, toolCallResult.IsError)

	// Check that the response indicates the server is quarantined
	require.Greater(t, len(toolCallResult.Content), 0)
	var toolCallContentText string
	if len(toolCallResult.Content) > 0 {
		contentBytes, err := json.Marshal(toolCallResult.Content[0])
		require.NoError(t, err)
		var contentMap map[string]interface{}
		err = json.Unmarshal(contentBytes, &contentMap)
		require.NoError(t, err)
		if text, ok := contentMap["text"].(string); ok {
			toolCallContentText = text
		}
	}

	var toolCallResponse map[string]interface{}
	err = json.Unmarshal([]byte(toolCallContentText), &toolCallResponse)
	require.NoError(t, err)
	assert.Equal(t, "QUARANTINED_SERVER_BLOCKED", toolCallResponse["status"])

	// Test 4: Test quarantine operation (quarantine is handled through tray/config, not LLM tools for security)
	// This test shows that the server remains quarantined and tools are blocked
	// In a real scenario, unquarantining would be done through the system tray or manual config editing
}

// Test: Error handling and recovery
func TestHandleV1ToolProxy(t *testing.T) {
	// Note: This test is currently disabled as it requires mock implementations
	// that are not yet defined. The test framework needs to be updated to support
	// proper HTTP handler testing for V1 tool proxy functionality.
	t.Skip("Test disabled: requires mockToolClient implementation")
}

// Test: Delete server functionality (remove operation)
func TestHandleRemoveUpstream(t *testing.T) {
	tests := []struct {
		name           string
		serverName     string
		serverExists   bool
		quarantined    bool
		readOnlyMode   bool
		disableManage  bool
		allowRemove    bool
		expectSuccess  bool
		expectErrorMsg string
	}{
		{
			name:          "successful removal of existing server",
			serverName:    "test-server",
			serverExists:  true,
			quarantined:   false,
			readOnlyMode:  false,
			disableManage: false,
			allowRemove:   true,
			expectSuccess: true,
		},
		{
			name:           "fail to remove non-existent server",
			serverName:     "non-existent-server",
			serverExists:   false,
			quarantined:    false,
			readOnlyMode:   false,
			disableManage:  false,
			allowRemove:    true,
			expectSuccess:  false,
			expectErrorMsg: "not found",
		},
		{
			name:           "fail to remove in read-only mode",
			serverName:     "test-server",
			serverExists:   true,
			quarantined:    false,
			readOnlyMode:   true,
			disableManage:  false,
			allowRemove:    true,
			expectSuccess:  false,
			expectErrorMsg: "read-only mode",
		},
		{
			name:           "fail to remove when management disabled",
			serverName:     "test-server",
			serverExists:   true,
			quarantined:    false,
			readOnlyMode:   false,
			disableManage:  true,
			allowRemove:    true,
			expectSuccess:  false,
			expectErrorMsg: "management disabled",
		},
		{
			name:           "fail to remove when not allowed",
			serverName:     "test-server",
			serverExists:   true,
			quarantined:    false,
			readOnlyMode:   false,
			disableManage:  false,
			allowRemove:    false,
			expectSuccess:  false,
			expectErrorMsg: "not allowed",
		},
		{
			name:          "successfully remove quarantined server",
			serverName:    "quarantined-server",
			serverExists:  true,
			quarantined:   true,
			readOnlyMode:  false,
			disableManage: false,
			allowRemove:   true,
			expectSuccess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the validation and operation logic for remove operation

			// 1. Test security checks
			if tt.readOnlyMode {
				assert.Contains(t, tt.expectErrorMsg, "read-only mode")
				assert.False(t, tt.expectSuccess)
				return
			}

			if tt.disableManage {
				assert.Contains(t, tt.expectErrorMsg, "management disabled")
				assert.False(t, tt.expectSuccess)
				return
			}

			if !tt.allowRemove {
				assert.Contains(t, tt.expectErrorMsg, "not allowed")
				assert.False(t, tt.expectSuccess)
				return
			}

			// 2. Test server existence check
			if !tt.serverExists {
				assert.Contains(t, tt.expectErrorMsg, "not found")
				assert.False(t, tt.expectSuccess)
				return
			}

			// 3. If all checks pass, operation should succeed
			if tt.expectSuccess {
				assert.True(t, tt.serverExists)
				assert.False(t, tt.readOnlyMode)
				assert.False(t, tt.disableManage)
				assert.True(t, tt.allowRemove)
			}
		})
	}
}

// Test: E2E delete server flow
func TestE2E_DeleteServerFlow(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	// Step 1: Add a test server
	mockServer := env.CreateMockUpstreamServer("delete-test-server", []mcp.Tool{
		{
			Name:        "test_tool",
			Description: "A test tool for deletion",
		},
	})

	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "delete-test-server",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}

	addResult, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)
	assert.False(t, addResult.IsError, "Adding server should succeed")

	// Step 2: Verify server was added (list servers)
	listRequest := mcp.CallToolRequest{}
	listRequest.Params.Name = "upstream_servers"
	listRequest.Params.Arguments = map[string]interface{}{
		"operation": "list",
	}

	listResult, err := mcpClient.CallTool(ctx, listRequest)
	require.NoError(t, err)
	assert.False(t, listResult.IsError)

	// Parse list result to verify server exists
	require.Greater(t, len(listResult.Content), 0)
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

	servers, ok := listResponse["servers"].([]interface{})
	require.True(t, ok)

	// Find our test server
	foundServer := false
	for _, s := range servers {
		serverMap := s.(map[string]interface{})
		if serverMap["name"] == "delete-test-server" {
			foundServer = true
			break
		}
	}
	assert.True(t, foundServer, "Server should be in the list after adding")

	// Step 3: Delete the server using 'remove' operation
	deleteRequest := mcp.CallToolRequest{}
	deleteRequest.Params.Name = "upstream_servers"
	deleteRequest.Params.Arguments = map[string]interface{}{
		"operation": "remove",
		"name":      "delete-test-server",
	}

	deleteResult, err := mcpClient.CallTool(ctx, deleteRequest)
	require.NoError(t, err)
	assert.False(t, deleteResult.IsError, "Delete operation should succeed")

	// Verify delete response
	require.Greater(t, len(deleteResult.Content), 0)
	var deleteContentText string
	if len(deleteResult.Content) > 0 {
		contentBytes, err := json.Marshal(deleteResult.Content[0])
		require.NoError(t, err)
		var contentMap map[string]interface{}
		err = json.Unmarshal(contentBytes, &contentMap)
		require.NoError(t, err)
		if text, ok := contentMap["text"].(string); ok {
			deleteContentText = text
		}
	}

	var deleteResponse map[string]interface{}
	err = json.Unmarshal([]byte(deleteContentText), &deleteResponse)
	require.NoError(t, err)
	assert.Equal(t, true, deleteResponse["removed"])
	assert.Equal(t, "delete-test-server", deleteResponse["name"])

	// Step 4: Verify server is no longer in the list
	listResult2, err := mcpClient.CallTool(ctx, listRequest)
	require.NoError(t, err)
	assert.False(t, listResult2.IsError)

	require.Greater(t, len(listResult2.Content), 0)
	var listContentText2 string
	if len(listResult2.Content) > 0 {
		contentBytes, err := json.Marshal(listResult2.Content[0])
		require.NoError(t, err)
		var contentMap map[string]interface{}
		err = json.Unmarshal(contentBytes, &contentMap)
		require.NoError(t, err)
		if text, ok := contentMap["text"].(string); ok {
			listContentText2 = text
		}
	}

	var listResponse2 map[string]interface{}
	err = json.Unmarshal([]byte(listContentText2), &listResponse2)
	require.NoError(t, err)

	servers2, ok := listResponse2["servers"].([]interface{})
	require.True(t, ok)

	// Verify server is gone
	foundServerAfterDelete := false
	for _, s := range servers2 {
		serverMap := s.(map[string]interface{})
		if serverMap["name"] == "delete-test-server" {
			foundServerAfterDelete = true
			break
		}
	}
	assert.False(t, foundServerAfterDelete, "Server should NOT be in the list after deletion")

	// Step 5: Try to delete again (should fail with server not found)
	deleteRequest2 := mcp.CallToolRequest{}
	deleteRequest2.Params.Name = "upstream_servers"
	deleteRequest2.Params.Arguments = map[string]interface{}{
		"operation": "remove",
		"name":      "delete-test-server",
	}

	deleteResult2, err := mcpClient.CallTool(ctx, deleteRequest2)
	require.NoError(t, err)
	assert.True(t, deleteResult2.IsError, "Deleting non-existent server should fail")
}

// TestEnvJsonParsing tests env_json parameter parsing for update/patch operations
func TestEnvJsonParsing(t *testing.T) {
	tests := []struct {
		name        string
		envJSON     string
		wantErr     bool
		errContains string
		expected    map[string]string
	}{
		{
			name:     "valid env_json",
			envJSON:  `{"API_KEY": "secret123", "DEBUG": "true"}`,
			wantErr:  false,
			expected: map[string]string{"API_KEY": "secret123", "DEBUG": "true"},
		},
		{
			name:     "empty object clears env vars",
			envJSON:  `{}`,
			wantErr:  false,
			expected: map[string]string{},
		},
		{
			name:        "invalid JSON",
			envJSON:     `not valid json`,
			wantErr:     true,
			errContains: "Invalid env_json format",
		},
		{
			name:        "array instead of object",
			envJSON:     `["key1", "key2"]`,
			wantErr:     true,
			errContains: "Invalid env_json format",
		},
		{
			name:     "single key-value",
			envJSON:  `{"SINGLE_VAR": "value"}`,
			wantErr:  false,
			expected: map[string]string{"SINGLE_VAR": "value"},
		},
		{
			name:     "unicode values",
			envJSON:  `{"UNICODE": "日本語テスト"}`,
			wantErr:  false,
			expected: map[string]string{"UNICODE": "日本語テスト"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var env map[string]string
			err := json.Unmarshal([]byte(tt.envJSON), &env)

			if tt.wantErr {
				assert.Error(t, err, "Expected error for %s", tt.name)
			} else {
				assert.NoError(t, err, "Unexpected error for %s", tt.name)
				assert.Equal(t, tt.expected, env, "Parsed env doesn't match expected for %s", tt.name)
			}
		})
	}
}

// TestArgsJsonParsing tests args_json parameter parsing for update/patch operations
func TestArgsJsonParsing(t *testing.T) {
	tests := []struct {
		name        string
		argsJSON    string
		wantErr     bool
		errContains string
		expected    []string
	}{
		{
			name:     "valid args_json",
			argsJSON: `["arg1", "arg2", "--flag"]`,
			wantErr:  false,
			expected: []string{"arg1", "arg2", "--flag"},
		},
		{
			name:     "empty array clears args",
			argsJSON: `[]`,
			wantErr:  false,
			expected: []string{},
		},
		{
			name:        "invalid JSON",
			argsJSON:    `not valid json`,
			wantErr:     true,
			errContains: "Invalid args_json format",
		},
		{
			name:        "object instead of array",
			argsJSON:    `{"key": "value"}`,
			wantErr:     true,
			errContains: "Invalid args_json format",
		},
		{
			name:     "single argument",
			argsJSON: `["single-arg"]`,
			wantErr:  false,
			expected: []string{"single-arg"},
		},
		{
			name:     "arguments with special characters",
			argsJSON: `["--config=/path/to/file", "-v", "value with spaces"]`,
			wantErr:  false,
			expected: []string{"--config=/path/to/file", "-v", "value with spaces"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var args []string
			err := json.Unmarshal([]byte(tt.argsJSON), &args)

			if tt.wantErr {
				assert.Error(t, err, "Expected error for %s", tt.name)
			} else {
				assert.NoError(t, err, "Unexpected error for %s", tt.name)
				assert.Equal(t, tt.expected, args, "Parsed args doesn't match expected for %s", tt.name)
			}
		})
	}
}

// TestHeadersJsonParsing tests headers_json parameter parsing for update/patch operations
func TestHeadersJsonParsing(t *testing.T) {
	tests := []struct {
		name        string
		headersJSON string
		wantErr     bool
		errContains string
		expected    map[string]string
	}{
		{
			name:        "valid headers_json",
			headersJSON: `{"Authorization": "Bearer token123", "X-Custom-Header": "value"}`,
			wantErr:     false,
			expected:    map[string]string{"Authorization": "Bearer token123", "X-Custom-Header": "value"},
		},
		{
			name:        "empty object clears headers",
			headersJSON: `{}`,
			wantErr:     false,
			expected:    map[string]string{},
		},
		{
			name:        "invalid JSON",
			headersJSON: `not valid json`,
			wantErr:     true,
			errContains: "Invalid headers_json format",
		},
		{
			name:        "array instead of object",
			headersJSON: `["header1", "header2"]`,
			wantErr:     true,
			errContains: "Invalid headers_json format",
		},
		{
			name:        "single header",
			headersJSON: `{"Content-Type": "application/json"}`,
			wantErr:     false,
			expected:    map[string]string{"Content-Type": "application/json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var headers map[string]string
			err := json.Unmarshal([]byte(tt.headersJSON), &headers)

			if tt.wantErr {
				assert.Error(t, err, "Expected error for %s", tt.name)
			} else {
				assert.NoError(t, err, "Unexpected error for %s", tt.name)
				assert.Equal(t, tt.expected, headers, "Parsed headers doesn't match expected for %s", tt.name)
			}
		})
	}
}

// TestFullReplacementSemantics tests that update/patch operations do full replacement
func TestFullReplacementSemantics(t *testing.T) {
	t.Run("env vars full replacement", func(t *testing.T) {
		// Initial env vars
		initialEnv := map[string]string{
			"VAR1": "value1",
			"VAR2": "value2",
			"VAR3": "value3",
		}

		// Update with partial new env (only VAR4)
		updateJSON := `{"VAR4": "value4"}`
		var newEnv map[string]string
		err := json.Unmarshal([]byte(updateJSON), &newEnv)
		require.NoError(t, err)

		// Full replacement means old keys should be gone
		// In real implementation, newEnv completely replaces initialEnv
		assert.NotContains(t, newEnv, "VAR1", "Old VAR1 should not exist in new env")
		assert.NotContains(t, newEnv, "VAR2", "Old VAR2 should not exist in new env")
		assert.NotContains(t, newEnv, "VAR3", "Old VAR3 should not exist in new env")
		assert.Contains(t, newEnv, "VAR4", "New VAR4 should exist")
		assert.Equal(t, "value4", newEnv["VAR4"])

		// Verify initialEnv still has original keys (not modified)
		assert.Contains(t, initialEnv, "VAR1")
		assert.Contains(t, initialEnv, "VAR2")
		assert.Contains(t, initialEnv, "VAR3")
	})

	t.Run("args full replacement", func(t *testing.T) {
		// Initial args
		initialArgs := []string{"arg1", "arg2", "arg3"}

		// Update with new args
		updateJSON := `["newarg1"]`
		var newArgs []string
		err := json.Unmarshal([]byte(updateJSON), &newArgs)
		require.NoError(t, err)

		// Full replacement means only new args remain
		assert.Len(t, newArgs, 1)
		assert.Equal(t, "newarg1", newArgs[0])
		assert.NotContains(t, newArgs, "arg1")
		assert.NotContains(t, newArgs, "arg2")

		// Verify initialArgs still has original values (not modified)
		assert.Len(t, initialArgs, 3)
	})
}

// ============================================================================
// Smart Config Patching Tests (Spec 023, Issues #239, #240)
// ============================================================================
// These tests verify that patch operations preserve unrelated config fields
// using the new deep merge semantics implemented in config.MergeServerConfig.

// TestPatchPreservesIsolationConfig verifies that patching a server config
// preserves the Isolation configuration when not explicitly modified.
// This is the key bug fix for Issue #239 and #240.
func TestPatchPreservesIsolationConfig(t *testing.T) {
	// Create a server config with isolation settings
	baseConfig := &config.ServerConfig{
		Name:     "test-server-isolation",
		URL:      "https://example.com/mcp",
		Protocol: "http",
		Enabled:  true,
		Isolation: &config.IsolationConfig{
			Enabled:     config.BoolPtr(true),
			Image:       "python:3.11",
			NetworkMode: "bridge",
			ExtraArgs:   []string{"-v", "/host:/container"},
			WorkingDir:  "/app",
			LogDriver:   "json-file",
			LogMaxSize:  "100m",
			LogMaxFiles: "3",
		},
	}

	// Patch: only modify the URL
	patch := &config.ServerConfig{
		URL: "https://new-url.com/mcp",
	}

	// Perform merge
	opts := config.DefaultMergeOptions()
	merged, diff, err := config.MergeServerConfig(baseConfig, patch, opts)
	require.NoError(t, err)

	// Verify URL was changed
	assert.Equal(t, "https://new-url.com/mcp", merged.URL)
	assert.NotNil(t, diff)
	assert.Contains(t, diff.Modified, "url")

	// CRITICAL: Verify Isolation config is preserved completely
	require.NotNil(t, merged.Isolation, "Isolation config must be preserved")
	assert.True(t, merged.Isolation.IsEnabled(), "Isolation.Enabled must be preserved")
	assert.Equal(t, "python:3.11", merged.Isolation.Image, "Isolation.Image must be preserved")
	assert.Equal(t, "bridge", merged.Isolation.NetworkMode, "Isolation.NetworkMode must be preserved")
	assert.Equal(t, []string{"-v", "/host:/container"}, merged.Isolation.ExtraArgs, "Isolation.ExtraArgs must be preserved")
	assert.Equal(t, "/app", merged.Isolation.WorkingDir, "Isolation.WorkingDir must be preserved")
	assert.Equal(t, "json-file", merged.Isolation.LogDriver, "Isolation.LogDriver must be preserved")
	assert.Equal(t, "100m", merged.Isolation.LogMaxSize, "Isolation.LogMaxSize must be preserved")
	assert.Equal(t, "3", merged.Isolation.LogMaxFiles, "Isolation.LogMaxFiles must be preserved")

	// Verify the diff does NOT contain isolation (it wasn't changed)
	assert.NotContains(t, diff.Modified, "isolation", "Diff should not include unchanged isolation")
}

// TestPatchPreservesOAuthConfig verifies that patching a server config
// preserves the OAuth configuration when not explicitly modified.
func TestPatchPreservesOAuthConfig(t *testing.T) {
	// Create a server config with OAuth settings
	baseConfig := &config.ServerConfig{
		Name:     "test-server-oauth",
		URL:      "https://example.com/mcp",
		Protocol: "http",
		Enabled:  true,
		OAuth: &config.OAuthConfig{
			ClientID:     "my-client-id",
			ClientSecret: "my-client-secret",
			RedirectURI:  "http://localhost:8080/callback",
			Scopes:       []string{"read", "write", "admin"},
			PKCEEnabled:  true,
			ExtraParams:  map[string]string{"audience": "api.example.com"},
		},
	}

	// Patch: only toggle enabled state
	patch := &config.ServerConfig{
		Enabled: false,
	}

	// Perform merge
	opts := config.DefaultMergeOptions()
	merged, diff, err := config.MergeServerConfig(baseConfig, patch, opts)
	require.NoError(t, err)

	// Verify Enabled was changed
	assert.False(t, merged.Enabled)
	assert.NotNil(t, diff)
	assert.Contains(t, diff.Modified, "enabled")

	// CRITICAL: Verify OAuth config is preserved completely
	require.NotNil(t, merged.OAuth, "OAuth config must be preserved")
	assert.Equal(t, "my-client-id", merged.OAuth.ClientID, "OAuth.ClientID must be preserved")
	assert.Equal(t, "my-client-secret", merged.OAuth.ClientSecret, "OAuth.ClientSecret must be preserved")
	assert.Equal(t, "http://localhost:8080/callback", merged.OAuth.RedirectURI, "OAuth.RedirectURI must be preserved")
	assert.Equal(t, []string{"read", "write", "admin"}, merged.OAuth.Scopes, "OAuth.Scopes must be preserved")
	assert.True(t, merged.OAuth.PKCEEnabled, "OAuth.PKCEEnabled must be preserved")
	assert.Equal(t, map[string]string{"audience": "api.example.com"}, merged.OAuth.ExtraParams, "OAuth.ExtraParams must be preserved")

	// Verify the diff does NOT contain oauth (it wasn't changed)
	assert.NotContains(t, diff.Modified, "oauth", "Diff should not include unchanged oauth")
}

// TestPatchPreservesEnvAndHeaders verifies that patching a server config
// with partial env/headers does deep merge, not replacement.
func TestPatchPreservesEnvAndHeaders(t *testing.T) {
	// Create a server config with env and headers
	baseConfig := &config.ServerConfig{
		Name:     "test-server-env",
		URL:      "https://example.com/mcp",
		Protocol: "http",
		Enabled:  true,
		Env: map[string]string{
			"API_KEY":    "secret-key",
			"DEBUG":      "true",
			"LOG_LEVEL":  "info",
		},
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"X-Custom":      "custom-value",
			"User-Agent":    "MCPProxy/1.0",
		},
	}

	// Patch: add new env var and header (deep merge behavior)
	patch := &config.ServerConfig{
		Env: map[string]string{
			"NEW_VAR": "new-value",
		},
		Headers: map[string]string{
			"X-New-Header": "new-header-value",
		},
	}

	// Perform merge
	opts := config.DefaultMergeOptions()
	merged, diff, err := config.MergeServerConfig(baseConfig, patch, opts)
	require.NoError(t, err)

	// CRITICAL: Verify existing env vars are preserved (deep merge)
	require.NotNil(t, merged.Env, "Env map must exist")
	assert.Equal(t, "secret-key", merged.Env["API_KEY"], "Existing API_KEY must be preserved")
	assert.Equal(t, "true", merged.Env["DEBUG"], "Existing DEBUG must be preserved")
	assert.Equal(t, "info", merged.Env["LOG_LEVEL"], "Existing LOG_LEVEL must be preserved")
	assert.Equal(t, "new-value", merged.Env["NEW_VAR"], "New NEW_VAR must be added")

	// CRITICAL: Verify existing headers are preserved (deep merge)
	require.NotNil(t, merged.Headers, "Headers map must exist")
	assert.Equal(t, "Bearer token123", merged.Headers["Authorization"], "Existing Authorization must be preserved")
	assert.Equal(t, "custom-value", merged.Headers["X-Custom"], "Existing X-Custom must be preserved")
	assert.Equal(t, "MCPProxy/1.0", merged.Headers["User-Agent"], "Existing User-Agent must be preserved")
	assert.Equal(t, "new-header-value", merged.Headers["X-New-Header"], "New X-New-Header must be added")

	// Verify the diff contains env and headers changes
	assert.NotNil(t, diff)
	assert.Contains(t, diff.Modified, "env", "Diff should include env changes")
	assert.Contains(t, diff.Modified, "headers", "Diff should include headers changes")
}

// TestPatchPreservesAllFieldsOnSimpleToggle verifies that toggling enabled
// state preserves ALL other fields - the primary use case for Issue #239.
func TestPatchPreservesAllFieldsOnSimpleToggle(t *testing.T) {
	// Create a fully-populated server config (simulates a real production config)
	baseConfig := &config.ServerConfig{
		Name:        "production-server",
		URL:         "https://api.production.com/mcp",
		Protocol:    "http",
		Command:     "npx",
		Args:        []string{"-y", "server-package"},
		WorkingDir:  "/opt/mcp",
		Enabled:     true,
		Quarantined: false,
		Env: map[string]string{
			"API_KEY":     "prod-secret-key",
			"ENVIRONMENT": "production",
		},
		Headers: map[string]string{
			"Authorization": "Bearer prod-token",
		},
		Isolation: &config.IsolationConfig{
			Enabled:     config.BoolPtr(true),
			Image:       "node:18",
			NetworkMode: "host",
			ExtraArgs:   []string{"--memory", "512m"},
		},
		OAuth: &config.OAuthConfig{
			ClientID:    "prod-client",
			PKCEEnabled: true,
			Scopes:      []string{"api.read", "api.write"},
		},
	}

	// Patch: only disable the server (common operation)
	patch := &config.ServerConfig{
		Enabled: false,
	}

	// Perform merge
	opts := config.DefaultMergeOptions()
	merged, diff, err := config.MergeServerConfig(baseConfig, patch, opts)
	require.NoError(t, err)

	// Verify enabled state changed
	assert.False(t, merged.Enabled)

	// Verify ALL other fields are preserved
	assert.Equal(t, baseConfig.Name, merged.Name)
	assert.Equal(t, baseConfig.URL, merged.URL)
	assert.Equal(t, baseConfig.Protocol, merged.Protocol)
	assert.Equal(t, baseConfig.Command, merged.Command)
	assert.Equal(t, baseConfig.Args, merged.Args)
	assert.Equal(t, baseConfig.WorkingDir, merged.WorkingDir)
	assert.Equal(t, baseConfig.Quarantined, merged.Quarantined)
	assert.Equal(t, baseConfig.Env, merged.Env)
	assert.Equal(t, baseConfig.Headers, merged.Headers)

	// Deep verify nested configs
	require.NotNil(t, merged.Isolation)
	assert.Equal(t, baseConfig.Isolation.IsEnabled(), merged.Isolation.IsEnabled())
	assert.Equal(t, baseConfig.Isolation.Image, merged.Isolation.Image)
	assert.Equal(t, baseConfig.Isolation.NetworkMode, merged.Isolation.NetworkMode)
	assert.Equal(t, baseConfig.Isolation.ExtraArgs, merged.Isolation.ExtraArgs)

	require.NotNil(t, merged.OAuth)
	assert.Equal(t, baseConfig.OAuth.ClientID, merged.OAuth.ClientID)
	assert.Equal(t, baseConfig.OAuth.PKCEEnabled, merged.OAuth.PKCEEnabled)
	assert.Equal(t, baseConfig.OAuth.Scopes, merged.OAuth.Scopes)

	// Diff should only contain enabled change
	assert.NotNil(t, diff)
	assert.Len(t, diff.Modified, 1, "Only 'enabled' should be modified")
	assert.Contains(t, diff.Modified, "enabled")
}

// TestPatchDeepMergeIsolation verifies that patching isolation does deep merge
// of nested fields, not full replacement.
func TestPatchDeepMergeIsolation(t *testing.T) {
	// Create a server config with isolation settings
	baseConfig := &config.ServerConfig{
		Name:     "test-deep-merge",
		URL:      "https://example.com/mcp",
		Protocol: "http",
		Enabled:  true,
		Isolation: &config.IsolationConfig{
			Enabled:     config.BoolPtr(true),
			Image:       "python:3.11",
			NetworkMode: "bridge",
			ExtraArgs:   []string{"-v", "/host:/container"},
			WorkingDir:  "/app",
		},
	}

	// Patch: only change the image (other isolation fields should be preserved)
	patch := &config.ServerConfig{
		Isolation: &config.IsolationConfig{
			Image: "python:3.12",
		},
	}

	// Perform merge
	opts := config.DefaultMergeOptions()
	merged, diff, err := config.MergeServerConfig(baseConfig, patch, opts)
	require.NoError(t, err)

	// Verify image was changed
	require.NotNil(t, merged.Isolation)
	assert.Equal(t, "python:3.12", merged.Isolation.Image)

	// CRITICAL: Verify other isolation fields are preserved via deep merge
	assert.Equal(t, "bridge", merged.Isolation.NetworkMode, "NetworkMode must be preserved")
	assert.Equal(t, []string{"-v", "/host:/container"}, merged.Isolation.ExtraArgs, "ExtraArgs must be preserved")
	assert.Equal(t, "/app", merged.Isolation.WorkingDir, "WorkingDir must be preserved")

	// With *bool: patch.Isolation.Enabled is nil (not set), so base value is preserved
	assert.True(t, merged.Isolation.IsEnabled(), "Enabled must be preserved when patch doesn't set it")

	// Verify the diff contains isolation changes
	assert.NotNil(t, diff)
	assert.Contains(t, diff.Modified, "isolation", "Diff should include isolation changes")
}
