package runtime

import (
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 119: under trust_mode scan a CHANGED contract that scans clean is
// approved in the same discovery pass. This file holds the approval write the
// change gate (checkToolApprovals) calls for that; the structural rules that
// run in front of it (spec 120, formerly the safer-direction hint flip) live in
// tool_contract_delta.go.

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
//
// evidence is the scan's verdict and signals are the structural-gate signals
// evaluated (empty on an approval); both go into the tool_auto_approved audit
// event together with the approved contract being replaced (spec 120 FR-010).
func (r *Runtime) scanApproveChangedContract(serverName, toolName string, existing *storage.ToolApprovalRecord, wasUnstamped bool, tool *config.ToolMetadata, schemaJSON, outputSchemaJSON, currentHash string, evidence *scanHoldEvidence, signals []Signal) bool {
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
	verdict := ""
	if evidence != nil {
		verdict = evidence.Verdict
	}
	r.emitToolAutoApprovedChange(serverName, toolName, &before, tool, schemaJSON, outputSchemaJSON, currentHash, "scan-approved", signals, verdict)
	return true
}

// emitToolAutoApprovedChange emits tool_auto_approved for a CHANGED contract
// with the approved contract it replaces (spec 120 FR-010): old/new hash,
// description, input and output schema and annotations, who approved, the
// structural-gate signals evaluated, and the scan verdict and bundle when a
// scan ran (approvedBy scan-approved). before is the record as it was before
// the approval write: Current* is the approved contract while the record was
// approved, Previous* once it had been marked changed.
func (r *Runtime) emitToolAutoApprovedChange(serverName, toolName string, before *storage.ToolApprovalRecord, tool *config.ToolMetadata, schemaJSON, outputSchemaJSON, currentHash, approvedBy string, signals []Signal, verdict string) {
	old := approvedSnapshotOf(before)
	oldDesc := before.CurrentDescription
	if before.Status == storage.ToolApprovalStatusChanged {
		oldDesc = before.PreviousDescription
	}
	extra := map[string]any{
		"approved_by":  approvedBy,
		"gate_signals": append([]string{}, signalStrings(signals)...),
	}
	if old.OutputSchema != "" {
		extra["old_output_schema"] = truncateEventText(old.OutputSchema)
	}
	if outputSchemaJSON != "" {
		extra["new_output_schema"] = truncateEventText(outputSchemaJSON)
	}
	if old.Annotations != nil {
		if raw, err := json.Marshal(old.Annotations); err == nil {
			extra["old_annotations"] = string(raw)
		}
	}
	if tool != nil && tool.Annotations != nil {
		if raw, err := json.Marshal(tool.Annotations); err == nil {
			extra["new_annotations"] = string(raw)
		}
	}
	if verdict != "" {
		extra["scan_verdict"] = verdict
		info := scanner.BundleStatus()
		bundle := info.Fingerprint
		if bundle == "" {
			bundle = info.BundleVersion
		}
		if bundle != "" {
			extra["scan_bundle"] = bundle
		}
	}
	r.emitToolQuarantineEventWith(serverName, toolName, "tool_auto_approved", before.ApprovedHash, currentHash,
		oldDesc, tool.Description, old.InputSchema, schemaJSON, extra)
}
