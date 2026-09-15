package server

import (
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

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
//
// The pair is normalized here for callers that hold a canonical "server:tool"
// id. A caller that already holds a SPLIT pair (every dispatch path, and a
// resolver that normalized once itself) must use evaluateExactToolGate.
func (p *MCPProxyServer) evaluateToolGate(serverName, toolName string) toolGate {
	serverName, toolName = normalizeServerTool(serverName, toolName)
	return p.evaluateExactToolGate(serverName, toolName)
}

// evaluateExactToolGate is evaluateToolGate for a pair that is already split
// into server and RAW tool name. It never re-normalizes: a raw name that
// begins with the server's own prefix ("a:ns:erase" on "a") would otherwise
// be approval- and config-gated as the suffix tool "ns:erase" while dispatch
// targets "a:ns:erase" (Spec 105 FR-009).
func (p *MCPProxyServer) evaluateExactToolGate(serverName, toolName string) toolGate {
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

	cfg := p.currentConfig()
	quarantineEnabled := cfg == nil || cfg.IsQuarantineEnabled()
	quarantineGate := quarantineEnabled && !serverConfig.IsQuarantineSkipped()

	// ONE snapshot read serves both the no-record rule below and the
	// classifier's Discovered input, so the two cannot disagree about whether
	// the tool is in the snapshot.
	identity := p.resolveExactToolIdentity(serverName, toolName)
	// The no-record rule holds only for a tool the LIVE snapshot lists. A
	// name on a server whose snapshot is empty because of its own state
	// (disconnected, connecting, quarantined, disabled) keeps the server-level
	// verdict that always owned it (research D4; codex r3 D1): an empty
	// snapshot cannot show the name absent, and synthesizing a pending record
	// there would answer "is in the server's tool list" for a list that is
	// empty — and pre-empt the not-connected verdict for a record-less name
	// while the recorded sibling on the same dropped server keeps it. No
	// consumer of this gate can carry such a name to an upstream: every
	// dispatch path refuses a not-connected client before any upstream call
	// (handleCallToolVariant / handleCallTool's IsConnected pre-check; the
	// sandbox bridge dispatches through managed.Client.CallTool, which refuses
	// when not connected and never reconnects), and once the server reconnects
	// the fresh discovery stamp makes a never-listed name Unresolved (identity
	// gate / liveIdentityRefusal / lookupToolPermission). reconnect_on_use
	// lives only in upstream.Manager.CallTool, reached by direct mode, whose
	// catalog is a live tools/list and whose evaluator hard-codes
	// discovered = true. A server the StateView does not hold at all keeps the
	// implicit default (unit fixtures without a runtime; server-existence
	// handling owns the rest).
	recordRequired := identity.Found

	approval, approvalErr := p.lookupToolApproval(serverName, toolName)
	switch {
	case approvalErr == nil:
		gate.approval = approval
	case errors.Is(approvalErr, storage.ErrToolApprovalNotFound):
		// No record. Spec 105 FR-009 (research D4): while the tool-level
		// quarantine gate is active for the server, a tool the discovery
		// snapshot contains (recordRequired) is PENDING under its own name,
		// never ready; the implicit-approved default survives only for a
		// name the snapshot does not list (identity resolution's concern for
		// a hydrated snapshot, the server-level verdicts' for an empty one)
		// or while the gate is off for the server.
		gate.approval = implicitPendingApproval(serverName, toolName, recordRequired, identity.Description, quarantineGate)
		if gate.approval != nil {
			p.logImplicitPending("tool_gate", serverName, toolName)
		}
	default:
		// A real BBolt failure must not silently re-enable a tool the user
		// disabled (isToolCallable's long-standing fail-closed rule).
		gate.storageErr = approvalErr
	}

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
		// Belt and braces with implicitPendingApproval above: the classifier
		// applies the same no-record rule itself, so the two cannot drift.
		Discovered: recordRequired,
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

// lookupToolApproval reads the Spec-032 approval record for one (server, RAW
// tool) pair, already split by normalizeServerTool or held split by the
// caller. It is the storage half of the Spec 105 FR-009 reader; the snapshot
// half — "no record for a tool the discovery snapshot contains is pending
// while the quarantine gate is active" — needs the live StateView and the
// gate flag, so it lives with the callers that hold both
// (evaluateExactToolGate, directCallabilityEvaluator.evaluate) and, for the
// preflight evaluator, in preflight.ClassifyTool's Discovered input.
//
// The absence of a usable record is reported as
// storage.ErrToolApprovalNotFound, the same contract GetToolApproval keeps.
func (p *MCPProxyServer) lookupToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	return readToolApprovalRecord(p.storage, serverName, toolName)
}

// readToolApprovalRecord resolves the approval record for one (server, RAW
// tool) pair under the Spec 105 FR-009 reader rules. It is a free function
// over the storage manager so the preflight glue's ApprovalReader — which is
// constructed with storage alone — resolves records by exactly the same rule
// as dispatch.
//
// Every producer now keys records by the raw upstream name (runtime's
// discovery producer checkToolApprovals, the ApproveTools review surface and
// the user toggle setToolEnabledNoEmit all read and write (server, "ns:erase")
// for a raw "ns:erase"), so the EXACT record is the tool's record and wins
// outright whenever it exists — with the single never-baselined exception
// described below.
//
// What remains to be reconciled is the store a pre-105 binary left behind:
// discovery used to file a raw name that carries a ":" segment under the
// COLLAPSED key — everything after the first colon, so "ns:erase" landed
// under "erase". Such a record is ambiguous on its face: it may describe the
// raw "ns:erase" that produced it, or a genuine sibling tool "erase". Nothing
// rewrites it (research D4, tool_quarantine.go: guessing would be a silent
// approval); instead a legacy collapsed record APPROVES only the raw name it
// stores. Read for a namespaced name it can therefore only RESTRICT: its
// quarantine lock (pending / changed) and its user Disabled flag still bind
// the namespaced tool — a tool that was locked or disabled at every instant
// must not become callable because its record sits under the old key — while
// an approved, enabled legacy record is reported as "no record", so the
// namespaced tool stays pending under an active gate until it is approved by
// its own name (the first discovery after upgrade files that exact record).
//
// One exact-record shape does NOT win outright: an APPROVED exact record whose
// ApprovedHash is EMPTY. The pre-105 user toggle (runtime setToolEnabledNoEmit)
// synthesized exactly that record under the exact raw name whenever the
// operator toggled a namespaced tool, while discovery kept the tool's real
// quarantine lock under the collapsed key — so until the first discovery
// after upgrade re-files it (runtime adoptLegacyLockOrBaseline), the exact
// record carries the user's visibility intent and no approval decision at
// all. For that shape, and only that shape, a restricting collapsed sibling
// still binds: the merge is re-admitted narrowly, with the sibling's lock and
// the union of both Disabled flags on the exact identity.
//
// Only an UNSTAMPED collapsed record is a legacy one
// (storage.ToolApprovalRecord.IdentityKeyed): a record a post-105 binary wrote
// under "erase" is the genuine "erase" tool's own record and lends nothing to
// "ns:erase" on either branch.
//
// Both keys are read in ONE storage snapshot (Manager.GetToolApprovals: a
// single read lock and a single read transaction) so a pair of operator writes
// cannot land between the two reads and be observed as a state that never
// existed.
func readToolApprovalRecord(st *storage.Manager, serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	keys := []string{toolName}
	// The collapsed key may be EMPTY: the pre-105 producer filed a raw name
	// that ends in a colon ("ns:") under (server, "") and storage accepts that
	// key, so it is read like any other collapsed record rather than skipped.
	collapsed, hasCollapsed := "", false
	if _, rest, ok := strings.Cut(toolName, ":"); ok {
		collapsed, hasCollapsed = rest, true
		keys = append(keys, collapsed)
	}
	records, err := st.GetToolApprovals(serverName, keys...)
	if err != nil {
		return nil, err
	}
	var legacy *storage.ToolApprovalRecord
	if hasCollapsed {
		if candidate := records[collapsed]; candidate != nil && !candidate.IdentityKeyed && legacyApprovalRestricts(candidate) {
			legacy = candidate
		}
	}
	if exact := records[toolName]; exact != nil {
		if legacy != nil && neverBaselined(exact) {
			merged := *legacy
			merged.ServerName, merged.ToolName = exact.ServerName, exact.ToolName
			merged.Disabled = exact.Disabled || legacy.Disabled
			merged.HeldSignals = append([]string(nil), legacy.HeldSignals...)
			return &merged, nil
		}
		return exact, nil
	}
	if legacy != nil {
		return legacy, nil
	}
	return nil, fmt.Errorf("%w: %s", storage.ErrToolApprovalNotFound, storage.ToolApprovalKey(serverName, toolName))
}

// neverBaselined reports the exact-record shape the pre-105 user toggle
// synthesized: approved, but with no approved contract hash — discovery has
// not baselined it, so it carries no approval decision that could outrank a
// legacy lock (readToolApprovalRecord).
func neverBaselined(record *storage.ToolApprovalRecord) bool {
	return record.Status == storage.ToolApprovalStatusApproved && record.ApprovedHash == ""
}

// legacyApprovalRestricts reports whether a pre-105 collapsed record carries a
// fact that must keep binding the namespaced raw name it may have been filed
// for: a quarantine lock or a user block. An approved, enabled record carries
// only an approval, and an approval belongs to the exact raw name alone. The
// rule itself lives on the record (storage.ToolApprovalRecord.Restricts).
func legacyApprovalRestricts(record *storage.ToolApprovalRecord) bool {
	return record.Restricts()
}

// implicitPendingHeldReason marks the in-memory record implicitPendingApproval
// synthesizes. It is never persisted — the discovery producer stamps only the
// storage.ToolHeldReason* scan reasons — so it cannot collide with a stored
// record, and it lets the response builders tell "no record yet" apart from a
// pending record an operator can actually find and approve in the review UI.
const implicitPendingHeldReason = "no_approval_record"

// isImplicitPendingApproval reports whether a record is the synthesized
// no-record placeholder rather than a stored pending record.
func isImplicitPendingApproval(record *storage.ToolApprovalRecord) bool {
	return record != nil && record.HeldReason == implicitPendingHeldReason
}

// implicitPendingApproval is the snapshot half of the FR-009 reader: while the
// tool-level quarantine gate is active for a server, a tool its discovery
// snapshot contains that has NO approval record is pending, never ready
// (research D4). It returns an in-memory pending record for the pair — never
// persisted, shaped like the one the discovery producer would have filed so
// the gate, the activity reason and the describe_tool gate answer exactly as
// they do for a stored pending record — or nil when the rule does not apply:
// the gate is off for the server, or the snapshot does not list the raw name
// (an undiscovered name is identity resolution's concern, not approval's).
//
// The record is tagged implicitPendingHeldReason so the refusal BODY differs
// from a stored pending record's: nothing is listed in the review UI for it
// yet, so the standard "ask the user to approve" remediation would be a dead
// end. Instead it says the server's tools are re-evaluated on its next
// discovery pass, which is what files the real record.
//
// In production the record is genuinely absent only through the discovery
// fail-open paths (checkToolApprovals / applyDifferentialToolUpdate skipping
// record creation) and, until the first discovery after upgrade, for a
// namespaced tool whose pre-105 record was collapsed onto a sibling's key;
// both are exactly the windows in which "no record" used to read as ready.
// Callers log the synthesis at Warn (logImplicitPending) so the fail-open
// discovery path that produced it is diagnosable.
func implicitPendingApproval(serverName, toolName string, discovered bool, description string, quarantineGate bool) *storage.ToolApprovalRecord {
	if !quarantineGate || !discovered {
		return nil
	}
	return &storage.ToolApprovalRecord{
		ServerName:         serverName,
		ToolName:           toolName,
		Status:             storage.ToolApprovalStatusPending,
		CurrentDescription: description,
		HeldReason:         implicitPendingHeldReason,
	}
}

// logImplicitPending records, at Warn, that a dispatch gate refused a
// snapshot tool because no approval record exists for it — the discovery
// pass that should have filed one either has not run yet or failed open.
func (p *MCPProxyServer) logImplicitPending(site, serverName, toolName string) {
	if p.logger == nil {
		return
	}
	p.logger.Warn("Snapshot tool has no approval record; treating as pending under the active quarantine gate",
		zap.String("site", site),
		zap.String("server_name", serverName),
		zap.String("tool_name", toolName))
}
