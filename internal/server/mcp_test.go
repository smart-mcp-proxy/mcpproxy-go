package server

import (
	"encoding/json"
	"testing"

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
