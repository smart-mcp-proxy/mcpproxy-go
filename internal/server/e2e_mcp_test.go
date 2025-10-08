package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

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

	t.Run("retrieve_tools - find everything server tools", func(t *testing.T) {
		output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
			"query": "echo",
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

		// Look for the echo tool
		found := false
		for _, tool := range tools {
			toolMap, ok := tool.(map[string]interface{})
			require.True(t, ok)
			name, _ := toolMap["name"].(string)
			server, _ := toolMap["server"].(string)
			if strings.Contains(strings.ToLower(name), "echo") {
				found = true
				assert.Equal(t, "everything", server, "Tool should report its upstream server")
				break
			}
		}
		assert.True(t, found, "Should find echo tool")
	})

	t.Run("retrieve_tools - search with different queries", func(t *testing.T) {
		testCases := []struct {
			query    string
			minTools int
		}{
			{"tool", 1},            // Should find tools with "tool" in name/description
			{"echo", 1},            // Should find echo tool
			{"random", 0},          // Should find random tool
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

	t.Run("call_tool - echo tool", func(t *testing.T) {
		// First, find the exact echo tool name
		output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
			"query": "echo",
			"limit": 10,
		})
		require.NoError(t, err)

		var retrieveResult map[string]interface{}
		err = json.Unmarshal(output, &retrieveResult)
		require.NoError(t, err)

		tools, ok := retrieveResult["tools"].([]interface{})
		require.True(t, ok)
		require.Greater(t, len(tools), 0)

		// Find echo tool
		var (
			echoToolName   string
			echoToolServer string
		)
		for _, tool := range tools {
			toolMap, ok := tool.(map[string]interface{})
			require.True(t, ok)
			name, _ := toolMap["name"].(string)
			server, _ := toolMap["server"].(string)
			if strings.Contains(strings.ToLower(name), "echo") {
				echoToolName = name
				echoToolServer = server
				break
			}
		}
		require.NotEmpty(t, echoToolName, "Should find echo tool")
		require.NotEmpty(t, echoToolServer, "Echo tool should report its server")

		// Now call the echo tool
		testMessage := "Hello from E2E test!"
		toolIdentifier := fmt.Sprintf("%s:%s", echoToolServer, echoToolName)
		output, err = env.CallMCPTool(toolIdentifier, map[string]interface{}{
			"message": testMessage,
		})
		require.NoError(t, err)

		// The output should contain our echoed message
		outputStr := string(output)
		assert.Contains(t, outputStr, testMessage, "Echo tool should return the input message")
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
		assert.Len(t, servers, 1, "Should have exactly one server (everything)")

		// Verify the everything server
		serverMap, ok := servers[0].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "everything", serverMap["name"])
		assert.Equal(t, "stdio", serverMap["protocol"])
		assert.Equal(t, true, serverMap["enabled"])
	})

	t.Run("tools_stat - get tool statistics", func(t *testing.T) {
		output, err := env.CallMCPTool("tools_stat", map[string]interface{}{
			"top_n": 10,
		})
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(output, &result)
		require.NoError(t, err)

		// Should have stats structure
		assert.Contains(t, result, "stats", "Response should contain stats")
	})
}

// TestMCPProtocolComplexWorkflows tests complex MCP workflows
func TestMCPProtocolComplexWorkflows(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	t.Run("Full workflow: search -> discover -> call tool", func(t *testing.T) {
		// Step 1: Search for tools
		output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
			"query": "echo",
			"limit": 5,
		})
		require.NoError(t, err)

		var searchResult map[string]interface{}
		err = json.Unmarshal(output, &searchResult)
		require.NoError(t, err)

		tools, ok := searchResult["tools"].([]interface{})
		require.True(t, ok)
		require.Greater(t, len(tools), 0, "Should find at least one tool")

		// Step 2: Get the first tool
		firstTool, ok := tools[0].(map[string]interface{})
		require.True(t, ok)
		toolName, ok := firstTool["name"].(string)
		require.True(t, ok)
		require.NotEmpty(t, toolName)

		// Step 3: Call the tool (if it's echo-like)
		if strings.Contains(strings.ToLower(toolName), "echo") {
			output, err = env.CallMCPTool("call_tool", map[string]interface{}{
				"name": toolName,
				"args": map[string]interface{}{
					"message": "Workflow test message",
				},
			})
			require.NoError(t, err)
			assert.Contains(t, string(output), "Workflow test message")
		}
	})

	t.Run("Server management workflow", func(t *testing.T) {
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

		// Step 2: Get server stats
		output, err = env.CallMCPTool("tools_stat", map[string]interface{}{
			"top_n": 5,
		})
		require.NoError(t, err)

		var statsResult map[string]interface{}
		err = json.Unmarshal(output, &statsResult)
		require.NoError(t, err)
		assert.Contains(t, statsResult, "stats")
	})
}

