package server

import (
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

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
func toolPendingApprovalResult(serverName, toolName string, approval *storage.ToolApprovalRecord) *mcp.CallToolResult {
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
	return mcp.NewToolResultText(string(jsonResult))
}
