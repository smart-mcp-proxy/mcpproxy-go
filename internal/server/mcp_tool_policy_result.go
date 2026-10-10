package server

import (
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Shared tool-policy block results.
//
// The call_tool_* variants (handleCallToolVariant) and direct mode
// (directToolCallabilityBlock) must return byte-identical block payloads for the
// same policy decision — otherwise an agent sees different remediation depending
// on which entrypoint it used, and the two paths drift over time. These builders
// are the single source of truth for the pending/changed quarantine responses so
// both entrypoints stay in lock-step.

// toolPendingApprovalResult builds the TOOL_QUARANTINED response for a tool that
// has never been approved (new, unapproved tool).
//
// A record synthesized by implicitPendingApproval (Spec 105 FR-009: a tool the
// discovery snapshot contains with NO stored record, under an active gate)
// gets a distinct body: there is nothing in the review UI to approve yet, so
// pointing the agent at the approve endpoint would be a dead end. The
// server's next discovery pass files the real record.
func toolPendingApprovalResult(serverName, toolName string, approval *storage.ToolApprovalRecord) *mcp.CallToolResult {
	if isImplicitPendingApproval(approval) {
		return toolPolicyJSONResult(map[string]interface{}{
			"status":              "TOOL_QUARANTINED",
			"server_name":         serverName,
			"tool_name":           toolName,
			"reason":              "no_approval_record",
			"message":             fmt.Sprintf("Tool '%s:%s' is in the server's tool list but has no approval record yet, so it cannot be called while tool-level quarantine is active for the server.", serverName, toolName),
			"current_description": approval.CurrentDescription,
			"action":              "No approval record exists for this tool yet. " + preflight.NoApprovalRecordRemediation(serverName),
		}, "pending tool approval")
	}
	response := map[string]interface{}{
		"status":              "TOOL_QUARANTINED",
		"server_name":         serverName,
		"tool_name":           toolName,
		"reason":              "new_unapproved_tool",
		"message":             fmt.Sprintf("Tool '%s:%s' has not been approved yet. New tools must be inspected and approved before use.", serverName, toolName),
		"current_description": approval.CurrentDescription,
		"action":              fmt.Sprintf("Approve via: POST /api/v1/servers/%s/tools/approve or mcpproxy upstream inspect %s", serverName, serverName),
	}
	return toolPolicyJSONResult(response, "pending tool approval")
}

// toolChangedApprovalResult builds the TOOL_QUARANTINED response for a tool whose
// description/schema changed since it was last approved (rug-pull detection).
func toolChangedApprovalResult(serverName, toolName string, approval *storage.ToolApprovalRecord) *mcp.CallToolResult {
	response := map[string]interface{}{
		"status":               "TOOL_QUARANTINED",
		"server_name":          serverName,
		"tool_name":            toolName,
		"reason":               "tool_description_changed",
		"message":              fmt.Sprintf("Tool '%s:%s' description has changed since last approval. Inspect changes before using.", serverName, toolName),
		"previous_description": approval.PreviousDescription,
		"current_description":  approval.CurrentDescription,
		"action":               fmt.Sprintf("Approve via: POST /api/v1/servers/%s/tools/approve or mcpproxy upstream inspect %s", serverName, serverName),
	}
	return toolPolicyJSONResult(response, "changed tool approval")
}

// toolPolicyJSONResult serializes a policy response map into a tool result,
// degrading to an error result if serialization fails.
func toolPolicyJSONResult(response map[string]interface{}, description string) *mcp.CallToolResult {
	jsonResult, err := json.Marshal(response)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize %s response: %v", description, err))
	}
	// The JSON body stays parseable (status/reason/action survive), but the
	// result is flagged isError so MCP clients treat a policy block as a failed
	// call (UX-05) — consistent with every other refusal shape.
	res := mcp.NewToolResultText(string(jsonResult))
	res.IsError = true
	return res
}

// Block reasons for quarantine refusals surfaced through REST (UX-05).
const (
	blockReasonServerQuarantined profile.BlockReason = "server_quarantined"
	blockReasonToolQuarantined   profile.BlockReason = "tool_quarantined"
)

// quarantineRefusalFromText recognizes the QUARANTINED_SERVER_BLOCKED and
// TOOL_QUARANTINED policy bodies built in this package and returns them as a
// typed *profile.ToolBlockedError so the HTTP layer can answer 403 with the
// payload instead of flattening them into a 500. The echoed request arguments
// (requestedArgs) are dropped from the error message: they are caller data
// that may carry secrets, and an error string ends up in logs and CLI stderr.
// Returns nil for any other text.
func quarantineRefusalFromText(text string) *profile.ToolBlockedError {
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		return nil
	}
	var reason profile.BlockReason
	switch body["status"] {
	case "QUARANTINED_SERVER_BLOCKED":
		reason = blockReasonServerQuarantined
	case "TOOL_QUARANTINED":
		reason = blockReasonToolQuarantined
	default:
		return nil
	}
	delete(body, "requestedArgs")
	redacted, err := json.Marshal(body)
	if err != nil {
		return nil
	}
	return &profile.ToolBlockedError{Reason: reason, Message: string(redacted)}
}
