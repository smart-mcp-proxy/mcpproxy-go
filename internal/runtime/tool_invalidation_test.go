package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// toolNames extracts tool names from a list of ToolMetadata
func toolNames(tools []*config.ToolMetadata) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		// Extract tool name without server prefix
		names[i] = extractToolName(tool.Name)
	}
	return names
}

// TestToolCacheInvalidation_ToolReplacement tests the exact scenario specified:
// Initial: tool_a, tool_b → Updated: tool_c, tool_d
// Expected: Only tool_c and tool_d remain
// Failure: All four tools (a, b, c, d) remain
func TestToolCacheInvalidation_ToolReplacement(t *testing.T) {
	// Setup: Create test runtime
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:           tempDir,
		Listen:            "127.0.0.1:0",
		ToolResponseLimit: 0,
		Servers:           []*config.ServerConfig{},
	}

	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = rt.Close()
	}()

	ctx := context.Background()

	// Phase 1: Index initial tools (tool_a and tool_b)
	t.Log("Phase 1: Indexing tool_a and tool_b")
	initialTools := []*config.ToolMetadata{
		{
			ServerName:  "test-server",
			Name:        "tool_a",
			Description: "Tool A description",
			ParamsJSON:  `{"type":"object","properties":{"arg1":{"type":"string"}}}`,
			Hash:        "hash_a",
		},
		{
			ServerName:  "test-server",
			Name:        "tool_b",
			Description: "Tool B description",
			ParamsJSON:  `{"type":"object","properties":{"arg1":{"type":"string"}}}`,
			Hash:        "hash_b",
		},
	}

	err = rt.indexManager.BatchIndexTools(initialTools)
	require.NoError(t, err)

	// Verify: Only tool_a and tool_b are indexed
	indexedTools, err := rt.indexManager.GetToolsByServer("test-server")
	require.NoError(t, err)
	names := toolNames(indexedTools)

	t.Logf("Phase 1 complete: Indexed %d tools: %v", len(indexedTools), names)
	assert.Len(t, indexedTools, 2, "Should have exactly 2 tools after initial indexing")
	assert.Contains(t, names, "tool_a", "tool_a should be indexed")
	assert.Contains(t, names, "tool_b", "tool_b should be indexed")

	// Phase 2: Update server to have tool_c and tool_d instead
	t.Log("Phase 2: Updating server to tool_c and tool_d")
	newTools := []*config.ToolMetadata{
		{
			ServerName:  "test-server",
			Name:        "tool_c",
			Description: "Tool C description",
			ParamsJSON:  `{"type":"object","properties":{"arg1":{"type":"string"}}}`,
			Hash:        "hash_c",
		},
		{
			ServerName:  "test-server",
			Name:        "tool_d",
			Description: "Tool D description",
			ParamsJSON:  `{"type":"object","properties":{"arg1":{"type":"string"}}}`,
			Hash:        "hash_d",
		},
	}

	// Apply differential update (simulating what happens during discovery)
	err = rt.applyDifferentialToolUpdate(ctx, "test-server", newTools)
	require.NoError(t, err)

	// Verify: ONLY tool_c and tool_d are indexed
	// FAIL CASE: If tool_a and tool_b still exist → test fails
	indexedTools, err = rt.indexManager.GetToolsByServer("test-server")
	require.NoError(t, err)
	names = toolNames(indexedTools)

	t.Logf("Phase 2 complete: Indexed %d tools: %v", len(indexedTools), names)

	// Critical assertions - this is what the user wanted to test
	assert.Len(t, indexedTools, 2, "Should have exactly 2 tools (FAIL if 4)")
	assert.Contains(t, names, "tool_c", "tool_c should be indexed")
	assert.Contains(t, names, "tool_d", "tool_d should be indexed")
	assert.NotContains(t, names, "tool_a", "tool_a should be REMOVED (FAIL if present)")
	assert.NotContains(t, names, "tool_b", "tool_b should be REMOVED (FAIL if present)")

	t.Log("✓ Test passed: Only tool_c and tool_d remain in index")
}

