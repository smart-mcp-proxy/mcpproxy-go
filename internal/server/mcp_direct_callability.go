package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// filterDirectToolsForAgentCallability hides direct-mode tools that an agent
// token cannot actually invoke because they are disabled, quarantined, pending
// approval, or changed since approval. Non-agent contexts keep the existing
// operator-visible discovery behavior.
func (p *MCPProxyServer) filterDirectToolsForAgentCallability(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	if len(tools) == 0 {
		return tools
	}

	authCtx := auth.AuthContextFromContext(ctx)
	if authCtx == nil || authCtx.Type != auth.AuthTypeAgent {
		return tools
	}

	filtered := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		serverName, toolName, ok := ParseDirectToolName(tool.Name)
		if !ok {
			filtered = append(filtered, tool)
			continue
		}

		if p.isDirectToolCallable(serverName, toolName) {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

// directToolCallabilityBlock returns a policy response when a direct-mode tool
// is not callable. It mirrors the call_tool_* policy boundary so direct mode
// cannot bypass disabled-tool, server-quarantine, or tool-approval controls.
func (p *MCPProxyServer) directToolCallabilityBlock(ctx context.Context, serverName, toolName string, args map[string]interface{}) *mcp.CallToolResult {
	// Unit tests historically construct a minimal MCPProxyServer with no
	// storage. Preserve that narrow behavior; production servers always have
	// storage and therefore enforce the policy below.
	if p.storage == nil {
		return nil
	}

	serverConfig, err := p.storage.GetUpstreamServer(serverName)
	if err != nil || serverConfig == nil {
		return mcp.NewToolResultError(p.blockedToolMessage(serverName, toolName))
	}

	if serverConfig.Quarantined {
		return p.handleQuarantinedToolCall(ctx, serverName, toolName, args)
	}

	if !serverConfig.IsToolAllowedByConfig(toolName) {
		return mcp.NewToolResultError(blockedToolMessageFor(true))
	}

	if p.config != nil && p.config.IsQuarantineEnabled() && !serverConfig.IsQuarantineSkipped() {
		approval, approvalErr := p.storage.GetToolApproval(serverName, toolName)
		if approvalErr == nil && approval != nil {
			switch approval.Status {
			case storage.ToolApprovalStatusPending:
				return directPendingApprovalResult(serverName, toolName, approval)
			case storage.ToolApprovalStatusChanged:
				return directChangedApprovalResult(serverName, toolName, approval)
			}
		}
	}

	if !p.isToolCallable(serverName, toolName) {
		return mcp.NewToolResultError(p.blockedToolMessage(serverName, toolName))
	}

	return nil
}

// isDirectToolCallable is the direct-mode discovery predicate. It includes the
// generic callable check plus tool-level pending/changed quarantine, because
// isToolCallable intentionally handles only disabled/config/server state.
func (p *MCPProxyServer) isDirectToolCallable(serverName, toolName string) bool {
	if p.storage == nil || !p.isToolCallable(serverName, toolName) {
		return false
	}

	serverConfig, err := p.storage.GetUpstreamServer(serverName)
	if err != nil || serverConfig == nil || serverConfig.Quarantined {
		return false
	}

	if p.config != nil && p.config.IsQuarantineEnabled() && !serverConfig.IsQuarantineSkipped() {
		approval, approvalErr := p.storage.GetToolApproval(serverName, toolName)
		if approvalErr == nil && approval != nil {
			return approval.Status != storage.ToolApprovalStatusPending &&
				approval.Status != storage.ToolApprovalStatusChanged
		}
	}

	return true
}

func directPendingApprovalResult(serverName, toolName string, approval *storage.ToolApprovalRecord) *mcp.CallToolResult {
	response := map[string]interface{}{
		"status":              "TOOL_QUARANTINED",
		"server_name":         serverName,
		"tool_name":           toolName,
		"reason":              "new_unapproved_tool",
		"message":             fmt.Sprintf("Tool '%s:%s' has not been approved yet. New tools must be inspected and approved before use.", serverName, toolName),
		"current_description": approval.CurrentDescription,
		"action":              fmt.Sprintf("Approve via: POST /api/v1/servers/%s/tools/approve or mcpproxy upstream inspect %s", serverName, serverName),
	}
	jsonResult, _ := json.Marshal(response)
	return mcp.NewToolResultText(string(jsonResult))
}

func directChangedApprovalResult(serverName, toolName string, approval *storage.ToolApprovalRecord) *mcp.CallToolResult {
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
	jsonResult, _ := json.Marshal(response)
	return mcp.NewToolResultText(string(jsonResult))
}
