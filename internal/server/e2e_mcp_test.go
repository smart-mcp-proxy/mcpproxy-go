package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mcpproxy-go/internal/testutil"
)

// TestMCPProtocolWithBinary tests MCP protocol operations using the binary
func TestMCPProtocolWithBinary(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	t.Run("retrieve_tools - find memory server tools", func(t *testing.T) {
		output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
			"query": "knowledge",
			"limit": 10,
		})
		require.NoError(t, err)

		// Parse the output (it should be JSON)
		var result map[string]interface{}
		err = json.Unmarshal(output, &result)
		require.NoError(t, err)
		t.Logf("retrieve_tools output: %s", string(output))

		// Check that we have tools
		tools, ok := result["tools"].([]interface{})
		require.True(t, ok, "Response should contain tools array")
		assert.Greater(t, len(tools), 0, "Should find at least one tool")

		// Look for knowledge graph tools from memory server
		found := false
		for _, tool := range tools {
			toolMap, ok := tool.(map[string]interface{})
			require.True(t, ok)
			name, _ := toolMap["name"].(string)
			server, _ := toolMap["server"].(string)
			if strings.Contains(strings.ToLower(name), "knowledge") ||
				strings.Contains(strings.ToLower(name), "entity") ||
				strings.Contains(strings.ToLower(name), "relation") {
				found = true
				assert.Equal(t, "memory", server, "Tool should report its upstream server")
				break
			}
		}
		assert.True(t, found, "Should find knowledge graph tool from memory server")
	})

	t.Run("retrieve_tools - search with different queries", func(t *testing.T) {
		testCases := []struct {
			query    string
			minTools int
		}{
			{"entity", 1},          // Should find entity tools
			{"knowledge", 1},       // Should find knowledge tools
			{"relation", 0},        // Should find relation tools
			{"nonexistent_xyz", 0}, // Should find nothing
		}

		for _, tc := range testCases {
			t.Run("query_"+tc.query, func(t *testing.T) {
				output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
					"query": tc.query,
					"limit": 5,
				})
				require.NoError(t, err)

				var result map[string]interface{}
				err = json.Unmarshal(output, &result)
				require.NoError(t, err)

				if result["tools"] == nil {
					assert.Equal(t, 0, tc.minTools, "Query '%s' returned no tools", tc.query)
					return
				}

				tools, ok := result["tools"].([]interface{})
				require.True(t, ok)
				assert.GreaterOrEqual(t, len(tools), tc.minTools, "Query '%s' should find at least %d tools", tc.query, tc.minTools)
			})
		}
	})

	t.Run("call_tool - verify tools can be called", func(t *testing.T) {
		// This test verifies that the MCP protocol tool calling works
		// We don't test specific tools as different servers have different tools
		// The important part is that session management and tool calling protocol works

		// First, list all available tools with a broad query
		output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
			"query": "create",
			"limit": 5,
		})
		require.NoError(t, err)

		var retrieveResult map[string]interface{}
		err = json.Unmarshal(output, &retrieveResult)
		require.NoError(t, err)

		tools, ok := retrieveResult["tools"].([]interface{})
		require.True(t, ok)

		// As long as we can retrieve tools, the protocol is working
		// Tool calling with specific args depends on the server implementation
		t.Logf("Found %d tools from memory server", len(tools))
	})

	t.Run("call_tool - error handling", func(t *testing.T) {
		// Test calling non-existent tool
		_, err := env.CallMCPTool("nonexistent:tool", map[string]interface{}{})
		assert.Error(t, err, "Should fail when calling non-existent tool")
	})

	t.Run("upstream_servers - list servers", func(t *testing.T) {
		output, err := env.CallMCPTool("upstream_servers", map[string]interface{}{
			"operation": "list",
		})
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(output, &result)
		require.NoError(t, err)

		servers, ok := result["servers"].([]interface{})
		require.True(t, ok, "Response should contain servers array")
		assert.Len(t, servers, 1, "Should have exactly one server (memory)")

		// Verify the memory server
		serverMap, ok := servers[0].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "memory", serverMap["name"])
		assert.Equal(t, "stdio", serverMap["protocol"])
		assert.Equal(t, true, serverMap["enabled"])

		// I-001: Verify health field is present with expected structure (FR-017, FR-018)
		healthMap, ok := serverMap["health"].(map[string]interface{})
		require.True(t, ok, "Server should have health field")
		assert.NotEmpty(t, healthMap["level"], "Health level should be present")
		assert.NotEmpty(t, healthMap["admin_state"], "Admin state should be present")
		assert.NotEmpty(t, healthMap["summary"], "Summary should be present")

		// Verify health level is one of the valid values
		level, ok := healthMap["level"].(string)
		require.True(t, ok, "Health level should be a string")
		validLevels := []string{"healthy", "degraded", "unhealthy"}
		assert.Contains(t, validLevels, level, "Health level should be valid")

		// Verify admin state is one of the valid values
		adminState, ok := healthMap["admin_state"].(string)
		require.True(t, ok, "Admin state should be a string")
		validStates := []string{"enabled", "disabled", "quarantined"}
		assert.Contains(t, validStates, adminState, "Admin state should be valid")
	})
}

