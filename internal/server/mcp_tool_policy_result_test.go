package server

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
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

// TestCallToolDirect_UpstreamErrorBodyIsNotProxyRefusal pins that the typed
// quarantine refusal comes from the policy gate, never from parsing an approved
// upstream's own error text: an upstream that returns isError with a
// quarantine-shaped body was dispatched and must stay a plain error, while a
// genuine gate block stays a typed refusal with zero dispatches.
func TestCallToolDirect_UpstreamErrorBodyIsNotProxyRefusal(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
	up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))

	call := func() error {
		req := mcp.CallToolRequest{}
		req.Params.Name = contracts.ToolVariantRead
		req.Params.Arguments = map[string]interface{}{"name": "a:erase", "args": map[string]interface{}{}}
		_, err := proxy.CallToolDirect(adminCtx(), req)
		return err
	}

	for _, status := range []string{"TOOL_QUARANTINED", "QUARANTINED_SERVER_BLOCKED"} {
		before := up.count.Load()
		body := `{"status":"` + status + `","message":"upstream says so"}`
		up.errBody.Store(&body)
		err := call()
		require.Error(t, err, status)
		var refusal *profile.ToolBlockedError
		assert.False(t, errors.As(err, &refusal), "%s from the upstream must not become a proxy refusal", status)
		assert.Equal(t, before+1, up.count.Load(), "%s: the call was dispatched", status)
	}
	up.errBody.Store(nil)

	// Genuine gate block: pending approval -> typed refusal, zero dispatches.
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusPending,
	}))
	before := up.count.Load()
	err := call()
	var refusal *profile.ToolBlockedError
	if assert.ErrorAs(t, err, &refusal) {
		assert.Equal(t, blockReasonToolQuarantined, refusal.Reason)
	}
	assert.Equal(t, before, up.count.Load(), "a gate block never dispatches")
}
