package registries

import (
	"testing"
)

func TestParseAzureMCPDemo(t *testing.T) {
	// Test data based on the user's example
	sampleData := map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{
				"id":          "0d96666a-ccb9-4c1b-959a-ea5def24cf14",
				"name":        "io.github.21st-dev/magic-mcp",
				"description": "It's like v0 but in your Cursor/WindSurf/Cline. 21st dev Magic MCP server for working with your frontend like Magic",
				"repository": map[string]interface{}{
					"url":    "https://github.com/21st-dev/magic-mcp",
					"source": "github",
					"id":     "935450522",
				},
				"version_detail": map[string]interface{}{
					"version":      "0.0.1-seed",
					"release_date": "2025-05-15T04:52:52Z",
					"is_latest":    true,
				},
			},
			map[string]interface{}{
				"id":          "eb5b0c73-1ed5-4180-b0ce-2cb8a36ee3f5",
				"name":        "io.github.tinyfish-io/agentql-mcp",
				"description": "Model Context Protocol server that integrates AgentQL's data extraction capabilities.",
				"repository": map[string]interface{}{
					"url":    "https://github.com/tinyfish-io/agentql-mcp",
					"source": "github",
					"id":     "906462272",
				},
				"version_detail": map[string]interface{}{
					"version":      "0.0.1-seed",
					"release_date": "2025-05-15T04:53:00Z",
					"is_latest":    true,
				},
			},
		},
	}

	servers := parseAzureMCPDemoWithoutGuesser(sampleData)

	// Verify we parsed 2 servers
	if len(servers) != 2 {
		t.Errorf("Expected 2 servers, got %d", len(servers))
	}

	// Test first server
	if len(servers) > 0 {
		server := servers[0]
		if server.ID != "0d96666a-ccb9-4c1b-959a-ea5def24cf14" {
			t.Errorf("Expected ID '0d96666a-ccb9-4c1b-959a-ea5def24cf14', got '%s'", server.ID)
		}
		if server.Name != "io.github.21st-dev/magic-mcp" {
			t.Errorf("Expected name 'io.github.21st-dev/magic-mcp', got '%s'", server.Name)
		}
		if server.SourceCodeURL != "https://github.com/21st-dev/magic-mcp" {
			t.Errorf("Expected SourceCodeURL 'https://github.com/21st-dev/magic-mcp', got '%s'", server.SourceCodeURL)
		}
		if server.UpdatedAt != "2025-05-15T04:52:52Z" {
			t.Errorf("Expected UpdatedAt '2025-05-15T04:52:52Z', got '%s'", server.UpdatedAt)
		}
		// Check that version is added to description
		expectedDesc := "It's like v0 but in your Cursor/WindSurf/Cline. 21st dev Magic MCP server for working with your frontend like Magic (v0.0.1-seed)"
		if server.Description != expectedDesc {
			t.Errorf("Expected description to include version, got '%s'", server.Description)
		}
	}
}

func TestParseRemoteMCPServers(t *testing.T) {
	// Test data based on the user's example
	sampleData := map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{
				"id":   "github",
				"name": "GitHub MCP Server",
				"url":  "https://api.githubcopilot.com/mcp/",
				"auth": "oauth",
			},
			map[string]interface{}{
				"id":   "deepwiki",
				"name": "DeepWiki MCP Server",
				"url":  "https://mcp.deepwiki.com/mcp",
				"auth": "open",
			},
			map[string]interface{}{
				"id":   "custom-auth",
				"name": "Custom Auth Server",
				"url":  "https://api.custom.com/mcp/",
				"auth": "api-key",
			},
		},
	}

	servers := parseRemoteMCPServers(sampleData)

	// Verify we parsed 3 servers
	if len(servers) != 3 {
		t.Errorf("Expected 3 servers, got %d", len(servers))
	}

	// Test OAuth server
	if len(servers) > 0 {
		server := servers[0]
		if server.ID != "github" {
			t.Errorf("Expected ID 'github', got '%s'", server.ID)
		}
		if server.Name != "GitHub MCP Server" {
			t.Errorf("Expected name 'GitHub MCP Server', got '%s'", server.Name)
		}
		if server.URL != "https://api.githubcopilot.com/mcp/" {
			t.Errorf("Expected URL 'https://api.githubcopilot.com/mcp/', got '%s'", server.URL)
		}
		expectedDesc := "GitHub MCP Server (OAuth authentication required)"
		if server.Description != expectedDesc {
			t.Errorf("Expected description '%s', got '%s'", expectedDesc, server.Description)
		}
	}

	// Test Open access server
	if len(servers) > 1 {
		server := servers[1]
		expectedDesc := "DeepWiki MCP Server (Open access)"
		if server.Description != expectedDesc {
			t.Errorf("Expected description '%s', got '%s'", expectedDesc, server.Description)
		}
	}

	// Test Custom auth server
	if len(servers) > 2 {
		server := servers[2]
		expectedDesc := "Custom Auth Server (Authentication: api-key)"
		if server.Description != expectedDesc {
			t.Errorf("Expected description '%s', got '%s'", expectedDesc, server.Description)
		}
	}
}

func TestParseAzureMCPDemo_EmptyData(t *testing.T) {
	// Test with empty data
	emptyData := map[string]interface{}{}
	servers := parseAzureMCPDemoWithoutGuesser(emptyData)
	if len(servers) != 0 {
		t.Errorf("Expected 0 servers for empty data, got %d", len(servers))
	}

	// Test with invalid data
	invalidData := "not a map"
	servers = parseAzureMCPDemoWithoutGuesser(invalidData)
	if len(servers) != 0 {
		t.Errorf("Expected 0 servers for invalid data, got %d", len(servers))
	}
}

func TestParseRemoteMCPServers_EmptyData(t *testing.T) {
	// Test with empty data
	emptyData := map[string]interface{}{}
	servers := parseRemoteMCPServers(emptyData)
	if len(servers) != 0 {
		t.Errorf("Expected 0 servers for empty data, got %d", len(servers))
	}

	// Test with invalid data
	invalidData := "not a map"
	servers = parseRemoteMCPServers(invalidData)
	if len(servers) != 0 {
		t.Errorf("Expected 0 servers for invalid data, got %d", len(servers))
	}
}
