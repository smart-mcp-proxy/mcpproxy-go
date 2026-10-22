package runtime

import (
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 119: under trust_mode scan a CHANGED contract that scans clean is
// approved in the same discovery pass. This file holds the two pieces the
// change gate (checkToolApprovals) calls for that: the safer-direction hint
// flip rule, which the offline scanner does not cover, and the approval write.

// SignalHintSaferFlip is the deterministic gate signal recorded when a changed
// contract re-labels itself as SAFER than the approved one (readOnlyHint
// false→true or destructiveHint true→false). Such a flip is held for review
// whatever the scan verdict (spec 119 FR-007): a tool that calls itself
// read-only is exactly what a profile tier cap and call_tool_read trust, and a
// flip with no description change is invisible to the capability-mismatch
// check. Not a TPA signature id on purpose: the Web UI lists it with the
// heuristic check ids.
const SignalHintSaferFlip = "GATE-HINT-SAFER-FLIP"

// saferHintFlip reports whether live re-labels the tool as safer than approved.
// Absent hints take the MCP defaults (readOnlyHint false, destructiveHint
// true), so a tool that GAINS readOnlyHint:true, or gains an explicit
// destructiveHint:false, counts as a flip; losing a hint toward the default is
// not safer and does not. An approved snapshot that is nil as a whole is read
// as "no hints" too: the upgrade wildcard for pre-annotation records is applied
// before the change gate runs, so by the time a hash mismatch reaches it the
// hints are part of the approved contract.
func saferHintFlip(approved, live *config.ToolAnnotations) bool {
	readOnly := func(a *config.ToolAnnotations) bool {
		return a != nil && a.ReadOnlyHint != nil && *a.ReadOnlyHint
	}
	destructive := func(a *config.ToolAnnotations) bool {
		return a == nil || a.DestructiveHint == nil || *a.DestructiveHint
	}
	if !readOnly(approved) && readOnly(live) {
		return true
	}
	if destructive(approved) && !destructive(live) {
		return true
	}
	return false
}

// withHintFlip adds the gate's safer-flip signal to the scan's evidence (FR-007:
// the signal is recorded whatever the verdict). A clean verdict becomes a
// finding with that one signal; a non-clean one keeps its verdict and
// signals, the flip listed after them. The result is never nil.
func withHintFlip(evidence *scanHoldEvidence) *scanHoldEvidence {
	if evidence == nil {
		return &scanHoldEvidence{Reason: storage.ToolHeldReasonScanFindings, Verdict: "clean", Signals: []string{SignalHintSaferFlip}}
	}
	out := *evidence
	out.Reason = storage.ToolHeldReasonScanFindings
	out.Signals = append(append([]string(nil), evidence.Signals...), SignalHintSaferFlip)
	return &out
}

// scanApproveChangedContract approves a changed contract that scanned clean
// under trust_mode scan (spec 119 FR-004/FR-006): the record becomes approved
// on its CURRENT definition with reason scan_approved, the hold evidence is
// cleared and a tool_auto_approved activity event is emitted, exactly as
// auto_approve_tool_changes does. It returns true when the record was written
// (the caller moves on to the next tool). On an invariant refusal or a failed
// write it returns false with the record restored field for field to what it
// was, and the caller holds the change as before: the hold write that follows
// must not carry the approved hash or the cleared Previous* evidence of an
// approval that never landed, or the next pass's hash-match shortcut would
// approve the contract with no scan and no operator (review round 1, P2).
func (r *Runtime) scanApproveChangedContract(serverName, toolName string, existing *storage.ToolApprovalRecord, wasUnstamped bool, tool *config.ToolMetadata, schemaJSON, outputSchemaJSON, currentHash string) bool {
	if invErr := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonScanApproved); invErr != nil {
		return false
	}
	before := *existing
	existing.Status = storage.ToolApprovalStatusApproved
	existing.ApprovedHash = currentHash
	existing.CurrentHash = currentHash
	existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
	existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
	existing.ApprovedAt = time.Now().UTC()
	existing.ApprovedBy = "scan-approved"
	existing.CurrentDescription = tool.Description
	existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
	existing.CurrentSchema = schemaJSON
	existing.CurrentOutputSchema = outputSchemaJSON
	existing.PreviousDescription = ""
	existing.PreviousAnnotations = nil
	existing.PreviousSchema = ""
	existing.PreviousOutputSchema = ""
	existing.ClearScanHold()
	if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
		*existing = before
		r.logger.Error("Failed to scan-approve changed tool; holding it for review instead",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.Error(saveErr))
		return false
	}
	r.logger.Info("Tool change scan-approved (trust_mode: scan, clean verdict)",
		zap.String("server", serverName),
		zap.String("tool", toolName),
		zap.String("current_hash", currentHash))
	r.emitToolQuarantineEvent(serverName, toolName, "tool_auto_approved", "", currentHash,
		"", tool.Description, "", schemaJSON)
	return true
}
