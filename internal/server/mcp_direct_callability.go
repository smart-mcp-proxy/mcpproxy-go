package server

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
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
		// Same catalog resolution as filterDirectModeToolsForAuth (D10). The two
		// filters run over the same listing, so if they resolved names
		// differently — one by catalog, one by first-"__" parse — a server whose
		// name contains "__" could be scope-checked as one origin and
		// callability-checked as another.
		entry, decision := p.resolveDirectTool(tool.Name)

		var serverName, toolName string
		switch decision {
		case directResolveBuiltin:
			// Built-ins are this proxy's own tools; there is no upstream
			// approval record to evaluate.
			filtered = append(filtered, tool)
			continue
		case directResolveDenied:
			continue
		case directResolveNoCatalog:
			// The parse cannot fail: a separator-less name was already classified
			// as a built-in above.
			serverName, toolName, _ = ParseDirectToolName(tool.Name)
		case directResolveFound:
			serverName, toolName = entry.ServerName, entry.ToolName
		}

		if evaluator.evaluate(serverName, toolName).callable {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

// directEntryCallable is the agent-callability half of the direct listing gate,
// for callers that already hold a resolved catalog entry (Spec 102 US2).
//
// Non-agent sessions are unfiltered here, exactly as the loop above leaves them:
// the direct listing deliberately RETAINS tool-level pending/changed/disabled
// states for an operator, and describe_tool must therefore keep describing them
// — a listed tool is never undescribable (SC-007). Only agent tokens, which
// cannot see those tools in their own listing, are gated.
func (p *MCPProxyServer) directEntryCallable(authCtx *auth.AuthContext, entry *directCatalogEntry) bool {
	if entry == nil {
		return false
	}
	if authCtx == nil || authCtx.Type != auth.AuthTypeAgent {
		return true
	}
	return newDirectCallabilityEvaluator(p).evaluate(entry.ServerName, entry.ToolName).callable
}

// directToolCallabilityBlock returns a policy response when a direct-mode tool
// is not callable. It mirrors the call_tool_* policy boundary so direct mode
// cannot bypass disabled-tool, server-quarantine, or tool-approval controls.
func (p *MCPProxyServer) directToolCallabilityBlock(ctx context.Context, serverName, toolName string, args map[string]interface{}) *mcp.CallToolResult {
	result, _ := p.directToolCallabilityBlockWithReason(ctx, serverName, toolName, args)
	return result
}

// directToolCallabilityBlockWithReason is directToolCallabilityBlock plus the
// structured reason key of the gate that fired (issue #969). Direct mode routes
// server-quarantine, pending approval, changed approval, and plain
// not-callable through a SINGLE emit site, so without carrying the key out of
// the evaluator every direct-mode block would be counted as
// tool_not_callable — the availability reason distribution these counters exist
// to measure would be wrong for the whole direct surface. Returns ("" ) when
// the tool is callable.
func (p *MCPProxyServer) directToolCallabilityBlockWithReason(ctx context.Context, serverName, toolName string, args map[string]interface{}) (*mcp.CallToolResult, string) {
	// Unit tests historically construct a minimal MCPProxyServer with no
	// storage. Preserve that narrow behavior; production servers always have
	// storage and therefore enforce the policy below.
	if p.storage == nil {
		return nil, ""
	}

	decision := newDirectCallabilityEvaluator(p).evaluate(serverName, toolName)
	if decision.callable {
		return nil, ""
	}

	return p.directToolCallabilityResult(ctx, decision, args), directBlockReasonKey(decision)
}

// directBlockReasonKey classifies a direct-mode callability block onto the
// closed telemetry.BlockReason* enum. The branches mirror
// directToolCallabilityResult exactly, so the counted reason always matches the
// payload the caller was handed.
func directBlockReasonKey(decision directCallabilityDecision) string {
	switch {
	case decision.serverConfig != nil && decision.serverConfig.Quarantined:
		return telemetry.BlockReasonServerQuarantined
	case decision.approvalStatus == storage.ToolApprovalStatusPending:
		return telemetry.BlockReasonToolPendingApproval
	case decision.approvalStatus == storage.ToolApprovalStatusChanged:
		return telemetry.BlockReasonToolChanged
	default:
		// Disabled server, config-denied tool, per-tool disable, and the
		// storage-error fallback all present as "not callable".
		return telemetry.BlockReasonToolNotCallable
	}
}

// evaluate classifies one direct-mode tool through the SHARED gate primitive
// (Spec 098 FR-002) so direct mode, the call_tool_* variants, code_execution and
// stored scripts cannot disagree about what is callable. The memoized storage
// reads below still exist because this evaluator runs over a whole tool list;
// only the classification moved.
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
	configDenied := e.proxy.isToolConfigDenied(serverName, toolName, serverConfig)
	// The server-level gates own the response when they fire, so the fields that
	// SELECT a more specific refusal — the config-denial wording and the
	// approval-lock message below — are only surfaced once the server itself is
	// past them. Pre-098 this was an early return on `!Enabled || Quarantined`;
	// the flag is the same gate, kept so a disabled server still answers
	// "server disabled" rather than "tool pending approval".
	serverGatesPassed := serverConfig.Enabled && !serverConfig.Quarantined
	if serverGatesPassed {
		decision.configDenied = configDenied
	}

	approval, approvalErr := e.getToolApproval(serverName, toolName)
	if approvalErr != nil && !errors.Is(approvalErr, storage.ErrToolApprovalNotFound) {
		decision.storageErr = approvalErr
		return decision
	}
	decision.approval = approval

	// The LIVE config, like evaluateToolGate: a hot-reloaded quarantine_enabled
	// must take effect on the next call here too, or direct mode would keep
	// waving through tools the other dispatch paths have started refusing.
	cfg := e.proxy.currentConfig()
	quarantineEnabled := cfg == nil || cfg.IsQuarantineEnabled()
	// approvalStatus drives the RESPONSE shape only, and keeps the pre-098
	// preference for the pending/changed message over the generic block, so a
	// refusal reads exactly as it always did.
	if serverGatesPassed && quarantineEnabled && !serverConfig.IsQuarantineSkipped() && approval != nil {
		switch approval.Status {
		case storage.ToolApprovalStatusPending, storage.ToolApprovalStatusChanged:
			decision.approvalStatus = approval.Status
		}
	}

	class := preflight.ClassifyTool(preflight.ClassifyInputs{
		Server: preflight.ServerPolicy{
			Found:                  true,
			Enabled:                serverConfig.Enabled,
			Quarantined:            serverConfig.Quarantined,
			AutoApproveToolChanges: serverConfig.IsQuarantineSkipped(),
		},
		QuarantineEnabled: quarantineEnabled,
		ConfigDenied:      configDenied,
		Approval:          approvalStateFor(approval),
	})
	decision.callable = class.Callable()
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
			return toolPendingApprovalResult(decision.serverName, decision.toolName, decision.approval)
		case storage.ToolApprovalStatusChanged:
			return toolChangedApprovalResult(decision.serverName, decision.toolName, decision.approval)
		}
	}

	return mcp.NewToolResultError(p.blockedToolMessage(decision.serverName, decision.toolName))
}