// TestToolCacheInvalidation_ToolAddition tests adding tools to an existing set
func TestToolCacheInvalidation_ToolAddition(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:           tempDir,
		Listen:            "127.0.0.1:0",
		ToolResponseLimit: 0,
		Servers:           []*config.ServerConfig{},
	}

	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = rt.Close()
	}()

	ctx := context.Background()

	// Start with 2 tools
	initialTools := []*config.ToolMetadata{
		{ServerName: "test-server", Name: "tool_a", Description: "Tool A", Hash: "hash_a"},
		{ServerName: "test-server", Name: "tool_b", Description: "Tool B", Hash: "hash_b"},
	}

	err = rt.indexManager.BatchIndexTools(initialTools)
	require.NoError(t, err)

	// Add 2 more tools (total 4)
	allTools := []*config.ToolMetadata{
		{ServerName: "test-server", Name: "tool_a", Description: "Tool A", Hash: "hash_a"},
		{ServerName: "test-server", Name: "tool_b", Description: "Tool B", Hash: "hash_b"},
		{ServerName: "test-server", Name: "tool_c", Description: "Tool C", Hash: "hash_c"},
		{ServerName: "test-server", Name: "tool_d", Description: "Tool D", Hash: "hash_d"},
	}

	err = rt.applyDifferentialToolUpdate(ctx, "test-server", allTools)
	require.NoError(t, err)

	// Verify all 4 tools are present
	indexedTools, err := rt.indexManager.GetToolsByServer("test-server")
	require.NoError(t, err)

	names := toolNames(indexedTools)
	assert.Len(t, indexedTools, 4, "Should have 4 tools after addition")
	assert.Contains(t, names, "tool_a")
	assert.Contains(t, names, "tool_b")
	assert.Contains(t, names, "tool_c")
	assert.Contains(t, names, "tool_d")
}

// TestToolCacheInvalidation_ToolRemoval tests removing tools from an existing set
func TestToolCacheInvalidation_ToolRemoval(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:           tempDir,
		Listen:            "127.0.0.1:0",
		ToolResponseLimit: 0,
		Servers:           []*config.ServerConfig{},
	}

	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = rt.Close()
	}()

	ctx := context.Background()

	// Start with 4 tools
	initialTools := []*config.ToolMetadata{
		{ServerName: "test-server", Name: "tool_a", Description: "Tool A", Hash: "hash_a"},
		{ServerName: "test-server", Name: "tool_b", Description: "Tool B", Hash: "hash_b"},
		{ServerName: "test-server", Name: "tool_c", Description: "Tool C", Hash: "hash_c"},
		{ServerName: "test-server", Name: "tool_d", Description: "Tool D", Hash: "hash_d"},
	}

	err = rt.indexManager.BatchIndexTools(initialTools)
	require.NoError(t, err)

	// Remove 2 tools (keep only a and b)
	remainingTools := []*config.ToolMetadata{
		{ServerName: "test-server", Name: "tool_a", Description: "Tool A", Hash: "hash_a"},
		{ServerName: "test-server", Name: "tool_b", Description: "Tool B", Hash: "hash_b"},
	}

	err = rt.applyDifferentialToolUpdate(ctx, "test-server", remainingTools)
	require.NoError(t, err)

	// Verify only 2 tools remain
	indexedTools, err := rt.indexManager.GetToolsByServer("test-server")
	require.NoError(t, err)

	names := toolNames(indexedTools)
	assert.Len(t, indexedTools, 2, "Should have 2 tools after removal")
	assert.Contains(t, names, "tool_a")
	assert.Contains(t, names, "tool_b")
	assert.NotContains(t, names, "tool_c", "tool_c should be removed")
	assert.NotContains(t, names, "tool_d", "tool_d should be removed")
}