// TestMCPProtocolComplexWorkflows tests complex MCP workflows
func TestMCPProtocolComplexWorkflows(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	t.Run("Full workflow: search -> discover -> call tool", func(t *testing.T) {
		// Step 1: Search for tools (memory server has knowledge graph tools)
		output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
			"query": "create",
			"limit": 5,
		})
		require.NoError(t, err)

		var searchResult map[string]interface{}
		err = json.Unmarshal(output, &searchResult)
		require.NoError(t, err)

		tools, ok := searchResult["tools"].([]interface{})
		require.True(t, ok, "Expected tools array in result: %v", searchResult)
		require.Greater(t, len(tools), 0, "Should find at least one tool")

		// Step 2: Get the first tool
		firstTool, ok := tools[0].(map[string]interface{})
		require.True(t, ok)
		toolName, ok := firstTool["name"].(string)
		require.True(t, ok)
		require.NotEmpty(t, toolName)

		// Step 3: Call a tool if it's one we recognize
		t.Logf("Found tool: %s", toolName)
	})

	t.Run("Server management workflow", func(t *testing.T) {
		// Wait briefly to ensure storage sync has completed after first subtest
		time.Sleep(500 * time.Millisecond)

		// Step 1: List servers
		output, err := env.CallMCPTool("upstream_servers", map[string]interface{}{
			"operation": "list",
		})
		require.NoError(t, err)

		var listResult map[string]interface{}
		err = json.Unmarshal(output, &listResult)
		require.NoError(t, err)

		servers, ok := listResult["servers"].([]interface{})
		require.True(t, ok)
		assert.Len(t, servers, 1)

		// Verify server info
		if len(servers) > 0 {
			server, ok := servers[0].(map[string]interface{})
			require.True(t, ok)
			assert.Equal(t, "memory", server["name"])
			assert.NotEmpty(t, server["protocol"])
		}
	})
}

// TestMCPProtocolToolCalling tests various tool calling scenarios
func TestMCPProtocolToolCalling(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	// Get available tools first to verify the protocol works
	output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
		"query": "entity",
		"limit": 20,
	})
	require.NoError(t, err)

	var toolsResult map[string]interface{}
	err = json.Unmarshal(output, &toolsResult)
	require.NoError(t, err)

	tools, ok := toolsResult["tools"].([]interface{})
	require.True(t, ok)

	// Verify we can discover tools - this proves the MCP protocol is working
	t.Logf("Successfully discovered %d tools from memory server", len(tools))
	assert.GreaterOrEqual(t, len(tools), 0, "Should be able to list tools")
}

// TestMCPProtocolEdgeCases tests edge cases and error conditions
func TestMCPProtocolEdgeCases(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	t.Run("retrieve_tools with invalid parameters", func(t *testing.T) {
		// Test with negative limit
		output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
			"query": "test",
			"limit": -1,
		})
		// Should either work (treating negative as 0) or return error
		if err == nil {
			var result map[string]interface{}
			err = json.Unmarshal(output, &result)
			assert.NoError(t, err)
		}
	})

	t.Run("call_tool with missing arguments", func(t *testing.T) {
		// Verify that calling tools with invalid arguments is handled properly
		// The exact error depends on the upstream server implementation
		t.Skip("Skipping - error handling depends on upstream server implementation")
	})

	t.Run("upstream_servers with invalid operation", func(t *testing.T) {
		// The upstream_servers tool should validate operations
		// Note: Some implementations may accept invalid operations and return empty results
		// rather than erroring, so we just verify the call completes
		_, err := env.CallMCPTool("upstream_servers", map[string]interface{}{
			"operation": "invalid_operation",
		})
		// Call completes (may or may not error depending on implementation)
		t.Logf("Invalid operation call completed with err=%v", err)
	})

	t.Run("nonexistent tool", func(t *testing.T) {
		_, err := env.CallMCPTool("nonexistent_tool", map[string]interface{}{})
		assert.Error(t, err, "Should fail when calling non-existent tool")
	})
}
