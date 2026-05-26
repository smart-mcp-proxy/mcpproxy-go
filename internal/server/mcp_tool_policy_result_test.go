package server

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// These builders are the single source of truth shared by the call_tool_*
// variants and direct mode. Lock their payload shape so the two entrypoints
// cannot drift apart.

func TestToolPendingApprovalResult_Shape(t *testing.T) {
	approval := &storage.ToolApprovalRecord{CurrentDescription: "new capability"}
	res := toolPendingApprovalResult("github", "new_tool", approval)
	require.NotNil(t, res)
	assert.False(t, res.IsError)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &payload))
	assert.Equal(t, "TOOL_QUARANTINED", payload["status"])
	assert.Equal(t, "github", payload["server_name"])
	assert.Equal(t, "new_tool", payload["tool_name"])
	assert.Equal(t, "new_unapproved_tool", payload["reason"])
	assert.Equal(t, "new capability", payload["current_description"])
	assert.Contains(t, payload["action"], "/api/v1/servers/github/tools/approve")
}

func TestToolChangedApprovalResult_Shape(t *testing.T) {
	approval := &storage.ToolApprovalRecord{PreviousDescription: "old", CurrentDescription: "new"}
	res := toolChangedApprovalResult("github", "mutated_tool", approval)
	require.NotNil(t, res)
	assert.False(t, res.IsError)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &payload))
	assert.Equal(t, "TOOL_QUARANTINED", payload["status"])
	assert.Equal(t, "tool_description_changed", payload["reason"])
	assert.Equal(t, "old", payload["previous_description"])
	assert.Equal(t, "new", payload["current_description"])
}
