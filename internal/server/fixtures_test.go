package server

import (
	"time"

	"mcpproxy-go/internal/config"

	"github.com/mark3labs/mcp-go/mcp"
)

// Test fixtures for server tests

// createMockCallToolRequest creates a mock CallToolRequest for testing
func createMockCallToolRequest(name string, args map[string]interface{}) mcp.CallToolRequest {
	request := mcp.CallToolRequest{}
	request.Params.Name = name

	// Set up the arguments properly - this is how it would come from MCP
	arguments := make(map[string]interface{})
	arguments["name"] = name
	if args != nil {
		arguments["args"] = args
	}
	request.Params.Arguments = arguments

	return request
}

// createMockCallToolRequestFromMap creates a mock CallToolRequest from a map
func createMockCallToolRequestFromMap(requestArgs map[string]interface{}) mcp.CallToolRequest {
	request := mcp.CallToolRequest{}

	if name, ok := requestArgs["name"].(string); ok {
		request.Params.Name = name
	}

	// Set up arguments - this simulates how MCP would structure the request
	request.Params.Arguments = requestArgs

	return request
}

// createMockCallToolRequestMissingName creates a CallToolRequest missing the name parameter
func createMockCallToolRequestMissingName() mcp.CallToolRequest {
	request := mcp.CallToolRequest{}
	// Deliberately not setting the name
	arguments := make(map[string]interface{})
	request.Params.Arguments = arguments

	return request
}

// createMockCallToolRequestInvalidArgs creates a CallToolRequest with invalid args format
func createMockCallToolRequestInvalidArgs() mcp.CallToolRequest {
	request := mcp.CallToolRequest{}
	request.Params.Name = "test:tool"

	// Set up arguments with invalid args format (not a map)
	arguments := make(map[string]interface{})
	arguments["name"] = "test:tool"
	arguments["args"] = "invalid_string_instead_of_map"
	request.Params.Arguments = arguments

	return request
}

// createMockRetrieveToolsRequest creates a mock request for retrieve_tools
func createMockRetrieveToolsRequest(query string, limit float64) mcp.CallToolRequest {
	request := mcp.CallToolRequest{}
	request.Params.Name = "retrieve_tools"

	arguments := make(map[string]interface{})
	arguments["query"] = query
	if limit > 0 {
		arguments["limit"] = limit
	}
	request.Params.Arguments = arguments

	return request
}

// createMockSearchResults creates mock search results for testing
func createMockSearchResults() []*config.SearchResult {
	return []*config.SearchResult{
		{
			Tool: &config.ToolMetadata{
				Name:        "coingecko:coins_id",
				ServerName:  "coingecko",
				Description: "Get detailed information about a cryptocurrency by ID",
				ParamsJSON:  `{"type":"object","properties":{"id":{"type":"string","description":"Cryptocurrency ID"},"market_data":{"type":"boolean","description":"Include market data"}}}`,
				Hash:        "abc123",
				Created:     time.Now(),
				Updated:     time.Now(),
			},
			Score: 0.95,
		},
		{
			Tool: &config.ToolMetadata{
				Name:        "weather:current",
				ServerName:  "weather",
				Description: "Get current weather information",
				ParamsJSON:  `{"type":"object","properties":{"location":{"type":"string","description":"Location to get weather for"}}}`,
				Hash:        "def456",
				Created:     time.Now(),
				Updated:     time.Now(),
			},
			Score: 0.85,
		},
	}
}

// createMockSearchResultsWithInvalidJSON creates mock search results with invalid JSON
func createMockSearchResultsWithInvalidJSON() []*config.SearchResult {
	return []*config.SearchResult{
		{
			Tool: &config.ToolMetadata{
				Name:        "invalid:tool",
				ServerName:  "invalid",
				Description: "Tool with invalid JSON schema",
				ParamsJSON:  `{"type":"object","properties":{"invalid":}`, // Invalid JSON
				Hash:        "invalid123",
				Created:     time.Now(),
				Updated:     time.Now(),
			},
			Score: 0.75,
		},
	}
}

// createMockSearchResultsEmpty creates mock search results with empty params
func createMockSearchResultsEmpty() []*config.SearchResult {
	return []*config.SearchResult{
		{
			Tool: &config.ToolMetadata{
				Name:        "empty:tool",
				ServerName:  "empty",
				Description: "Tool with empty params",
				ParamsJSON:  "", // Empty params
				Hash:        "empty123",
				Created:     time.Now(),
				Updated:     time.Now(),
			},
			Score: 0.65,
		},
	}
}

// createExpectedCallToolArgs creates the expected args for the coingecko example
func createExpectedCallToolArgs() map[string]interface{} {
	return map[string]interface{}{
		"id":          "bitcoin",
		"market_data": true,
	}
}

// createMockUpstreamResult creates a mock result from upstream server
func createMockUpstreamResult() interface{} {
	return []interface{}{
		map[string]interface{}{
			"text": "Bitcoin price is $45,000",
		},
	}
}

// createExpectedMCPToolFormat creates the expected MCP tool format for testing
func createExpectedMCPToolFormat() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "coingecko:coins_id",
			"description": "Get detailed information about a cryptocurrency by ID",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Cryptocurrency ID",
					},
					"market_data": map[string]interface{}{
						"type":        "boolean",
						"description": "Include market data",
					},
				},
			},
			"score":  0.95,
			"server": "coingecko",
		},
		{
			"name":        "weather:current",
			"description": "Get current weather information",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"location": map[string]interface{}{
						"type":        "string",
						"description": "Location to get weather for",
					},
				},
			},
			"score":  0.85,
			"server": "weather",
		},
	}
}
