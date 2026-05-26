package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

type directCallabilityDecision struct {
	callable       bool
	serverName     string
	toolName       string
	serverConfig   *config.ServerConfig
	approval       *storage.ToolApprovalRecord
	approvalStatus string
	configDenied   bool
	storageErr     error
}

type directCallabilityEvaluator struct {
	proxy         *MCPProxyServer
	serverConfigs map[string]*config.ServerConfig
	serverErrors  map[string]error
	approvals     map[string]*storage.ToolApprovalRecord
	approvalErrs  map[string]error
}

func newDirectCallabilityEvaluator(proxy *MCPProxyServer) *directCallabilityEvaluator {
	return &directCallabilityEvaluator{
		proxy:         proxy,
		serverConfigs: make(map[string]*config.ServerConfig),
		serverErrors:  make(map[string]error),
		approvals:     make(map[string]*storage.ToolApprovalRecord),
		approvalErrs:  make(map[string]error),
	}
}

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

	evaluator := newDirectCallabilityEvaluator(p)
	filtered := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		serverName, toolName, ok := ParseDirectToolName(tool.Name)
		if !ok {
			filtered = append(filtered, tool)
			continue
		}

		if evaluator.evaluate(serverName, toolName).callable {
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

	decision := newDirectCallabilityEvaluator(p).evaluate(serverName, toolName)
	if decision.callable {
		return nil
	}

	return p.directToolCallabilityResult(ctx, decision, args)
}

func (e *directCallabilityEvaluator) evaluate(serverName, toolName string) directCallabilityDecision {
	decision := directCallabilityDecision{
		serverName: serverName,
		toolName:   toolName,
	}

	if e.proxy.storage == nil {
		return decision
	}

	serverConfig, serverErr := e.getServerConfig(serverName)
	if serverErr != nil || serverConfig == nil {
		decision.storageErr = serverErr
		return decision
	}
	decision.serverConfig = serverConfig

	if !serverConfig.Enabled || serverConfig.Quarantined {
		return decision
	}

	if !serverConfig.IsToolAllowedByConfig(toolName) {
		decision.configDenied = true
		return decision
	}

	approval, approvalErr := e.getToolApproval(serverName, toolName)
	if approvalErr != nil && !errors.Is(approvalErr, storage.ErrToolApprovalNotFound) {
		decision.storageErr = approvalErr
		return decision
	}
	decision.approval = approval

	if e.proxy.config != nil && e.proxy.config.IsQuarantineEnabled() && !serverConfig.IsQuarantineSkipped() && approval != nil {
		switch approval.Status {
		case storage.ToolApprovalStatusPending, storage.ToolApprovalStatusChanged:
			decision.approvalStatus = approval.Status
			return decision
		}
	}

	if approval != nil && approval.Disabled {
		return decision
	}

	decision.callable = true
	return decision
}

func (e *directCallabilityEvaluator) getServerConfig(serverName string) (*config.ServerConfig, error) {
	if serverConfig, ok := e.serverConfigs[serverName]; ok {
		return serverConfig, e.serverErrors[serverName]
	}

	serverConfig, err := e.proxy.storage.GetUpstreamServer(serverName)
	e.serverConfigs[serverName] = serverConfig
	e.serverErrors[serverName] = err
	return serverConfig, err
}

func (e *directCallabilityEvaluator) getToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	key := serverName + "\x00" + toolName
	if approval, ok := e.approvals[key]; ok {
		return approval, e.approvalErrs[key]
	}
	if err, ok := e.approvalErrs[key]; ok {
		return nil, err
	}

	approval, err := e.proxy.storage.GetToolApproval(serverName, toolName)
	if approval != nil {
		e.approvals[key] = approval
	}
	e.approvalErrs[key] = err
	return approval, err
}

func (p *MCPProxyServer) directToolCallabilityResult(ctx context.Context, decision directCallabilityDecision, args map[string]interface{}) *mcp.CallToolResult {
	if decision.serverConfig != nil && decision.serverConfig.Quarantined {
		return p.handleQuarantinedToolCall(ctx, decision.serverName, decision.toolName, args)
	}

	if decision.configDenied {
		return mcp.NewToolResultError(blockedToolMessageFor(true))
	}

	if decision.approval != nil {
		switch decision.approvalStatus {
		case storage.ToolApprovalStatusPending:
			return directPendingApprovalResult(decision.serverName, decision.toolName, decision.approval)
		case storage.ToolApprovalStatusChanged:
			return directChangedApprovalResult(decision.serverName, decision.toolName, decision.approval)
		}
	}

	return mcp.NewToolResultError(p.blockedToolMessage(decision.serverName, decision.toolName))
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
	return directPolicyJSONResult(response, "pending tool approval")
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
	return directPolicyJSONResult(response, "changed tool approval")
}

func directPolicyJSONResult(response map[string]interface{}, description string) *mcp.CallToolResult {
	jsonResult, err := json.Marshal(response)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize %s response: %v", description, err))
	}
	return mcp.NewToolResultText(string(jsonResult))
}
