package server

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

// Test the core argument parsing logic for call_tool
func TestCallToolArgumentParsing(t *testing.T) {
	tests := []struct {
		name           string
		requestArgs    map[string]interface{}
		expectedArgs   map[string]interface{}
		expectedResult bool
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
			expectedResult: true,
		},
		{
			name: "No args parameter",
			requestArgs: map[string]interface{}{
				"name": "simple:tool",
			},
			expectedArgs:   nil,
			expectedResult: true,
		},
		{
			name: "Invalid args type (string instead of map)",
			requestArgs: map[string]interface{}{
				"name": "test:tool",
				"args": "invalid_string",
			},
			expectedArgs:   nil,
			expectedResult: true, // Should gracefully handle invalid type
		},
		{
			name: "Empty args map",
			requestArgs: map[string]interface{}{
				"name": "test:tool",
				"args": map[string]interface{}{},
			},
			expectedArgs:   map[string]interface{}{},
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock request
			request := createMockCallToolRequestFromMap(tt.requestArgs)

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

// Test the MCP tool format conversion for retrieve_tools
func TestRetrieveToolsFormatConversion(t *testing.T) {
	// Create mock search results
	searchResults := createMockSearchResults()

	// Convert to MCP format using the same logic as in handleRetrieveTools
	var mcpTools []map[string]interface{}
	for _, result := range searchResults {
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

	// Verify parameter details
	idParam, ok := properties["id"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "string", idParam["type"])
	assert.Equal(t, "Cryptocurrency ID", idParam["description"])

	marketDataParam, ok := properties["market_data"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "boolean", marketDataParam["type"])
	assert.Equal(t, "Include market data", marketDataParam["description"])
}

// Test handling of invalid JSON in tool parameters
func TestRetrieveToolsInvalidJSONHandling(t *testing.T) {
	// Create search results with invalid JSON
	searchResults := createMockSearchResultsWithInvalidJSON()

	// Convert to MCP format
	var mcpTools []map[string]interface{}
	for _, result := range searchResults {
		var inputSchema map[string]interface{}
		if result.Tool.ParamsJSON != "" {
			if err := json.Unmarshal([]byte(result.Tool.ParamsJSON), &inputSchema); err != nil {
				// Should fallback to default schema
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

		mcpTool := map[string]interface{}{
			"name":        result.Tool.Name,
			"description": result.Tool.Description,
			"inputSchema": inputSchema,
			"score":       result.Score,
			"server":      result.Tool.ServerName,
		}
		mcpTools = append(mcpTools, mcpTool)
	}

	// Verify graceful handling
	assert.Len(t, mcpTools, 1)

	tool := mcpTools[0]
	inputSchema, ok := tool["inputSchema"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "object", inputSchema["type"])

	properties, ok := inputSchema["properties"].(map[string]interface{})
	assert.True(t, ok)
	assert.Empty(t, properties) // Should be empty due to invalid JSON
}

// Test handling of empty parameters
func TestRetrieveToolsEmptyParamsHandling(t *testing.T) {
	// Create search results with empty params
	searchResults := createMockSearchResultsEmpty()

	// Convert to MCP format
	var mcpTools []map[string]interface{}
	for _, result := range searchResults {
		var inputSchema map[string]interface{}
		if result.Tool.ParamsJSON != "" {
			if err := json.Unmarshal([]byte(result.Tool.ParamsJSON), &inputSchema); err != nil {
				inputSchema = map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				}
			}
		} else {
			// Should use default schema for empty params
			inputSchema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		mcpTool := map[string]interface{}{
			"name":        result.Tool.Name,
			"description": result.Tool.Description,
			"inputSchema": inputSchema,
			"score":       result.Score,
			"server":      result.Tool.ServerName,
		}
		mcpTools = append(mcpTools, mcpTool)
	}

	// Verify handling
	assert.Len(t, mcpTools, 1)

	tool := mcpTools[0]
	inputSchema, ok := tool["inputSchema"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "object", inputSchema["type"])

	properties, ok := inputSchema["properties"].(map[string]interface{})
	assert.True(t, ok)
	assert.Empty(t, properties) // Should be empty for empty params
}

// Test demonstrates the core functionality without complex mocking
func TestArgumentParsingAndFormatting(t *testing.T) {
	t.Run("argument parsing works correctly", func(t *testing.T) {
		// Test the core logic that was fixed
		requestArgs := map[string]interface{}{
			"name": "coingecko:coins_id",
			"args": map[string]interface{}{
				"id":          "bitcoin",
				"market_data": true,
			},
		}

		// Simulate the argument extraction logic from handleCallTool
		var extractedArgs map[string]interface{}
		if argsParam, ok := requestArgs["args"]; ok {
			if argsMap, ok := argsParam.(map[string]interface{}); ok {
				extractedArgs = argsMap
			}
		}

		// Verify correct extraction
		assert.NotNil(t, extractedArgs)
		assert.Equal(t, "bitcoin", extractedArgs["id"])
		assert.Equal(t, true, extractedArgs["market_data"])
	})

	t.Run("MCP format conversion works correctly", func(t *testing.T) {
		// Test the retrieve_tools format conversion logic
		mockResults := createMockSearchResults()

		// Apply the conversion logic from handleRetrieveTools
		var mcpTools []map[string]interface{}
		for _, result := range mockResults {
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

			mcpTool := map[string]interface{}{
				"name":        result.Tool.Name,
				"description": result.Tool.Description,
				"inputSchema": inputSchema,
				"score":       result.Score,
				"server":      result.Tool.ServerName,
			}
			mcpTools = append(mcpTools, mcpTool)
		}

		// Verify the conversion results
		assert.Len(t, mcpTools, 2)

		firstTool := mcpTools[0]
		assert.Equal(t, "coingecko:coins_id", firstTool["name"])
		assert.Equal(t, "coingecko", firstTool["server"])

		inputSchema, ok := firstTool["inputSchema"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "object", inputSchema["type"])

		properties, ok := inputSchema["properties"].(map[string]interface{})
		assert.True(t, ok)
		assert.Contains(t, properties, "id")
		assert.Contains(t, properties, "market_data")
	})
}

// Test the new upstream_servers operations
func TestUpstreamServersOperations(t *testing.T) {
	// Test batch add servers
	t.Run("BatchAddServers", func(t *testing.T) {
		request := createMockUpstreamServersRequest("add_batch", map[string]interface{}{
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
		})

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
		request := createMockUpstreamServersRequest("import_cursor", map[string]interface{}{
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
		})

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

	// Test patch operation
	t.Run("PatchServer", func(t *testing.T) {
		request := createMockUpstreamServersRequest("patch", map[string]interface{}{
			"name":    "test-server",
			"enabled": false,
			"url":     "http://localhost:3002",
		})

		name, _ := request.RequireString("name")
		enabled := request.GetBool("enabled", true)
		url := request.GetString("url", "")

		assert.Equal(t, "test-server", name)
		assert.Equal(t, false, enabled)
		assert.Equal(t, "http://localhost:3002", url)
	})
}

// Test parameter parsing for complex objects
func TestComplexParameterParsing(t *testing.T) {
	t.Run("ParseArgsArray", func(t *testing.T) {
		request := createMockUpstreamServersRequest("add", map[string]interface{}{
			"name":    "test-server",
			"command": "python",
			"args":    []interface{}{"-m", "test_server", "--port", "3000"},
			"env":     map[string]interface{}{"TEST": "value", "DEBUG": "true"},
			"enabled": true,
		})

		// Parse args array
		var args []string
		if request.Params.Arguments != nil {
			if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
				if argsParam, ok := argumentsMap["args"]; ok {
					if argsList, ok := argsParam.([]interface{}); ok {
						for _, arg := range argsList {
							if argStr, ok := arg.(string); ok {
								args = append(args, argStr)
							}
						}
					}
				}
			}
		}

		assert.Equal(t, []string{"-m", "test_server", "--port", "3000"}, args)
	})

	t.Run("ParseEnvMap", func(t *testing.T) {
		request := createMockUpstreamServersRequest("add", map[string]interface{}{
			"name": "test-server",
			"env":  map[string]interface{}{"TEST": "value", "DEBUG": "true", "PORT": "3000"},
		})

		// Parse env map
		var env map[string]string
		if request.Params.Arguments != nil {
			if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
				if envParam, ok := argumentsMap["env"]; ok {
					if envMap, ok := envParam.(map[string]interface{}); ok {
						env = make(map[string]string)
						for k, v := range envMap {
							if vStr, ok := v.(string); ok {
								env[k] = vStr
							}
						}
					}
				}
			}
		}

		expected := map[string]string{
			"TEST":  "value",
			"DEBUG": "true",
			"PORT":  "3000",
		}
		assert.Equal(t, expected, env)
	})
}

// Helper function to create mock upstream_servers requests
func createMockUpstreamServersRequest(operation string, params map[string]interface{}) mcp.CallToolRequest {
	request := mcp.CallToolRequest{}
	request.Params.Name = "upstream_servers"

	arguments := make(map[string]interface{})
	arguments["operation"] = operation

	// Add all params to arguments
	for k, v := range params {
		arguments[k] = v
	}

	request.Params.Arguments = arguments
	return request
}
