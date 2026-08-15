package server

import (
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// toolGate is ONE evaluation of the shared per-tool policy gates, consumed by
// every dispatch path (Spec 098 FR-002, plan decision 2):
//
//	call_tool_* variants   handleCallToolVariant
//	legacy call_tool       handleCallTool
//	direct mode            directCallabilityEvaluator
//	code_execution +       upstreamToolCaller.CallTool (the sandbox's bridge,
//	stored scripts (097)   shared by both script surfaces)
//
// The refusal DECISION always comes from class (preflight.ClassifyTool), so
// preflight and dispatch cannot disagree about whether a tool is callable. The
// extra fields exist so each path can keep the exact response it always
// produced: lockStatus preserves the dispatch-order preference for the
// pending/changed message over the generic "blocked" one, and configDenied
// selects the operator-policy wording.
type toolGate struct {
	serverName string
	toolName   string

	// class is the shared classification — the single authority on callability.
	class preflight.ToolClass
	// serverConfig is the stored upstream record, nil when the server is not
	// configured OR its record could not be read — storageErr tells the two
	// apart, which matters for the paths that deliberately fail OPEN on an
	// unknown server.
	serverConfig *config.ServerConfig
	// approval is the spec 032 record, nil when none exists (implicit-approved).
	approval *storage.ToolApprovalRecord
	// configDenied is the enabled_tools/disabled_tools verdict.
	configDenied bool
	// lockStatus is the tool-level quarantine lock ("" | pending | changed) as
	// the DISPATCH paths compute it: it reflects only the quarantine gate, so a
	// tool that is both user-disabled and pending still reports its lock exactly
	// as before this consolidation. class, which follows the spec-098 precedence
	// (user block outranks the quarantine lock), remains the callability truth.
	lockStatus string
	// storageErr is a genuine read failure on either stored input (the upstream
	// record or the approval record) — never the "no such record" sentinels.
	// Dispatch fails CLOSED on it (isToolCallable always has), so it is folded
	// into callable().
	storageErr error
}

// callable reports whether dispatch may proceed.
func (g toolGate) callable() bool {
	return g.class.Callable() && g.storageErr == nil
}

// serverQuarantined reports the server-level quarantine gate, which every
// dispatch path answers with the quarantine analysis response rather than a
// plain refusal.
func (g toolGate) serverQuarantined() bool {
	return g.serverConfig != nil && g.serverConfig.Quarantined
}

// blockedMessage is the agent-actionable refusal text for a non-callable tool
// that is neither quarantined nor approval-locked.
func (g toolGate) blockedMessage() string {
	return blockedToolMessageFor(g.configDenied)
}

// evaluateToolGate reads the local policy state for one tool exactly once and
// classifies it through the shared classifier.
//
// It reads the LIVE config (currentConfig) for the global quarantine switch, so
// a hot-reloaded quarantine_enabled takes effect on the next call and the
// preflight glue — which reads the same live config — cannot drift from it. In
// unit tests, where no runtime is wired, currentConfig() is the construction
// config, so behavior is unchanged there.
func (p *MCPProxyServer) evaluateToolGate(serverName, toolName string) toolGate {
	serverName, toolName = normalizeServerTool(serverName, toolName)
	gate := toolGate{serverName: serverName, toolName: toolName}

	if serverName == "" || toolName == "" {
		gate.class = preflight.ToolClassServerNotConfigured
		return gate
	}

	serverConfig, err := p.storage.GetUpstreamServer(serverName)
	if err != nil || serverConfig == nil {
		// "No such upstream" is a verdict every dispatch path has always
		// treated as not-callable. A genuine read failure is NOT that verdict:
		// it is recorded so the paths that fail open on an unknown server (the
		// sandbox bridge) can refuse instead of mistaking an unreadable record
		// for an absent one.
		if err != nil && !errors.Is(err, storage.ErrUpstreamNotFound) {
			gate.storageErr = err
		}
		gate.class = preflight.ToolClassServerNotConfigured
		return gate
	}
	gate.serverConfig = serverConfig
	gate.configDenied = p.isToolConfigDenied(serverName, toolName, serverConfig)

	approval, approvalErr := p.storage.GetToolApproval(serverName, toolName)
	switch {
	case approvalErr == nil:
		gate.approval = approval
	case errors.Is(approvalErr, storage.ErrToolApprovalNotFound):
		// No record → implicit-approved default.
	default:
		// A real BBolt failure must not silently re-enable a tool the user
		// disabled (isToolCallable's long-standing fail-closed rule).
		gate.storageErr = approvalErr
	}

	cfg := p.currentConfig()
	quarantineEnabled := cfg == nil || cfg.IsQuarantineEnabled()
	quarantineGate := quarantineEnabled && !serverConfig.IsQuarantineSkipped()
	if quarantineGate && gate.approval != nil {
		switch gate.approval.Status {
		case storage.ToolApprovalStatusPending, storage.ToolApprovalStatusChanged:
			gate.lockStatus = gate.approval.Status
		}
	}

	gate.class = preflight.ClassifyTool(preflight.ClassifyInputs{
		Server: preflight.ServerPolicy{
			Found:                  true,
			Enabled:                serverConfig.Enabled,
			Quarantined:            serverConfig.Quarantined,
			AutoApproveToolChanges: serverConfig.IsQuarantineSkipped(),
		},
		QuarantineEnabled: quarantineEnabled,
		ConfigDenied:      gate.configDenied,
		Approval:          approvalStateFor(gate.approval),
	})
	return gate
}

// approvalStateFor narrows a storage record to the classifier's read-only view.
func approvalStateFor(record *storage.ToolApprovalRecord) *preflight.ApprovalState {
	if record == nil {
		return nil
	}
	return &preflight.ApprovalState{
		Status:            record.Status,
		Disabled:          record.Disabled,
		CurrentHash:       record.CurrentHash,
		HashSchemaVersion: record.HashSchemaVersion,
	}
}
