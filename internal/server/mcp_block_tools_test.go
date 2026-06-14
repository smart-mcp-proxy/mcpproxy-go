package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// createTestProxyWithRuntime builds an MCPProxyServer wired to a real Runtime so
// that handlers reaching p.mainServer.runtime (e.g. the block_tool /
// block_all_tools ops, which call runtime.BlockTools) execute their success
// path. The proxy and runtime share a single storage manager, so records seeded
// via rt.StorageManager() are the same ones the handlers mutate. Returns the
// runtime so tests can seed/verify approval records directly.
func createTestProxyWithRuntime(t *testing.T, servers []*config.ServerConfig) (*MCPProxyServer, *runtime.Runtime) {
	t.Helper()

	logger := zap.NewNop()

	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.ToolsLimit = 20
	cfg.Servers = servers

	rt, err := runtime.New(cfg, "", logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	sm := rt.StorageManager()
	require.NotNil(t, sm, "runtime must expose a storage manager")

	idx, err := index.NewManager(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	um := upstream.NewManager(logger, cfg, nil, secret.NewResolver(), nil)

	cm, err := cache.NewManager(sm.GetDB(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { cm.Close() })

	tr := truncate.NewTruncator(0)

	mainSrv := &Server{runtime: rt}
	proxy := NewMCPProxyServer(sm, idx, um, cm, tr, logger, mainSrv, false, cfg)
	return proxy, rt
}

func quarantineRequest(args map[string]interface{}) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// TestQuarantineSecurity_BlockTool_Success exercises the full dispatch +
// success path: handleQuarantineSecurity routes operation="block_tool" to
// handleBlockToolByName, which atomically approves AND disables the tool.
func TestQuarantineSecurity_BlockTool_Success(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true, Quarantined: true},
	})

	require.NoError(t, rt.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "create_issue",
		Status:             storage.ToolApprovalStatusPending,
		CurrentHash:        "h1",
		CurrentDescription: "Creates a GitHub issue",
		CurrentSchema:      `{"type":"object"}`,
	}))

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "block_tool",
		"name":      "github",
		"tool_name": "create_issue",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError, "block_tool on a real record must succeed")
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "has been blocked (approved + disabled)")

	// A block is all-or-nothing: the record must be both approved AND disabled.
	rec, err := rt.StorageManager().GetToolApproval("github", "create_issue")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status, "tool must be approved")
	assert.True(t, rec.Disabled, "tool must be disabled")
	assert.Equal(t, "mcp", rec.ApprovedBy, "block recorded the mcp actor")
}

// TestQuarantineSecurity_BlockTool_NoRecord covers the success-path branch where
// the named tool has no approval record: the op completes (no error) but reports
// that nothing was blocked.
func TestQuarantineSecurity_BlockTool_NoRecord(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true},
	})

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "block_tool",
		"name":      "github",
		"tool_name": "ghost_tool",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "nothing blocked")
}

// TestQuarantineSecurity_BlockAllTools_Success verifies dispatch +
// handleBlockAllToolsByServer blocks every pending/changed tool while leaving an
// already-approved tool untouched.
func TestQuarantineSecurity_BlockAllTools_Success(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true, Quarantined: true},
	})

	sm := rt.StorageManager()
	require.NoError(t, sm.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "create_issue",
		Status: storage.ToolApprovalStatusPending, CurrentHash: "h1",
	}))
	require.NoError(t, sm.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "delete_repo",
		Status: storage.ToolApprovalStatusChanged, CurrentHash: "h2",
	}))
	// Already approved + enabled — block_all must not touch it.
	require.NoError(t, sm.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "list_issues",
		Status: storage.ToolApprovalStatusApproved, CurrentHash: "h3", ApprovedHash: "h3",
	}))

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "block_all_tools",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Blocked 2 tool(s)")

	for _, tool := range []string{"create_issue", "delete_repo"} {
		rec, gErr := sm.GetToolApproval("github", tool)
		require.NoError(t, gErr)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status, "%s approved", tool)
		assert.True(t, rec.Disabled, "%s disabled", tool)
	}

	// The pre-approved, enabled tool stays enabled.
	rec, gErr := sm.GetToolApproval("github", "list_issues")
	require.NoError(t, gErr)
	assert.False(t, rec.Disabled, "already-approved enabled tool must stay enabled")
}

// TestQuarantineSecurity_BlockTool_MissingName proves the required-arg guard:
// handleBlockToolByName rejects a missing server name before touching the
// runtime (so this passes even with a nil runtime).
func TestQuarantineSecurity_BlockTool_MissingName(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "block_tool",
		"tool_name": "create_issue",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Missing required parameter 'name'")
}

// TestQuarantineSecurity_BlockTool_MissingToolName proves block_tool requires a
// tool_name once a server name is present.
func TestQuarantineSecurity_BlockTool_MissingToolName(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "block_tool",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Missing required parameter 'tool_name'")
}

// TestQuarantineSecurity_BlockAllTools_MissingName proves block_all_tools
// requires a server name.
func TestQuarantineSecurity_BlockAllTools_MissingName(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "block_all_tools",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Missing required parameter 'name'")
}
