package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestIsToolCallable_DisabledTool(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:     "context7",
		URL:      "https://mcp.context7.com/mcp",
		Protocol: "http",
		Enabled:  true,
	}))

	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "context7",
		ToolName:   "resolve-library-id",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	assert.False(t, proxy.isToolCallable("context7", "resolve-library-id"))
}

func TestRetrieveTools_ExcludesDisabledToolsPreRanking(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "context7", Enabled: true}))
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))

	// Mark the context7 tool as approved-but-disabled. The github tool stays
	// enabled (no approval row needed - default callable for an enabled server).
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "context7",
		ToolName:   "library-lookup",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	// Both tools share keywords from the query so BM25 returns both - that way
	// we get a real positive control: the github tool MUST survive the filter.
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name:        "context7:library-lookup",
		ServerName:  "context7",
		Description: "library documentation lookup helper",
		ParamsJSON:  "{\"type\":\"object\"}",
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name:        "github:library-lookup",
		ServerName:  "github",
		Description: "library documentation lookup helper",
		ParamsJSON:  "{\"type\":\"object\"}",
	}))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": "library lookup",
		"limit": float64(10),
	}

	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)

	text := result.Content[0].(mcp.TextContent).Text
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &payload))

	toolsValue, exists := payload["tools"]
	require.True(t, exists, "retrieve_tools response must contain a tools array")
	require.NotNil(t, toolsValue, "tools array must not be nil")

	toolsRaw, ok := toolsValue.([]interface{})
	require.True(t, ok, "tools must be a JSON array")

	names := make([]string, 0, len(toolsRaw))
	for _, item := range toolsRaw {
		tool := item.(map[string]interface{})
		names = append(names, tool["name"].(string))
	}

	// Positive control: the enabled tool must survive ranking + filtering. If the
	// disabled tool's filtering also clobbered the enabled one (regression), this
	// catches it. Negative control: the disabled tool must be gone.
	assert.Contains(t, names, "github:library-lookup",
		"enabled tool must survive ranking and filtering")
	assert.NotContains(t, names, "context7:library-lookup",
		"disabled tool must be filtered out before ranking")
}

func TestCallBlockedTool_ReturnsToolBlocked(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "context7", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "context7",
		ToolName:   "query-docs",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name": "context7:query-docs",
	}

	result, err := proxy.handleCallToolVariant(context.Background(), req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.True(t, result.IsError)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "TOOL_BLOCKED")
	assert.Contains(t, text, "Tool is disabled and not callable.")
}

func TestReenableTool_VisibleAndCallable(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "context7", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "context7",
		ToolName:   "resolve-library-id",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	assert.False(t, proxy.isToolCallable("context7", "resolve-library-id"))

	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "context7",
		ToolName:   "resolve-library-id",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   false,
	}))

	assert.True(t, proxy.isToolCallable("context7", "resolve-library-id"))
}

// TestIsToolCallable_FailsClosedOnStorageError verifies that isToolCallable is
// fail-closed: when GetToolApproval returns a real storage error (not
// ErrToolApprovalNotFound), the function must return false (not callable) so
// that a transient I/O error cannot silently re-enable a tool the user disabled.
//
// We trigger a real storage error by writing corrupt (non-JSON) bytes directly
// into the tool_approvals BBolt bucket for the target key. When GetToolApproval
// reads those bytes and json.Unmarshal fails, it returns a decode error that is
// NOT wrapped in ErrToolApprovalNotFound — exactly the "real error" case the
// fix must handle.
func TestIsToolCallable_FailsClosedOnStorageError(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	// Register an enabled server so the early-exit check passes.
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:    "badstore",
		Enabled: true,
	}))

	// Write corrupt (non-JSON) bytes directly into the tool_approvals bucket for
	// the target key. This bypasses the normal SaveToolApproval path and causes
	// GetToolApproval to return a json.UnmarshalError — a real error that is NOT
	// ErrToolApprovalNotFound.
	corruptKey := storage.ToolApprovalKey("badstore", "fragile-tool")
	err := proxy.storage.GetDB().Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(storage.ToolApprovalBucket))
		if b == nil {
			t.Fatal("tool_approvals bucket not found")
		}
		return b.Put([]byte(corruptKey), []byte("NOT VALID JSON {{{"))
	})
	require.NoError(t, err, "writing corrupt bytes into BBolt must succeed")

	// isToolCallable must return false (fail-closed) rather than true (fail-open)
	// when it encounters a real storage error reading the approval record.
	assert.False(t, proxy.isToolCallable("badstore", "fragile-tool"),
		"isToolCallable must be fail-closed on storage decode errors")
}

func TestBlockedToolMessageFor(t *testing.T) {
	cfgDenied := blockedToolMessageFor(true)
	assert.Contains(t, cfgDenied, "TOOL_BLOCKED")
	assert.Contains(t, cfgDenied, "enabled_tools/disabled_tools")
	assert.Contains(t, cfgDenied, "NOT user-overridable")
	assert.Contains(t, cfgDenied, "mcp_config.json")
	// Must NOT tell the user to flip a UI toggle — that 409s for config-denied.
	assert.NotContains(t, cfgDenied, "enable it in the mcpproxy UI")

	userDisabled := blockedToolMessageFor(false)
	assert.Contains(t, userDisabled, "TOOL_BLOCKED")
	// Preserves the legacy substring existing tests/agents key off of.
	assert.Contains(t, userDisabled, "Tool is disabled and not callable.")
	assert.Contains(t, userDisabled, "enable it in the mcpproxy UI")
}
