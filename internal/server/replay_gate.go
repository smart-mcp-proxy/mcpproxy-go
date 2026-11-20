package server

// replay_gate.go — the REST replay door's refusals from the shared per-tool
// gate (toolGate). Replay is a dispatch path like handleCallToolVariant, so a
// tool the gate would not let an agent call (server quarantined or disabled,
// tool pending / changed, enabled_tools / disabled_tools, review-dirty) must
// not be reachable through POST /tool-calls/{id}/replay either, for ANY
// caller. Refusals are typed *profile.ToolBlockedError (httpapi answers 403).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// refuseReplay records a blocked policy decision for the replayed (server,
// tool) pair and returns the typed refusal.
func (p *MCPProxyServer) refuseReplay(ctx context.Context, original *contracts.ToolCallRecord, requestID string, reason profile.BlockReason, message, telemetryReason string) error {
	p.emitActivityPolicyDecision(ctx, original.ServerName, original.ToolName,
		sessionIDFromContext(ctx), requestID, "blocked", message, telemetryReason)
	return &profile.ToolBlockedError{Reason: reason, Message: message}
}

// replayGateRefusal answers the shared gate's verdict for a replay in the
// order handleCallToolVariant answers it: server quarantine, tool-level
// quarantine lock (pending / changed), then the generic not-callable verdict
// (disabled, enabled_tools / disabled_tools, server disabled, storage error,
// review-dirty). It returns nil when the tool is callable.
func (p *MCPProxyServer) replayGateRefusal(ctx context.Context, gate toolGate, requestID string) error {
	original := &contracts.ToolCallRecord{ServerName: gate.serverName, ToolName: gate.toolName}
	if gate.serverQuarantined() {
		message := fmt.Sprintf("Server '%s' is quarantined for security review: its tool calls are blocked until it is approved", gate.serverName)
		body, _ := json.Marshal(map[string]interface{}{"status": "QUARANTINED_SERVER_BLOCKED", "serverName": gate.serverName, "toolName": gate.toolName, "message": message})
		return p.refuseReplay(ctx, original, requestID, blockReasonServerQuarantined, string(body), telemetry.BlockReasonServerQuarantined)
	}
	switch gate.lockStatus {
	case storage.ToolApprovalStatusPending:
		return p.refuseReplayQuarantine(ctx, original, requestID, toolPendingApprovalResult(gate.serverName, gate.toolName, gate.approval), telemetry.BlockReasonToolPendingApproval)
	case storage.ToolApprovalStatusChanged:
		return p.refuseReplayQuarantine(ctx, original, requestID, toolChangedApprovalResult(gate.serverName, gate.toolName, gate.approval), telemetry.BlockReasonToolChanged)
	}
	if !gate.callable() {
		return p.refuseReplay(ctx, original, requestID, replayBlockReasonToolNotCallable, gate.blockedMessage(), telemetry.BlockReasonToolNotCallable)
	}
	return nil
}

// refuseReplayQuarantine converts the proxy's own TOOL_QUARANTINED body (built
// at the gate, never upstream output) into the typed 403 refusal.
func (p *MCPProxyServer) refuseReplayQuarantine(ctx context.Context, original *contracts.ToolCallRecord, requestID string, res *mcp.CallToolResult, telemetryReason string) error {
	message := "Tool is quarantined pending approval"
	reason := blockReasonToolQuarantined
	if len(res.Content) > 0 {
		if text, ok := res.Content[0].(mcp.TextContent); ok {
			if refusal := quarantineRefusalFromText(text.Text); refusal != nil {
				message, reason = refusal.Message, refusal.Reason
			}
		}
	}
	return p.refuseReplay(ctx, original, requestID, reason, message, telemetryReason)
}
