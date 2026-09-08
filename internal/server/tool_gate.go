package server

import (
	"errors"
	"fmt"
	"strings"

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

	approval, approvalErr := p.lookupToolApproval(serverName, toolName)
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

// lookupToolApproval reads the Spec-032 approval record for one (server, tool)
// pair, already normalized by normalizeServerTool.
//
// Two producers file records for a raw name that carries a ":" segment, and
// they key it differently: runtime's discovery producer (checkToolApprovals)
// writes every pending / changed / baseline record under extractToolName —
// everything after the first colon, so "ns:erase" lands under "erase" — while
// the user toggle (setToolEnabledNoEmit, reached from the tools REST endpoint
// and the Web UI with the raw StateView name) reads and synthesizes an
// approved record under the EXACT name. Neither producer ever sees the other's
// record. Trusting either one alone therefore fails open in one direction:
// exact-only turns a discovered tool's pending / changed lock into an implicit
// approval, and exact-first lets an approved record left behind by a toggle
// shadow a later "changed" mark the rug-pull detector wrote under the
// collapsed key. So both records are read and MERGED (mergeApprovalRecords):
// the two carry independent facts — the toggle's Disabled flag and the
// detector's pending / changed lock — and each must reach the classifier,
// which lets the user block outrank the lock for callability while dispatch
// keeps answering with the lock's review response (the toolGate.lockStatus
// contract). This is the conservative reader rule until the producers are
// made exact-name as well (Spec 105 FR-009 follow-up).
//
// Both keys are read in ONE storage snapshot (Manager.GetToolApprovals: a
// single read lock and a single read transaction). Two independent reads would
// let a pair of operator writes land between them — the exact record read
// while still approved and enabled, the collapsed one read after its lock was
// lifted — and merge into approved+enabled although the tool was locked or
// disabled at every instant. The absence of both records is reported as
// storage.ErrToolApprovalNotFound, the same contract GetToolApproval keeps.
func (p *MCPProxyServer) lookupToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	keys := []string{toolName}
	// The collapsed key may be EMPTY: the producer files a raw name that ends
	// in a colon ("ns:") under (server, "") and storage accepts that key, so
	// it is read like any other collapsed record rather than skipped.
	collapsed, hasCollapsed := "", false
	if _, rest, ok := strings.Cut(toolName, ":"); ok {
		collapsed, hasCollapsed = rest, true
		keys = append(keys, collapsed)
	}
	records, err := p.storage.GetToolApprovals(serverName, keys...)
	if err != nil {
		return nil, err
	}
	exact := records[toolName]
	var legacy *storage.ToolApprovalRecord
	if hasCollapsed {
		legacy = records[collapsed]
	}
	switch {
	case exact == nil && legacy == nil:
		return nil, fmt.Errorf("%w: %s", storage.ErrToolApprovalNotFound, storage.ToolApprovalKey(serverName, toolName))
	case exact == nil:
		return legacy, nil
	case legacy == nil:
		return exact, nil
	default:
		return mergeApprovalRecords(exact, legacy), nil
	}
}

// mergeApprovalRecords folds the exact-name and collapsed-name records for one
// raw tool into the single view preflight.ClassifyTool and the dispatch
// responses consume. The record that carries a quarantine lock (pending or
// changed) is the base, so Status and the review evidence that goes with it
// (previous / current description, hashes) come from the producer that wrote
// the lock; the exact record is the base when neither or both are locked. The
// user's Disabled flag is OR'd across both, so a toggle filed under either key
// keeps blocking. The result is a copy — the stored records are never mutated.
func mergeApprovalRecords(exact, legacy *storage.ToolApprovalRecord) *storage.ToolApprovalRecord {
	base := exact
	if approvalLocked(legacy) && !approvalLocked(exact) {
		base = legacy
	}
	merged := *base
	merged.Disabled = exact.Disabled || legacy.Disabled
	return &merged
}

// approvalLocked reports whether a record carries the tool-level quarantine
// lock (pending or changed) that preflight.ClassifyTool and the dispatch paths
// honour while the quarantine gate applies.
func approvalLocked(record *storage.ToolApprovalRecord) bool {
	return record.Status == storage.ToolApprovalStatusPending || record.Status == storage.ToolApprovalStatusChanged
}
