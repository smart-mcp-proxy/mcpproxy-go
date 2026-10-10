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
	assert.True(t, res.IsError, "policy blocks are flagged isError (UX-05)")

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
	assert.True(t, res.IsError, "policy blocks are flagged isError (UX-05)")

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &payload))
	assert.Equal(t, "TOOL_QUARANTINED", payload["status"])
	assert.Equal(t, "tool_description_changed", payload["reason"])
	assert.Equal(t, "old", payload["previous_description"])
	assert.Equal(t, "new", payload["current_description"])
}

// Spec 105 FR-009: the record implicitPendingApproval synthesizes for a
// snapshot tool with NO stored record answers a distinct body — nothing is
// listed for review yet, so the standard approve-endpoint remediation would
// be a dead end; the agent is told the record is filed on the server's next
// discovery pass and how to trigger one.
func TestToolPendingApprovalResult_ImplicitNoRecordShape(t *testing.T) {
	approval := implicitPendingApproval("github", "new_tool", true, "new capability", true)
	require.NotNil(t, approval)
	require.True(t, isImplicitPendingApproval(approval))
	res := toolPendingApprovalResult("github", "new_tool", approval)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "policy blocks are flagged isError (UX-05)")

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &payload))
	assert.Equal(t, "TOOL_QUARANTINED", payload["status"])
	assert.Equal(t, "github", payload["server_name"])
	assert.Equal(t, "new_tool", payload["tool_name"])
	assert.Equal(t, "no_approval_record", payload["reason"])
	assert.Equal(t, "new capability", payload["current_description"])
	assert.Contains(t, payload["action"], "next discovery pass")
	assert.Contains(t, payload["action"], "refresh")
	assert.NotContains(t, payload["action"], "/tools/approve", "there is no record to approve yet")

	// A stored pending record is never mistaken for the implicit one, and
	// the gate-off / undiscovered inputs synthesize nothing.
	assert.False(t, isImplicitPendingApproval(&storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusPending}))
	assert.Nil(t, implicitPendingApproval("github", "new_tool", true, "", false), "gate off")
	assert.Nil(t, implicitPendingApproval("github", "new_tool", false, "", true), "not discovered")
}

func TestQuarantineRefusalFromText(t *testing.T) {
	pending := toolPendingApprovalResult("github", "new_tool", &storage.ToolApprovalRecord{CurrentDescription: "d"})
	r := quarantineRefusalFromText(pending.Content[0].(mcp.TextContent).Text)
	require.NotNil(t, r)
	assert.Equal(t, blockReasonToolQuarantined, r.Reason)
	assert.Contains(t, r.Message, "TOOL_QUARANTINED")

	body, err := json.Marshal(map[string]interface{}{
		"status":        "QUARANTINED_SERVER_BLOCKED",
		"serverName":    "s",
		"requestedArgs": map[string]interface{}{"token": "sk-secret"},
	})
	require.NoError(t, err)
	r = quarantineRefusalFromText(string(body))
	require.NotNil(t, r)
	assert.Equal(t, blockReasonServerQuarantined, r.Reason)
	assert.NotContains(t, r.Message, "sk-secret", "echoed args never reach the REST error text")
	assert.NotContains(t, r.Message, "requestedArgs")
	assert.Contains(t, r.Message, "QUARANTINED_SERVER_BLOCKED")

	assert.Nil(t, quarantineRefusalFromText("plain upstream error"))
	assert.Nil(t, quarantineRefusalFromText(`{"status":"OK"}`))
}