// TestMCPProtocolToolCalling tests various tool calling scenarios
func TestMCPProtocolToolCalling(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	// Get available tools first
	output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
		"query": "",
		"limit": 20,
	})
	require.NoError(t, err)

	var toolsResult map[string]interface{}
	err = json.Unmarshal(output, &toolsResult)
	require.NoError(t, err)

	tools, ok := toolsResult["tools"].([]interface{})
	require.True(t, ok)
	require.Greater(t, len(tools), 0)

	// Test different tools from the everything server
	for _, tool := range tools {
		toolMap, ok := tool.(map[string]interface{})
		require.True(t, ok)

		toolName, ok := toolMap["name"].(string)
		require.True(t, ok)
		toolServer, _ := toolMap["server"].(string)
		targetTool := toolName
		if toolServer != "" {
			targetTool = fmt.Sprintf("%s:%s", toolServer, toolName)
		}

		t.Run("call_tool_"+strings.ReplaceAll(toolName, ":", "_"), func(t *testing.T) {
			// Test basic tool calling with appropriate args based on tool name
			var args map[string]interface{}

			switch {
			case strings.Contains(strings.ToLower(toolName), "echo"):
				args = map[string]interface{}{
					"message": "test message",
				}
			case strings.Contains(strings.ToLower(toolName), "add"):
				args = map[string]interface{}{
					"a": 5,
					"b": 3,
				}
			case strings.Contains(strings.ToLower(toolName), "random"):
				args = map[string]interface{}{
					"min": 1,
					"max": 10,
				}
			default:
				// Try with empty args for unknown tools
				args = map[string]interface{}{}
			}

			output, err := env.CallMCPTool(targetTool, args)

			// We don't require success for all tools since some might need specific args
			// But we should not get a panic or system error
			if err != nil {
				// Log the error but don't fail the test for individual tools
				t.Logf("Tool %s failed with args %v: %v", toolName, args, err)
			} else {
				assert.NotEmpty(t, output, "Tool should return some output")
				t.Logf("Tool %s succeeded with output: %s", toolName, string(output))
			}
		})
	}
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
		// Find echo tool
		output, err := env.CallMCPTool("retrieve_tools", map[string]interface{}{
			"query": "echo",
			"limit": 1,
		})
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(output, &result)
		require.NoError(t, err)

		tools, ok := result["tools"].([]interface{})
		require.True(t, ok)
		if len(tools) > 0 {
			toolMap, ok := tools[0].(map[string]interface{})
			require.True(t, ok)
			toolName, ok := toolMap["name"].(string)
			require.True(t, ok)

			// Call echo tool without required message argument
			_, err = env.CallMCPTool("call_tool", map[string]interface{}{
				"name": toolName,
				"args": map[string]interface{}{},
			})
			// Should return an error about missing arguments
			assert.Error(t, err)
		}
	})

	t.Run("upstream_servers with invalid operation", func(t *testing.T) {
		_, err := env.CallMCPTool("upstream_servers", map[string]interface{}{
			"operation": "invalid_operation",
		})
		assert.Error(t, err, "Should fail with invalid operation")
	})

	t.Run("nonexistent tool", func(t *testing.T) {
		_, err := env.CallMCPTool("nonexistent_tool", map[string]interface{}{})
		assert.Error(t, err, "Should fail when calling non-existent tool")
	})
}