// TestToolCacheInvalidation_ToolModification tests modifying tool schemas
func TestToolCacheInvalidation_ToolModification(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:           tempDir,
		Listen:            "127.0.0.1:0",
		ToolResponseLimit: 0,
		Servers:           []*config.ServerConfig{},
	}

	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = rt.Close()
	}()

	ctx := context.Background()

	// Start with tool_a with hash1
	initialTools := []*config.ToolMetadata{
		{
			ServerName:  "test-server",
			Name:        "tool_a",
			Description: "Tool A version 1",
			Hash:        "hash_v1",
			ParamsJSON:  `{"type":"object","properties":{"param1":{"type":"string"}}}`,
		},
	}

	err = rt.indexManager.BatchIndexTools(initialTools)
	require.NoError(t, err)

	// Modify tool_a (same name, different schema/hash)
	modifiedTools := []*config.ToolMetadata{
		{
			ServerName:  "test-server",
			Name:        "tool_a",
			Description: "Tool A version 2 - modified",
			Hash:        "hash_v2",
			ParamsJSON:  `{"type":"object","properties":{"param1":{"type":"string"},"param2":{"type":"number"}}}`,
		},
	}

	err = rt.applyDifferentialToolUpdate(ctx, "test-server", modifiedTools)
	require.NoError(t, err)

	// Verify tool was updated (still 1 tool, but with new hash/description)
	indexedTools, err := rt.indexManager.GetToolsByServer("test-server")
	require.NoError(t, err)

	assert.Len(t, indexedTools, 1, "Should still have 1 tool")
	assert.Equal(t, "hash_v2", indexedTools[0].Hash, "Tool should have new hash")
	assert.Contains(t, indexedTools[0].Description, "version 2", "Tool should have updated description")
}

// TestToolCacheInvalidation_MultipleServers tests that changes to one server don't affect another
func TestToolCacheInvalidation_MultipleServers(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:           tempDir,
		Listen:            "127.0.0.1:0",
		ToolResponseLimit: 0,
		Servers:           []*config.ServerConfig{},
	}

	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = rt.Close()
	}()

	ctx := context.Background()

	// Server 1: tool_a, tool_b
	server1Tools := []*config.ToolMetadata{
		{ServerName: "server1", Name: "tool_a", Description: "Server 1 Tool A", Hash: "hash_1a"},
		{ServerName: "server1", Name: "tool_b", Description: "Server 1 Tool B", Hash: "hash_1b"},
	}

	// Server 2: tool_x, tool_y
	server2Tools := []*config.ToolMetadata{
		{ServerName: "server2", Name: "tool_x", Description: "Server 2 Tool X", Hash: "hash_2x"},
		{ServerName: "server2", Name: "tool_y", Description: "Server 2 Tool Y", Hash: "hash_2y"},
	}

	err = rt.indexManager.BatchIndexTools(append(server1Tools, server2Tools...))
	require.NoError(t, err)

	// Update server1 to only have tool_c
	newServer1Tools := []*config.ToolMetadata{
		{ServerName: "server1", Name: "tool_c", Description: "Server 1 Tool C", Hash: "hash_1c"},
	}

	err = rt.applyDifferentialToolUpdate(ctx, "server1", newServer1Tools)
	require.NoError(t, err)

	// Verify server1 only has tool_c
	server1Indexed, err := rt.indexManager.GetToolsByServer("server1")
	require.NoError(t, err)
	names1 := toolNames(server1Indexed)
	assert.Len(t, server1Indexed, 1)
	assert.Contains(t, names1, "tool_c")

	// Verify server2 still has tool_x and tool_y (unchanged)
	server2Indexed, err := rt.indexManager.GetToolsByServer("server2")
	require.NoError(t, err)
	names2 := toolNames(server2Indexed)
	assert.Len(t, server2Indexed, 2, "Server 2 tools should be unchanged")
	assert.Contains(t, names2, "tool_x")
	assert.Contains(t, names2, "tool_y")
}

