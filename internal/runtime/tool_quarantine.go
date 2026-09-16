package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/hash"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// calculateToolApprovalHash computes a stable SHA-256 hash for tool-level quarantine.
// Uses toolName + description + input schema JSON + output schema JSON.
// Annotations are intentionally EXCLUDED because:
// 1. They are metadata hints, not functional changes to the tool
// 2. They may not be stable across reconnections (some servers omit them)
// 3. Including them caused false "tool_description_changed" spam on every reconnect
// 4. This matches the upstream client's hash.ComputeToolHashWithOutputSchema approach
// The annotations parameter is kept for API compatibility but ignored.
func calculateToolApprovalHash(toolName, description, schemaJSON string, annotations *config.ToolAnnotations) string {
	return calculateToolApprovalHashWithOutputSchema(toolName, description, schemaJSON, "", annotations)
}

func calculateToolApprovalHashWithOutputSchema(toolName, description, schemaJSON, outputSchemaJSON string, annotations *config.ToolAnnotations) string {
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte("|"))
	h.Write([]byte(description))
	h.Write([]byte("|"))
	// Normalize JSON schema to prevent key-order differences from causing
	// false "tool_description_changed" events. Parse → sort keys → serialize.
	h.Write([]byte(normalizeJSON(schemaJSON)))
	// Only fold the output schema into the hash when the tool actually exposes
	// one. This keeps the hash byte-identical to the pre-output-schema formula
	// for tools without an outputSchema, so they are NOT re-baselined or
	// re-quarantined on upgrade. Tools that do expose an outputSchema get a new
	// hash, which the version-gated migration in checkToolApprovals handles.
	if normalized := normalizeJSON(outputSchemaJSON); normalized != "" {
		h.Write([]byte("|"))
		h.Write([]byte(normalized))
	}
	// Annotations excluded from hash — see comment above
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeJSON parses a JSON string and re-serializes with sorted keys.
// Returns the original string if parsing fails (non-JSON content). Delegates to
// hash.NormalizeJSON so the approval hash and the upstream tool capture share a
// single canonical normalizer.
func normalizeJSON(s string) string {
	return hash.NormalizeJSON(s)
}

// calculateLegacyToolApprovalHash computes the old hash format (without annotations).
// Used for backward compatibility: tools approved before annotation tracking can be
// silently re-approved if only the hash formula changed (not the actual content).
func calculateLegacyToolApprovalHash(toolName, description, schemaJSON string) string {
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte("|"))
	h.Write([]byte(description))
	h.Write([]byte("|"))
	h.Write([]byte(normalizeJSON(schemaJSON)))
	return hex.EncodeToString(h.Sum(nil))
}

// calculateHashWithAnnotations computes the OLD hash formula that included annotations.
// Used for migration: tools approved with the old formula need to be silently re-approved
// with the new formula (which excludes annotations to prevent false change detection).
func calculateHashWithAnnotations(toolName, description, schemaJSON string, annotations *config.ToolAnnotations) string {
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte("|"))
	h.Write([]byte(description))
	h.Write([]byte("|"))
	h.Write([]byte(schemaJSON))
	if annotations != nil {
		annotationsJSON, err := json.Marshal(annotations)
		if err == nil {
			h.Write([]byte("|"))
			h.Write(annotationsJSON)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TransitionReason explains why a tool approval state transition is occurring.
type TransitionReason string

const (
	ReasonHashMatch         TransitionReason = "hash_match"
	ReasonDescriptionRevert TransitionReason = "description_revert"
	ReasonFormulaMigration  TransitionReason = "formula_migration"
	ReasonContentMatch      TransitionReason = "content_match"
	ReasonDescriptionMatch  TransitionReason = "description_match"
	ReasonUserApprove       TransitionReason = "user_approve"
	ReasonAutoApprove       TransitionReason = "auto_approve"
	// ReasonBaselineTrust marks promotion of a never-reviewed (pending) tool to
	// approved because the server is trusted (non-quarantined) and has no prior
	// approved baseline yet — the current toolset IS the baseline (MCP-2931).
	ReasonBaselineTrust TransitionReason = "baseline_trust"
	// ReasonAutoApproveChanges marks promotion driven by the explicit per-server
	// auto_approve_tool_changes flag (MCP-2931/MCP-2930). It is the ONLY reason,
	// besides explicit user action, that may clear a changed (rug-pull) record —
	// because the operator opted into auto-approving changes for that server.
	ReasonAutoApproveChanges TransitionReason = "auto_approve_changes"
	// ReasonScanApproved marks a change/addition auto-approved because the server
	// runs in trust_mode: scan AND a synchronous, offline TPA scan of the changed
	// tool returned a green (clean) verdict (spec 086 stage 2). Like
	// ReasonAutoApproveChanges it may clear a changed (rug-pull) record, but only
	// with the scanner's positive attestation — a non-green or degraded/absent
	// verdict never reaches this reason (the gate fails closed before it).
	ReasonScanApproved TransitionReason = "scan_approved"
)

// assertToolApprovalInvariant checks that a state transition is valid according
// to quarantine safety rules. Returns nil for valid transitions.
//
// Invariants:
//   - changed→approved: requires user action, description revert, or proof that
//     the tool content hasn't actually changed (hash match, formula migration,
//     content match).
//   - pending→approved: requires explicit user action or auto-approve (when
//     quarantine is disabled for the server).
func assertToolApprovalInvariant(oldStatus, newStatus string, reason TransitionReason) error {
	if newStatus != storage.ToolApprovalStatusApproved {
		return nil
	}

	switch oldStatus {
	case storage.ToolApprovalStatusChanged:
		switch reason {
		case ReasonHashMatch, ReasonDescriptionRevert, ReasonFormulaMigration,
			ReasonContentMatch, ReasonDescriptionMatch, ReasonUserApprove,
			ReasonAutoApproveChanges, ReasonScanApproved:
			return nil
		default:
			return fmt.Errorf("invariant violation: changed→approved with reason %q "+
				"(requires user action or description revert)", reason)
		}
	case storage.ToolApprovalStatusPending:
		switch reason {
		case ReasonUserApprove, ReasonAutoApprove, ReasonBaselineTrust, ReasonAutoApproveChanges, ReasonScanApproved:
			return nil
		default:
			return fmt.Errorf("invariant violation: pending→approved with reason %q "+
				"(requires user action)", reason)
		}
	}
	return nil
}

// enforceInvariant logs and returns the invariant error, or panics in test mode.
func (r *Runtime) enforceInvariant(serverName, toolName, oldStatus, newStatus string, reason TransitionReason) error {
	err := assertToolApprovalInvariant(oldStatus, newStatus, reason)
	if err == nil {
		return nil
	}
	r.logger.Error("Tool approval invariant violation",
		zap.String("server", serverName),
		zap.String("tool", toolName),
		zap.String("old_status", oldStatus),
		zap.String("new_status", newStatus),
		zap.String("reason", string(reason)),
		zap.Error(err))
	return err
}

// scanChangeIsClean runs a synchronous, offline (no Docker/network/filesystem)
// TPA scan of a single changed tool and reports whether trust_mode: scan may
// auto-approve it (spec 086 stage 2, FR-012/FR-013/FR-014). Green — the ONLY
// auto-approvable state — is a "clean" verdict with full scanner coverage. A
// non-clean verdict ("warnings"/"dangerous"), degraded/absent coverage, or a
// missing bundle all fail closed (return false) so the record stays held for
// human review. On a hold it also returns the scan evidence (verdict + matched
// check ids) so the caller can persist it onto the approval record and the
// tool-approval surfaces can tell the operator WHY the tool is held (FR-018).
// The evidence is nil when the tool is clean.
func (r *Runtime) scanChangeIsClean(serverName string, tool *config.ToolMetadata) (bool, *scanHoldEvidence) {
	// Feed the changed tool the SAME cross-server context the async full-server
	// scan gets: every OTHER connected server's current tools. Without this the
	// hard-tier shadowing/impersonation checks are inert (they only see the single
	// changed tool) and a cross-server rug-pull would resolve to a green,
	// full-coverage verdict — a fail-open. See ScanToolMetadataVerdict's peerTools
	// contract.
	peers := r.collectPeerToolMetadata(serverName)
	// Schema v9: count the gate INVOCATION (not the outcome) before the
	// verdict branches below — this synchronous path is the TPA detection most
	// installs actually exercise, and it emitted nothing until now. Nil-safe.
	telemetry.RecordTPAToolChangeGateScanOn(r.TelemetryRegistry())
	verdict, findings, coverageOK := scanner.ScanToolMetadataVerdict(serverName, []*config.ToolMetadata{tool}, peers)
	if coverageOK && verdict == "clean" {
		return true, nil
	}
	var signals []string
	for _, f := range findings {
		signals = append(signals, f.Signals...)
	}
	// Distinguish "the scan found something" from "the scan could not be
	// trusted": the latter has no findings to show, so the operator needs the
	// reason itself to make sense of an evidence-free hold.
	reason := storage.ToolHeldReasonScanFindings
	if !coverageOK && len(signals) == 0 {
		reason = storage.ToolHeldReasonScanCoverage
	}
	r.logger.Info("trust_mode scan held tool change for review (non-green verdict)",
		zap.String("server", serverName),
		zap.String("tool", config.RawToolName(tool)),
		zap.String("verdict", verdict),
		zap.Bool("coverage_ok", coverageOK),
		zap.Strings("tpa_signals", signals))
	return false, &scanHoldEvidence{Reason: reason, Verdict: verdict, Signals: signals}
}

// scanHoldEvidence is the compact, serializable explanation of a trust_mode:
// scan hold, carried from the gate onto the tool-approval record (FR-018).
type scanHoldEvidence struct {
	Reason  string   // one of storage.ToolHeldReason*
	Verdict string   // "clean" | "warnings" | "dangerous"
	Signals []string // matched deterministic check ids (incl. TPA signature ids)
}

// applyTo writes the evidence onto an approval record; a nil evidence clears any
// previously recorded hold so the record always describes its CURRENT state.
func (e *scanHoldEvidence) applyTo(record *storage.ToolApprovalRecord) {
	if record == nil {
		return
	}
	if e == nil {
		record.ClearScanHold()
		return
	}
	record.SetScanHold(e.Reason, e.Verdict, e.Signals)
}

// recordScanHold updates an ALREADY-PERSISTED approval record with the current
// scan-hold evidence, persisting only when it actually changed. Discovery runs
// this gate on every reconnect, so the no-op guard keeps a steady-state held
// tool from generating a BBolt write per pass. The record was READ by the
// caller's pass and still restricts (it is held), so the write goes through
// saveReadToolApproval with the caller's wasUnstamped (Spec 105 FR-009).
func (r *Runtime) recordScanHold(serverName, toolName string, existing *storage.ToolApprovalRecord, wasUnstamped bool, evidence *scanHoldEvidence) {
	if existing == nil || r.storageManager == nil {
		return
	}
	prevReason, prevVerdict, prevSignals := existing.HeldReason, existing.HeldVerdict, existing.HeldSignals
	evidence.applyTo(existing)
	if existing.HeldReason == prevReason && existing.HeldVerdict == prevVerdict &&
		slices.Equal(existing.HeldSignals, prevSignals) {
		return
	}
	if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
		r.logger.Debug("Failed to persist scan-hold evidence on tool approval",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.Error(saveErr))
	}
}

// collectPeerToolMetadata returns every OTHER connected server's current tools,
// keyed by server name, projected onto config.ToolMetadata for the synchronous
// scan gate. It is the cross-server context that lets the peer-dependent
// shadowing/impersonation checks fire inline (parity with the async full-server
// scan). Sourced from the lock-free StateView snapshot; returns nil when no
// supervisor/snapshot is available (best effort — a genuinely single-server
// deployment has no peers to shadow anyway).
func (r *Runtime) collectPeerToolMetadata(serverName string) map[string][]*config.ToolMetadata {
	if r.supervisor == nil {
		return nil
	}
	snapshot := r.supervisor.StateView().Snapshot()
	if snapshot == nil {
		return nil
	}
	peers := make(map[string][]*config.ToolMetadata)
	for name, status := range snapshot.Servers {
		if name == serverName || status == nil {
			continue
		}
		metas := make([]*config.ToolMetadata, 0, len(status.Tools))
		for i := range status.Tools {
			t := status.Tools[i]
			paramsJSON := ""
			if t.InputSchema != nil {
				if raw, err := json.Marshal(t.InputSchema); err == nil {
					paramsJSON = string(raw)
				}
			}
			metas = append(metas, &config.ToolMetadata{
				ServerName:       name,
				Name:             t.Name,
				RawName:          t.Name, // StateView holds raw names (Spec 105 FR-009)
				Description:      t.Description,
				ParamsJSON:       paramsJSON,
				OutputSchemaJSON: t.OutputSchemaJSON,
			})
		}
		if len(metas) > 0 {
			peers[name] = metas
		}
	}
	if len(peers) == 0 {
		return nil
	}
	return peers
}

// scanApproveChange re-baselines a changed (or approved-then-changed) tool record
// to approved under trust_mode: scan after scanChangeIsClean returned a green
// verdict (spec 086 stage 2). It routes through enforceInvariant with
// ReasonScanApproved; returns true when the record was saved approved, false when
// the invariant refused or the save failed — in which case the caller MUST fall
// through to the fail-closed (held) path. The record was READ by the caller's
// pass, so the write goes through saveReadToolApproval with the caller's
// wasUnstamped (Spec 105 FR-009): a user-disabled pre-105 record scan-approved
// here still restricts and keeps waiting unstamped.
func (r *Runtime) scanApproveChange(serverName, toolName string, existing *storage.ToolApprovalRecord, wasUnstamped bool, tool *config.ToolMetadata, schemaJSON, outputSchemaJSON, currentHash string) bool {
	if invErr := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonScanApproved); invErr != nil {
		return false
	}
	// Snapshot the pre-mutation record so a save failure leaves `existing`
	// byte-identical to what the caller passed in. Otherwise the caller's
	// fail-closed mark-changed path would read the already-overwritten fields
	// (new description/hash) as the "old" values and persist a corrupted rug-pull
	// record.
	snapshot := *existing
	existing.Status = storage.ToolApprovalStatusApproved
	existing.ApprovedHash = currentHash
	existing.CurrentHash = currentHash
	existing.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
	existing.ApprovedAt = time.Now().UTC()
	existing.ApprovedBy = "scan-approved"
	existing.CurrentDescription = tool.Description
	existing.CurrentSchema = schemaJSON
	existing.CurrentOutputSchema = outputSchemaJSON
	existing.PreviousDescription = ""
	existing.PreviousSchema = ""
	existing.PreviousOutputSchema = ""
	existing.ClearScanHold() // the record is no longer held — drop stale evidence
	if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
		*existing = snapshot // restore so the caller's mark-changed path sees true old values
		r.logger.Error("Failed to scan-approve tool change",
			zap.String("server", serverName), zap.String("tool", toolName), zap.Error(saveErr))
		return false
	}
	r.logger.Info("Tool change scan-approved (trust_mode: scan, clean verdict)",
		zap.String("server", serverName), zap.String("tool", toolName))
	r.emitToolQuarantineEvent(serverName, toolName, "tool_auto_approved", "", currentHash,
		"", tool.Description, "", schemaJSON)
	return true
}

// ToolApprovalResult contains the result of checking tool approvals for a server.
type ToolApprovalResult struct {
	// BlockedTools is the set of tool names that should not be indexed (pending, changed, or disabled).
	BlockedTools map[string]bool
	// PendingCount is the number of newly discovered tools awaiting approval.
	PendingCount int
	// ChangedCount is the number of tools whose description/schema changed since approval.
	ChangedCount int
}

// toolQuarantineGate is the per-server resolution of the tool-level
// quarantine gate — the single place the global flag, the server's
// skip/quarantined state and its trust mode (spec 086) are folded into the
// enforcement levels checkToolApprovals and the user toggle
// (setToolEnabledNoEmit) key on.
type toolQuarantineGate struct {
	globalEnabled     bool
	serverSkipped     bool
	serverQuarantined bool
	// autoApproveChanges: trust_mode auto (today's behavior — every change
	// and addition auto-approves).
	autoApproveChanges bool
	// scanMode: trust_mode scan — a change auto-approves ONLY on a green
	// in-process TPA verdict, else it is held (fail closed).
	scanMode bool
	// enforceNewTools: block NEW tools for review (unless quarantine is
	// disabled globally or the server is skipped). Even trusted
	// (non-quarantined) servers have new tools reviewed when quarantine is
	// globally enabled — this prevents injection via new tool additions on
	// compromised servers; only skip_quarantine=true explicitly opts out.
	enforceNewTools bool
	// enforceQuarantine: full quarantine mode for servers explicitly
	// quarantined.
	enforceQuarantine bool
}

// active reports whether a new, unreviewed tool on this server is held for
// review under its own name (the mirror image of neverBaselinedGate.lifted):
// quarantine is enforced for the server and the operator has not opted into
// auto-approving changes. trust_mode: scan counts as active — a scan can
// approve a change discovery observes, never a record the toggle mints.
func (g toolQuarantineGate) active() bool {
	return g.enforceNewTools && !g.autoApproveChanges
}

// resolveToolQuarantineGate resolves the gate for a server from the current
// config (trust mode: manual → every change/addition held; auto → all
// auto-approved; scan → green-scan-gated).
func (r *Runtime) resolveToolQuarantineGate(serverName string) toolQuarantineGate {
	cfg := r.Config()
	gate := toolQuarantineGate{globalEnabled: cfg.IsQuarantineEnabled()}
	for _, sc := range cfg.Servers {
		if sc.Name == serverName {
			mode := sc.EffectiveTrustMode()
			gate.serverSkipped = sc.IsQuarantineSkipped()
			gate.serverQuarantined = sc.Quarantined
			gate.autoApproveChanges = mode == config.TrustModeAuto
			gate.scanMode = mode == config.TrustModeScan
			break
		}
	}
	gate.enforceNewTools = gate.globalEnabled && !gate.serverSkipped
	gate.enforceQuarantine = gate.enforceNewTools && gate.serverQuarantined
	return gate
}

// checkToolApprovals checks and updates tool approval records for discovered tools.
// It returns the set of tool names that should be blocked (not indexed).
// If quarantine is disabled (globally or per-server), new tools are auto-approved
// and no tools are blocked. Changed tools from previously-approved servers are still
// blocked for security (rug pull detection).
func (r *Runtime) checkToolApprovals(serverName string, tools []*config.ToolMetadata) (*ToolApprovalResult, error) {
	if r.storageManager == nil {
		return &ToolApprovalResult{BlockedTools: make(map[string]bool)}, nil
	}

	// Determine if quarantine is enforced for this server
	gate := r.resolveToolQuarantineGate(serverName)
	globalEnabled := gate.globalEnabled
	serverQuarantined := gate.serverQuarantined
	autoApproveChanges := gate.autoApproveChanges
	scanMode := gate.scanMode
	enforceNewTools := gate.enforceNewTools
	enforceQuarantine := gate.enforceQuarantine

	// Trust-baseline model (MCP-2931): a trusted (non-quarantined) server whose
	// tools have NEVER been approved before treats its CURRENT toolset as the
	// baseline — every current tool is auto-approved instead of stranded as
	// pending. A record in approved OR changed state proves a prior baseline
	// exists (changed implies the tool was approved once, then mutated), so its
	// presence disqualifies the baseline pass and post-baseline review resumes.
	// Detection is snapshotted BEFORE the loop so promoting tools mid-pass does
	// not flip the decision.
	//
	// The same pre-pass listing is the ONLY store the Spec 105 legacy
	// consults below read (legacyCollapsedSibling): a collapsed pre-105
	// record is looked up as it stood BEFORE this pass, so the order in
	// which "erase" and "ns:erase" are processed cannot change the outcome —
	// processing "erase" first re-saves (and stamps) its record, which would
	// otherwise hide it from "ns:erase" on the very pass that migrates it.
	serverHasBaseline := false
	priorByName := make(map[string]*storage.ToolApprovalRecord)
	if priorRecords, listErr := r.storageManager.ListToolApprovals(serverName); listErr == nil {
		for _, rec := range priorRecords {
			priorByName[rec.ToolName] = rec
			if rec.Status == storage.ToolApprovalStatusApproved || rec.Status == storage.ToolApprovalStatusChanged {
				serverHasBaseline = true
			}
		}
	}
	isBaselinePass := enforceNewTools && !serverQuarantined && !serverHasBaseline

	result := &ToolApprovalResult{
		BlockedTools: make(map[string]bool),
	}

	schemaVersion, schemaVersionErr := r.storageManager.GetSchemaVersion()
	outputSchemaHashMigration := schemaVersionErr == nil && schemaVersion < storage.OutputSchemaHashSchemaVersion
	migratedOutputSchemaApprovals := false

	for _, tool := range tools {
		// Spec 105 FR-009: every approval record is keyed by the tool's RAW
		// upstream name, exactly as dispatched — never by a suffix guessed from
		// the first colon. A raw "ns:erase" is therefore filed under
		// (server, "ns:erase") and can neither inherit nor disturb the record of
		// a sibling "erase".
		//
		// Legacy collapsed records (one-shot migration story): a store written
		// before this change holds "ns:erase" under the key "erase". Nothing
		// rewrites that record — its raw name cannot be recovered from the
		// collapsed key, so guessing would be a silent approval. Instead the
		// exact keying makes the migration fall out of the first discovery after
		// upgrade: the collapsed record is only ever looked up by a raw "erase"
		// (approving exactly that name and nothing else), while "ns:erase"
		// misses, takes the new-tool branch below and gets its own record —
		// pending under an active quarantine gate, auto-approved otherwise. If
		// the server never served a bare "erase", the collapsed record is
		// simply orphaned; the reader side (internal/server/tool_gate.go) never
		// hands it to another raw name.
		toolName := config.RawToolName(tool)

		// Serialize schema for hashing
		schemaJSON := tool.ParamsJSON
		if schemaJSON == "" {
			// Try to serialize from any parsed schema if available
			schemaJSON = "{}"
		}

		// Normalize JSON schemas before hashing and storage to ensure stable key ordering
		schemaJSON = normalizeJSON(schemaJSON)
		outputSchemaJSON := normalizeJSON(tool.OutputSchemaJSON)

		// Calculate current hash for the full approved contract, including output schema.
		currentHash := calculateToolApprovalHashWithOutputSchema(toolName, tool.Description, schemaJSON, outputSchemaJSON, tool.Annotations)

		// Look up existing approval record
		existing, err := r.storageManager.GetToolApproval(serverName, toolName)

		// Spec 105 FR-009 (migration review, round 6): whether the record
		// this pass READ was still unstamped is decided ONCE, here, and every
		// save of `existing` below goes through saveReadToolApproval with it
		// — the rug-pull mark, the pending/changed holds, the backfill, the
		// formula migrations and the scan helpers alike. A save that stamps
		// mid-branch would otherwise make a later `!existing.IdentityKeyed`
		// read lie, and a pre-105 record that still restricts after the
		// write (user-disabled, pending, changed) must keep waiting unstamped
		// for the namespaced tool it may have been filed for, whichever
		// branch wrote it. A record adopted from a collapsed sibling below
		// (adoptLegacyLockForNewTool) is brand new and never unstamped.
		wasUnstamped := existing != nil && !existing.IdentityKeyed

		// The backfill is for records that HOLD an approved contract. An
		// approved record with an EMPTY ApprovedHash was never baselined (the
		// pre-105 user toggle synthesized that shape; see the never-baselined
		// handling below) and has no contract to re-hash: its stored
		// description/schema are empty, so the fallback to the LIVE contract
		// would make the match trivially true and baseline the record blind
		// to whatever the upstream serves right now — on a store still at
		// schema version 2 that skipped every legacy-lock / rug-pull /
		// Disabled carry-over below (Spec 105 FR-009 migration review, round
		// 5). Such a record is left to adoptLegacyLockOrBaseline, which sets
		// HashSchemaVersion so the migration can still complete.
		if existing != nil && existing.Status == storage.ToolApprovalStatusApproved && existing.ApprovedHash != "" && outputSchemaHashMigration && existing.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion {
			// One-time output-schema hash backfill: previously approved tools did
			// not store outputSchema in the approved contract hash. Rebaseline using
			// the stored approved description/input schema plus the currently observed
			// output schema. If description/input schema changed, route through the
			// normal rug-pull detection path instead of silently approving it.
			storedDesc := existing.CurrentDescription
			storedSchema := existing.CurrentSchema
			if storedDesc == "" {
				storedDesc = tool.Description
			}
			if storedSchema == "" {
				storedSchema = schemaJSON
			}
			descMatch := storedDesc == tool.Description
			schemaMatch := normalizeJSON(storedSchema) == normalizeJSON(schemaJSON)
			if descMatch && schemaMatch {
				backfilledHash := calculateToolApprovalHashWithOutputSchema(toolName, storedDesc, storedSchema, outputSchemaJSON, tool.Annotations)
				existing.ApprovedHash = backfilledHash
				existing.CurrentHash = backfilledHash
				existing.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
				existing.CurrentDescription = storedDesc
				existing.CurrentSchema = normalizeJSON(storedSchema)
				existing.CurrentOutputSchema = outputSchemaJSON
				if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
					r.logger.Debug("Failed to backfill output schema approval hash",
						zap.String("server", serverName),
						zap.String("tool", toolName),
						zap.Error(saveErr))
				} else {
					migratedOutputSchemaApprovals = true
					r.logger.Info("Tool approval hash backfilled with output schema",
						zap.String("server", serverName),
						zap.String("tool", toolName))
				}
				continue
			}
		}

		if err != nil {
			// Spec 105 FR-009, legacy carry-over (lock): the raw name has no
			// exact record, but an UNSTAMPED collapsed sibling may hold the
			// pending/changed lock pre-105 discovery filed FOR this tool.
			// Under an active gate that lock — with its rug-pull / scan-hold
			// evidence — is adopted onto the first exact record instead of
			// re-filing the tool as brand new: the review UI keeps showing
			// the before/after diff, and on a baseline pass the tool stays
			// pending until approved by its own name rather than being
			// auto-baselined (FR-009). Under a lifted gate the lock never
			// bound and the ordinary new-tool ladder below applies.
			if adopted := r.adoptLegacyLockForNewTool(serverName, toolName, priorByName, tool, currentHash, schemaJSON, outputSchemaJSON, gate.active()); adopted != nil {
				existing, err = adopted, nil
			}
		}

		if err != nil {
			// No existing record - this is a new tool. Decide whether it
			// auto-approves and under which provenance label:
			//   - "auto"               quarantine disabled globally or skip_quarantine
			//   - "auto-baseline"      trusted server establishing its baseline (MCP-2931 #1)
			//   - "auto-approve-changes" trusted server, post-baseline addition, operator opted in (MCP-2931 #3)
			//   - "scan-approved"      spec 086: trust_mode:scan, offline TPA scan of
			//                          the new tool came back green (fails closed to
			//                          pending on any non-green/degraded/absent verdict)
			// Otherwise the tool is pending and blocked until reviewed (MCP-2931 #2).
			autoApprove := false
			approvedBy := ""
			// holdEvidence carries the scan verdict + matched TPA/check ids onto
			// the pending record when the scan gate refuses a new tool (FR-018).
			var holdEvidence *scanHoldEvidence

			// Spec 105 FR-009, legacy carry-over: a pre-105 binary filed a raw
			// "ns:erase" under the COLLAPSED key "erase". Its approval stays
			// with the bare name, but a user block it carries must not
			// evaporate the moment this exact record is created — under a
			// lifted gate (quarantine off, skip_quarantine, auto-approve
			// changes, green scan) the new record would otherwise be saved
			// approved+enabled and the operator's block on the namespaced tool
			// would be silently lost. The reader (internal/server/tool_gate.go)
			// only consults the collapsed record while no exact one exists, so
			// the block is copied here once, onto the record that will be read
			// from now on.
			//
			// Here the user's Disabled block is what is carried. A
			// pending/changed LOCK on the sibling is adopted with its
			// evidence under an active gate (adoptLegacyLockForNewTool
			// above, so this branch never sees it); under a lifted gate the
			// lock never bound (the existing promote rule applies) — and it
			// is never converted into a block, which would turn a review
			// hold into a permanent user block that ApproveTools never
			// clears. Only an UNSTAMPED sibling is consulted
			// (storage.ToolApprovalRecord.IdentityKeyed): a record a
			// post-105 binary wrote is a genuine sibling tool, and FR-009
			// forbids inheriting anything from it.
			legacyDisabled := r.legacyCollapsedSiblingDisabled(serverName, toolName, priorByName)
			switch {
			case !enforceNewTools:
				autoApprove, approvedBy = true, "auto"
			case isBaselinePass:
				autoApprove, approvedBy = true, "auto-baseline"
			case autoApproveChanges:
				autoApprove, approvedBy = true, "auto-approve-changes"
			case scanMode:
				if clean, evidence := r.scanChangeIsClean(serverName, tool); clean {
					autoApprove, approvedBy = true, "scan-approved"
				} else {
					holdEvidence = evidence
				}
			}

			if autoApprove {
				now := time.Now().UTC()
				record := &storage.ToolApprovalRecord{
					ServerName:          serverName,
					ToolName:            toolName,
					CurrentHash:         currentHash,
					ApprovedHash:        currentHash,
					HashSchemaVersion:   storage.OutputSchemaHashSchemaVersion,
					Status:              storage.ToolApprovalStatusApproved,
					ApprovedBy:          approvedBy,
					ApprovedAt:          now,
					CurrentDescription:  tool.Description,
					CurrentSchema:       schemaJSON,
					CurrentOutputSchema: outputSchemaJSON,
					Disabled:            legacyDisabled,
				}

				if saveErr := r.saveToolApproval(record); saveErr != nil {
					r.logger.Error("Failed to save auto-approved tool record",
						zap.String("server", serverName),
						zap.String("tool", toolName),
						zap.String("approved_by", approvedBy),
						zap.Error(saveErr))
					continue
				}

				r.logger.Info("New tool discovered, auto-approved",
					zap.String("server", serverName),
					zap.String("tool", toolName),
					zap.String("approved_by", approvedBy),
					zap.Bool("disabled", legacyDisabled))
				r.stampConsultedLegacySibling(serverName, toolName, priorByName)

				r.emitToolQuarantineEvent(serverName, toolName, "tool_auto_approved", "", currentHash,
					"", tool.Description, "", schemaJSON)

				if legacyDisabled {
					result.BlockedTools[toolName] = true
				}
				continue
			}

			// Quarantine enabled, no baseline exemption — new tool requires user
			// review before use. This applies to trusted servers post-baseline
			// (and always to quarantined ones) to prevent injection attacks via
			// new tool additions on compromised servers.
			record := &storage.ToolApprovalRecord{
				ServerName:          serverName,
				ToolName:            toolName,
				CurrentHash:         currentHash,
				HashSchemaVersion:   storage.OutputSchemaHashSchemaVersion,
				Status:              storage.ToolApprovalStatusPending,
				CurrentDescription:  tool.Description,
				CurrentSchema:       schemaJSON,
				CurrentOutputSchema: outputSchemaJSON,
				Disabled:            legacyDisabled,
			}
			holdEvidence.applyTo(record)

			if saveErr := r.saveToolApproval(record); saveErr != nil {
				r.logger.Error("Failed to save tool approval record",
					zap.String("server", serverName),
					zap.String("tool", toolName),
					zap.Error(saveErr))
				continue
			}

			r.logger.Info("New tool discovered, pending approval",
				zap.String("server", serverName),
				zap.String("tool", toolName),
				zap.Bool("server_quarantined", serverQuarantined))
			r.stampConsultedLegacySibling(serverName, toolName, priorByName)

			result.BlockedTools[toolName] = true
			result.PendingCount++

			r.emitToolQuarantineEvent(serverName, toolName, "tool_discovered", "", currentHash,
				"", tool.Description, "", schemaJSON)

			continue
		}

		// Spec 105 FR-009 (migration review): an APPROVED record whose
		// ApprovedHash is EMPTY was never baselined by discovery. The pre-105
		// user toggle (setToolEnabledNoEmit) synthesized exactly this shape
		// under the exact raw name whenever the operator toggled a namespaced
		// tool — while discovery kept the tool's real quarantine lock under the
		// COLLAPSED key "erase". Read on its own, the empty-hash record would
		// (a) never trip the `ApprovedHash != ""` rug-pull guard below, so
		// detection for the tool is dead forever, and (b) shadow the collapsed
		// lock, so a rug-pulled "ns:erase" dispatches for every caller right
		// after upgrade. So, once: if an UNSTAMPED collapsed sibling carries a
		// pending/changed lock, that lock (with its rug-pull evidence) is
		// adopted onto the exact record, which then flows through the ordinary
		// pending/changed handling below. Otherwise the record is NEVER
		// baselined blindly to whatever the upstream serves right now: it is
		// baselined only when an unstamped APPROVED sibling's approved
		// contract matches the current one (the sibling was this tool's real
		// baseline, so the approval is genuine), marked changed when that
		// contract differs (a rug pull across the upgrade), and filed pending
		// under an active gate when no sibling exists at all — an
		// unreviewed tool stays unreviewed. An unstamped sibling's user block
		// (Disabled) is carried onto the exact record on every one of those
		// paths — pre-105 disable_all / block_all wrote it under the collapsed
		// key for this very tool — so the block below (`existing.Disabled`)
		// keeps holding it after the upgrade.
		if existing.Status == storage.ToolApprovalStatusApproved && existing.ApprovedHash == "" {
			// A never-baselined record below the output-schema hash version
			// is the one shape the backfill above skips; adoption stamps the
			// current version onto it, so the migration-complete check must
			// still run at the end of this pass.
			if outputSchemaHashMigration && existing.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion {
				migratedOutputSchemaApprovals = true
			}
			r.adoptLegacyLockOrBaseline(serverName, existing, wasUnstamped, priorByName, tool, currentHash, schemaJSON, outputSchemaJSON, neverBaselinedGate{
				enforceNewTools:    enforceNewTools,
				autoApproveChanges: autoApproveChanges,
				scanMode:           scanMode,
			})
		}

		if existing.Disabled {
			result.BlockedTools[toolName] = true
		}

		// Existing record found - check if hash matches
		if existing.ApprovedHash == currentHash {
			// Spec 105 FR-009: a pre-105 record the pass otherwise leaves
			// untouched is still re-saved once so it carries the
			// identity-keyed stamp — the legacy consults are bounded to the
			// upgrade only if every record a discovery pass sees gets stamped
			// (saveToolApproval stamps on write). The one exception is a
			// record that still RESTRICTS (here: user-disabled — the status
			// is approved below): it may be the collapsed record of a
			// namespaced tool this pass does not list yet, so it keeps
			// waiting unstamped (saveReadToolApproval) and there is nothing
			// to write for it.
			needsSave := wasUnstamped && !existing.Disabled
			if existing.Status != storage.ToolApprovalStatusApproved {
				// Hash matches but status is not approved (e.g., falsely marked "changed"
				// by a previous binary with a different hash formula). Restore to approved.
				if err := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonHashMatch); err != nil {
					result.BlockedTools[toolName] = true
					result.ChangedCount++
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.PreviousDescription = ""
				existing.PreviousSchema = ""
				existing.PreviousOutputSchema = ""
				existing.ClearScanHold()
				needsSave = true
				r.logger.Info("Tool restored to approved (hash matches after formula update)",
					zap.String("server", serverName),
					zap.String("tool", toolName))
			}
			// Update current hash/description/schema in case they differ from storage
			if existing.CurrentHash != currentHash || existing.CurrentOutputSchema != outputSchemaJSON || existing.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion {
				existing.CurrentHash = currentHash
				existing.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
				existing.CurrentDescription = tool.Description
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				needsSave = true
			}
			if needsSave {
				if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
					r.logger.Debug("Failed to update tool approval record",
						zap.String("server", serverName),
						zap.String("tool", toolName),
						zap.Error(saveErr))
				}
			}
			continue
		}

		if existing.Status == storage.ToolApprovalStatusPending {
			// Capture whether the stored hash still matches the live tool BEFORE
			// we overwrite CurrentHash — the baseline migration (MCP-2931 #4)
			// only promotes a stranded pending record that is unchanged since it
			// was recorded.
			priorHashMatches := existing.CurrentHash == currentHash

			// Update current info to the live snapshot.
			existing.CurrentHash = currentHash
			existing.CurrentDescription = tool.Description
			existing.CurrentSchema = schemaJSON
			existing.CurrentOutputSchema = outputSchemaJSON

			// Decide whether this pending record is promoted to approved:
			//   - baseline migration: trusted server, no prior baseline, hash unchanged (MCP-2931 #4)
			//   - auto_approve_tool_changes: operator opted in (MCP-2931 #3)
			promote := false
			var promoteReason TransitionReason
			var promoteBy string
			switch {
			case isBaselinePass && priorHashMatches:
				promote, promoteReason, promoteBy = true, ReasonBaselineTrust, "auto-baseline"
			case autoApproveChanges:
				promote, promoteReason, promoteBy = true, ReasonAutoApproveChanges, "auto-approve-changes"
			}

			if promote {
				if invErr := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, promoteReason); invErr != nil {
					// Refuse to promote on an invariant violation — keep blocked.
					if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
						r.logger.Debug("Failed to update pending tool approval",
							zap.String("server", serverName), zap.String("tool", toolName), zap.Error(saveErr))
					}
					if enforceNewTools {
						result.BlockedTools[toolName] = true
						result.PendingCount++
					}
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.ApprovedHash = currentHash
				existing.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
				existing.ApprovedAt = time.Now().UTC()
				existing.ApprovedBy = promoteBy
				existing.PreviousDescription = ""
				existing.PreviousSchema = ""
				existing.PreviousOutputSchema = ""
				existing.ClearScanHold()
				if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
					r.logger.Error("Failed to promote pending tool approval",
						zap.String("server", serverName), zap.String("tool", toolName),
						zap.String("approved_by", promoteBy), zap.Error(saveErr))
					continue
				}
				r.logger.Info("Pending tool promoted to approved",
					zap.String("server", serverName), zap.String("tool", toolName),
					zap.String("approved_by", promoteBy))
				r.emitToolQuarantineEvent(serverName, toolName, "tool_auto_approved", "", currentHash,
					"", tool.Description, "", schemaJSON)
				continue
			}

			// Stays pending — persist the updated current info.
			if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
				r.logger.Debug("Failed to update pending tool approval",
					zap.String("server", serverName),
					zap.String("tool", toolName),
					zap.Error(saveErr))
			}

			// Two-gate consistency (MCP-2931 #5): block from the index whenever
			// the stored status is pending and quarantine is enforced, matching
			// the call-time gate (internal/server/mcp.go) which blocks on stored
			// pending status regardless of whether the SERVER is quarantined.
			// Previously this only blocked when the server itself was quarantined
			// (enforceQuarantine), so a post-baseline pending tool on a trusted
			// server was indexed/visible but uncallable.
			if enforceNewTools {
				result.BlockedTools[toolName] = true
				result.PendingCount++
			}
			continue
		}

		// If tool was previously marked "changed", check if the tool has reverted
		// to its PREVIOUS (pre-change) contract. Only auto-approve if the WHOLE
		// contract — description AND input/output schemas — matches the APPROVED
		// version, not the current (changed) one. This prevents the bug where a
		// changed tool gets auto-approved on the next checkToolApprovals pass
		// because CurrentDescription was already updated to the new description,
		// and (astra r1 P1) the bug where a schema-only change — description
		// unchanged, so a description-only comparison already "matched" — was
		// restored to approved on the very pass that marked it changed, with the
		// changed schema baselined and the stale PreviousOutputSchema left in
		// place so a genuine revert then read as a rug pull.
		if existing.Status == storage.ToolApprovalStatusChanged {
			// Only restore if the tool reverted to the PREVIOUS (approved) contract
			if revertedToPreviousContract(existing, tool.Description, schemaJSON, outputSchemaJSON) {
				if err := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonDescriptionRevert); err != nil {
					result.BlockedTools[toolName] = true
					result.ChangedCount++
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.ApprovedHash = currentHash
				existing.CurrentHash = currentHash
				existing.CurrentDescription = tool.Description
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousSchema = ""
				existing.PreviousOutputSchema = ""
				existing.ClearScanHold()
				if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr == nil {
					r.logger.Info("Changed tool restored (reverted to previous contract)",
						zap.String("server", serverName),
						zap.String("tool", toolName))
				}
				continue
			}
			// auto_approve_tool_changes (MCP-2931 #3): the operator opted into
			// auto-approving changes for this server, so a still-changed record is
			// re-baselined to its current snapshot instead of staying blocked.
			if autoApproveChanges {
				if invErr := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonAutoApproveChanges); invErr != nil {
					result.BlockedTools[toolName] = true
					result.ChangedCount++
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.ApprovedHash = currentHash
				existing.CurrentHash = currentHash
				existing.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
				existing.ApprovedAt = time.Now().UTC()
				existing.ApprovedBy = "auto-approve-changes"
				existing.CurrentDescription = tool.Description
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousSchema = ""
				existing.PreviousOutputSchema = ""
				existing.ClearScanHold()
				if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr == nil {
					r.logger.Info("Changed tool auto-approved (auto_approve_tool_changes enabled)",
						zap.String("server", serverName),
						zap.String("tool", toolName))
					r.emitToolQuarantineEvent(serverName, toolName, "tool_auto_approved", "", currentHash,
						"", tool.Description, "", schemaJSON)
				}
				continue
			}
			// trust_mode: scan (spec 086 stage 2): a still-changed record is
			// re-baselined ONLY when a synchronous in-process TPA scan of the
			// current (changed) tool returns a green verdict. A non-green/degraded
			// verdict falls through and keeps the tool blocked (fail closed), with
			// the matched TPA/check ids recorded on the record (FR-018).
			if scanMode {
				clean, evidence := r.scanChangeIsClean(serverName, tool)
				if clean && r.scanApproveChange(serverName, toolName, existing, wasUnstamped, tool, schemaJSON, outputSchemaJSON, currentHash) {
					continue
				}
				r.recordScanHold(serverName, toolName, existing, wasUnstamped, evidence)
			}
			// Tool still has the changed description — keep it blocked. A
			// pre-105 record that stays changed still RESTRICTS, so there is
			// no stamp to write for it (Spec 105 FR-009): like a served,
			// user-disabled record it keeps waiting unstamped for a
			// namespaced tool it may have been filed for, and is stamped by
			// the first write that leaves it unrestricted (a revert, an
			// approval by name) or by the pass that files that tool.
			if globalEnabled {
				result.BlockedTools[toolName] = true
				result.ChangedCount++
			}
			continue
		}

		if existing.ApprovedHash != "" && existing.ApprovedHash != currentHash {
			// Before marking as changed, check if this is a hash formula migration.
			// Recompute what the approved hash WOULD be using the STORED description+schema
			// with the CURRENT formula. If it matches the current hash, the tool hasn't
			// actually changed — only the hash formula did.
			storedDesc := existing.CurrentDescription
			storedSchema := existing.CurrentSchema
			storedOutputSchema := existing.CurrentOutputSchema
			if storedDesc == "" {
				storedDesc = tool.Description
			}
			if storedSchema == "" {
				storedSchema = schemaJSON
			}
			if storedOutputSchema == "" {
				storedOutputSchema = outputSchemaJSON
			}
			rehashedFromStored := calculateToolApprovalHashWithOutputSchema(toolName, storedDesc, storedSchema, storedOutputSchema, nil)

			// Also check legacy and with-annotations formulas
			legacyHash := calculateLegacyToolApprovalHash(toolName, tool.Description, schemaJSON)
			annotationsHash := calculateHashWithAnnotations(toolName, tool.Description, schemaJSON, tool.Annotations)

			isFormulaChange := rehashedFromStored == currentHash
			if existing.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion {
				isFormulaChange = isFormulaChange ||
					existing.ApprovedHash == legacyHash ||
					existing.ApprovedHash == annotationsHash
			}

			if isFormulaChange {
				if err := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonFormulaMigration); err != nil {
					result.BlockedTools[toolName] = true
					result.ChangedCount++
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.ApprovedHash = currentHash
				existing.CurrentHash = currentHash
				existing.CurrentDescription = tool.Description
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousSchema = ""
				existing.ClearScanHold()
				if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
					r.logger.Debug("Failed to migrate changed tool approval hash",
						zap.String("server", serverName),
						zap.String("tool", toolName),
						zap.Error(saveErr))
				} else {
					r.logger.Info("Tool approval hash migrated (formula change, not actual tool change)",
						zap.String("server", serverName),
						zap.String("tool", toolName))
				}
				continue
			}

			// Final safety: compare actual text content before flagging as changed.
			// If description AND schema text are identical, this is a hash formula issue,
			// not a real tool change. Auto-approve silently.
			// Content comparison: check if the SEMANTIC content is the same.
			// Multiple sources of hash mismatch are possible:
			// 1. Annotations were included in old hash but excluded now
			// 2. JSON key ordering differs between sessions
			// 3. Whitespace/formatting differences in schema
			//
			// We normalize by comparing description text AND normalized schema JSON.
			// If both match semantically, auto-approve (this is a formula change, not a tool change).
			descMatch := tool.Description == existing.CurrentDescription || existing.CurrentDescription == ""
			var schemaMatch bool
			if existing.CurrentSchema == "" || schemaJSON == existing.CurrentSchema {
				schemaMatch = true
			} else {
				// Normalize both schemas and compare
				schemaMatch = normalizeJSON(schemaJSON) == normalizeJSON(existing.CurrentSchema)
			}
			var outputSchemaMatch bool
			if existing.CurrentOutputSchema == "" || outputSchemaJSON == existing.CurrentOutputSchema {
				outputSchemaMatch = true
			} else {
				outputSchemaMatch = normalizeJSON(outputSchemaJSON) == normalizeJSON(existing.CurrentOutputSchema)
			}
			if descMatch && schemaMatch && outputSchemaMatch {
				if err := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonContentMatch); err != nil {
					result.BlockedTools[toolName] = true
					result.ChangedCount++
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.ApprovedHash = currentHash
				existing.CurrentHash = currentHash
				existing.CurrentDescription = tool.Description
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousSchema = ""
				existing.ClearScanHold()
				if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr == nil {
					r.logger.Info("Tool auto-approved (identical content, hash formula change)",
						zap.String("server", serverName),
						zap.String("tool", toolName))
				}
				continue
			}

			// Log why the content comparison failed for debugging
			r.logger.Warn("Tool hash mismatch not resolved by content comparison",
				zap.String("server", serverName),
				zap.String("tool", toolName),
				zap.Bool("desc_match", descMatch),
				zap.Bool("schema_match", schemaMatch),
				zap.Bool("output_schema_match", outputSchemaMatch),
				zap.Int("stored_desc_len", len(existing.CurrentDescription)),
				zap.Int("current_desc_len", len(tool.Description)),
				zap.Int("stored_schema_len", len(existing.CurrentSchema)),
				zap.Int("current_schema_len", len(schemaJSON)))

			// LAST RESORT: If description and output schema match, auto-approve even
			// if input schema normalization differs. Output schema is part of the
			// approved contract and must not be bypassed here.
			//
			// EXCLUDED under trust_mode: scan. This fallback fires when the input
			// schema genuinely differs after normalization (pure key-order/whitespace
			// noise is already absorbed by the schemaMatch check above), and the
			// scanner covers InputSchema — a TPA payload injected into an
			// input-schema field description would otherwise auto-approve here without
			// ever being scanned. In scan mode we fall through to the scan gate so
			// the changed definition is actually scanned (fail closed on non-green).
			if !scanMode && descMatch && outputSchemaMatch {
				if err := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonDescriptionMatch); err != nil {
					result.BlockedTools[toolName] = true
					result.ChangedCount++
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.ApprovedHash = currentHash
				existing.CurrentHash = currentHash
				existing.CurrentDescription = tool.Description
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousSchema = ""
				existing.ClearScanHold()
				if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr == nil {
					r.logger.Info("Tool auto-approved (description matches, schema format differs)",
						zap.String("server", serverName),
						zap.String("tool", toolName))
				}
				continue
			}

			// Hash differs AND description differs - genuine tool change (rug pull).
			// auto_approve_tool_changes (MCP-2931 #3): when the operator opted into
			// auto-approving changes for this server, re-baseline to the current
			// snapshot instead of flagging it changed — so no `changed` ever surfaces.
			if autoApproveChanges {
				if invErr := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonAutoApproveChanges); invErr != nil {
					result.BlockedTools[toolName] = true
					result.ChangedCount++
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.ApprovedHash = currentHash
				existing.CurrentHash = currentHash
				existing.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
				existing.ApprovedAt = time.Now().UTC()
				existing.ApprovedBy = "auto-approve-changes"
				existing.CurrentDescription = tool.Description
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousSchema = ""
				existing.PreviousOutputSchema = ""
				existing.ClearScanHold()
				if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
					r.logger.Error("Failed to auto-approve changed tool",
						zap.String("server", serverName),
						zap.String("tool", toolName),
						zap.Error(saveErr))
					continue
				}
				r.logger.Info("Tool change auto-approved (auto_approve_tool_changes enabled)",
					zap.String("server", serverName),
					zap.String("tool", toolName))
				r.emitToolQuarantineEvent(serverName, toolName, "tool_auto_approved", "", currentHash,
					"", tool.Description, "", schemaJSON)
				continue
			}

			// trust_mode: scan (spec 086 stage 2) — PRIMARY change gate. A genuine
			// tool change (rug pull) auto-approves ONLY when a synchronous, offline
			// TPA scan of the new tool definition returns a green (clean) verdict.
			// Any non-green ("warnings"/"dangerous"), degraded, or absent verdict
			// falls through to mark-changed below (fail closed) — the change is held
			// for human review exactly as in manual mode.
			// The hold evidence (verdict + matched TPA/check ids) rides along onto
			// the changed record so the tool-approval surfaces can explain the hold
			// (FR-018).
			var holdEvidence *scanHoldEvidence
			if scanMode {
				clean, evidence := r.scanChangeIsClean(serverName, tool)
				if clean && r.scanApproveChange(serverName, toolName, existing, wasUnstamped, tool, schemaJSON, outputSchemaJSON, currentHash) {
					continue
				}
				holdEvidence = evidence
			}

			oldDesc := existing.CurrentDescription
			oldSchema := existing.CurrentSchema
			oldOutputSchema := existing.CurrentOutputSchema
			if existing.Status == storage.ToolApprovalStatusApproved {
				// Transitioning from approved to changed
				oldDesc = existing.CurrentDescription
				oldSchema = existing.CurrentSchema
				oldOutputSchema = existing.CurrentOutputSchema
			}

			existing.Status = storage.ToolApprovalStatusChanged
			existing.PreviousDescription = oldDesc
			existing.PreviousSchema = oldSchema
			existing.PreviousOutputSchema = oldOutputSchema
			existing.CurrentHash = currentHash
			existing.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
			existing.CurrentDescription = tool.Description
			existing.CurrentSchema = schemaJSON
			existing.CurrentOutputSchema = outputSchemaJSON
			holdEvidence.applyTo(existing)

			if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
				r.logger.Error("Failed to update changed tool approval",
					zap.String("server", serverName),
					zap.String("tool", toolName),
					zap.Error(saveErr))
				continue
			}

			r.logger.Warn("Tool description/schema changed since approval (potential rug pull)",
				zap.String("server", serverName),
				zap.String("tool", toolName),
				zap.String("approved_hash", existing.ApprovedHash),
				zap.String("current_hash", currentHash),
				zap.Bool("quarantine_enforced", enforceQuarantine))

			// Always block changed tools when quarantine is globally enabled,
			// even for trusted (non-quarantined) servers.
			// Rug pull detection is a critical security feature.
			if globalEnabled {
				result.BlockedTools[toolName] = true
				result.ChangedCount++
			}

			// Emit activity event for description change
			r.emitToolQuarantineEvent(serverName, toolName, "tool_description_changed",
				existing.ApprovedHash, currentHash,
				oldDesc, tool.Description,
				oldSchema, schemaJSON)
		}
	}

	if migratedOutputSchemaApprovals {
		r.markOutputSchemaHashMigrationCompleteIfReady()
	}

	r.stampRemainingLegacyToolApprovals(serverName)

	if len(result.BlockedTools) > 0 {
		r.logger.Info("Tool-level quarantine: tools blocked",
			zap.String("server", serverName),
			zap.Int("pending", result.PendingCount),
			zap.Int("changed", result.ChangedCount),
			zap.Int("total_blocked", len(result.BlockedTools)))
	}

	return result, nil
}

// revertedToPreviousContract reports whether a changed record's live contract
// is the one it was approved for BEFORE the change: description AND
// input/output schemas (astra r1 P1). A schema-only change keeps the
// description equal, so a description-only comparison would restore the
// record — and baseline the changed schema — on the very pass that marked it
// changed (adoptLegacyLockOrBaseline marks and falls through in one pass; the
// ordinary rug-pull path on the next). A Previous* schema the record never
// stored (a pre-output-schema record, or a producer that recorded only the
// description) is not evidence and is not compared — the same idiom as
// legacySiblingApprovesContract.
func revertedToPreviousContract(existing *storage.ToolApprovalRecord, description, schemaJSON, outputSchemaJSON string) bool {
	if existing.PreviousDescription == "" || description != existing.PreviousDescription {
		return false
	}
	if existing.PreviousSchema != "" && normalizeJSON(existing.PreviousSchema) != normalizeJSON(schemaJSON) {
		return false
	}
	if existing.PreviousOutputSchema != "" && normalizeJSON(existing.PreviousOutputSchema) != normalizeJSON(outputSchemaJSON) {
		return false
	}
	return true
}

// saveToolApproval persists an approval record through the ONLY write seam
// the runtime uses, stamping it as identity-keyed first (Spec 105 FR-009,
// storage.ToolApprovalRecord.IdentityKeyed): every record this binary writes
// is keyed by the exact raw tool name, and the stamp is what tells the legacy
// consults (legacyCollapsedSibling) that a record is a genuine sibling rather
// than a pre-105 collapsed one.
func (r *Runtime) saveToolApproval(record *storage.ToolApprovalRecord) error {
	record.IdentityKeyed = true
	return r.storageManager.SaveToolApproval(record)
}

// saveReadToolApproval is saveToolApproval for a record that was READ from
// the store and mutated in place, with one exception (Spec 105 FR-009
// migration review, round 5): a record that was UNSTAMPED when read
// (wasUnstamped) and still Restricts() after the write — user-disabled,
// pending or changed — is written back WITHOUT the identity-keyed stamp. A
// pre-105 record is a collapsed one until proven otherwise, and a restricting
// collapsed record may be the only record of a block or lock on a namespaced
// tool that has not been filed yet: the operator disabling or blocking
// "erase" between the upgrade and the server's first discovery, or a bare
// "erase" served on the first pass while "ns:erase" only appears on a later
// one. Stamping it there would end the legacy consult
// (legacyCollapsedSibling / the reader's re-admission) before the namespaced
// tool ever existed, and the block would evaporate the moment its exact
// record was filed approved+enabled. So such a record keeps waiting — the
// same window stampRemainingLegacyToolApprovals already grants a restricting
// orphan — while it restricts, and is stamped by the first write that leaves
// it unrestricted (a consult never stamps a restricting record, see
// stampConsultedLegacySibling). Brand-new records never take this path: they
// are this binary's own and are stamped by saveToolApproval.
func (r *Runtime) saveReadToolApproval(record *storage.ToolApprovalRecord, wasUnstamped bool) error {
	if wasUnstamped && record.Restricts() {
		record.IdentityKeyed = false
		return r.storageManager.SaveToolApproval(record)
	}
	return r.saveToolApproval(record)
}

// stampRemainingLegacyToolApprovals bounds the Spec 105 FR-009 legacy logic to
// the upgrade: after a discovery pass has processed every tool the server
// currently lists (each of those records is saved, and so stamped, by the
// pass itself — except one that still restricts, which saveReadToolApproval
// leaves waiting for the same reason as the orphan rule below), every OTHER
// record the server still holds unstamped — a
// collapsed pre-105 record whose bare name is not served, or a record for a
// tool the upstream no longer lists — is stamped identity-keyed in one write.
// From then on legacyCollapsedSibling and the reader's legacy re-admission
// (internal/server/tool_gate.go) consult nothing on this server: a sibling
// record is a genuine sibling's own, and a later "v2:erase" inherits nothing
// from an "erase" the operator disables months after the upgrade.
//
// The one exception is an orphan that still RESTRICTS
// (storage.ToolApprovalRecord.Restricts: user-disabled, pending or changed).
// Such a record is a pre-105 decision about a tool that is merely absent
// from this pass — not served in the first pass after upgrade, or dropped by
// an authoritative empty refresh — and a collapsed one may be the only
// record of a block or lock on a namespaced tool (#873 evicts a disabled
// tool from the index, so pre-105 it survived exactly such an absence). It
// is left unstamped so it waits for its tool to reappear and is consulted
// while it restricts, by every pass that files an exact record collapsing to
// it (stampConsultedLegacySibling stamps it only once it no longer
// restricts). Approved, enabled orphans carry only an approval, which
// belongs to the exact name they store, and are stamped here.
func (r *Runtime) stampRemainingLegacyToolApprovals(serverName string) {
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		r.logger.Debug("Failed to list tool approvals for identity stamping",
			zap.String("server", serverName), zap.Error(err))
		return
	}
	var candidates []string
	var deferred []string
	for _, rec := range records {
		if rec == nil || rec.IdentityKeyed {
			continue
		}
		if rec.Restricts() {
			deferred = append(deferred, rec.ToolName)
			continue
		}
		candidates = append(candidates, rec.ToolName)
	}
	if len(deferred) > 0 {
		r.logger.Info("Pre-105 tool approval records that still restrict (disabled / pending / changed) are left unstamped until their tool is listed again",
			zap.String("server", serverName), zap.Strings("tools", deferred))
	}
	if len(candidates) == 0 {
		return
	}
	if r.legacyStampBeforeWrite != nil {
		r.legacyStampBeforeWrite()
	}
	// The listing above is a detached snapshot and nothing serialises this
	// sweep against an operator write (SetToolEnabled / BlockTools) on one of
	// the listed records. The stamp therefore never writes the listed copies
	// back (astra r1 P4: a stale copy would have discarded a concurrent
	// Disabled=true AND stamped the record): the store re-reads each record
	// inside the write transaction, under the same manager lock the operator
	// write takes, and touches only the IdentityKeyed bit of a record that is
	// still unstamped and still unrestricting at that instant.
	names, err := r.storageManager.StampToolApprovalsIdentityKeyed(serverName, candidates)
	if err != nil {
		r.logger.Warn("Failed to stamp remaining legacy tool approval records identity-keyed",
			zap.String("server", serverName), zap.Int("count", len(candidates)), zap.Error(err))
		return
	}
	if len(names) == 0 {
		return
	}
	r.logger.Info("Stamped remaining pre-105 tool approval records identity-keyed; legacy collapsed-name consults end for this server",
		zap.String("server", serverName), zap.Strings("tools", names))
}

// legacyCollapsedSibling returns the pre-Spec-105 COLLAPSED record for a raw
// name — the record an older binary filed under the text after the first
// colon ("erase" for a raw "ns:erase") — or nil when there is none: the raw
// name has no colon, the store holds nothing under the collapsed key, or the
// record there is STAMPED identity-keyed (written by a post-105 binary, so it
// is a genuine sibling tool's own record and lends nothing to another raw
// name). It reads from the pre-pass listing (prior) so the outcome does not
// depend on the order in which the pass processes the two names.
func legacyCollapsedSibling(rawName string, prior map[string]*storage.ToolApprovalRecord) *storage.ToolApprovalRecord {
	_, collapsed, ok := strings.Cut(rawName, ":")
	if !ok {
		return nil
	}
	sibling := prior[collapsed]
	if sibling == nil || sibling.IdentityKeyed {
		return nil
	}
	return sibling
}

// legacyCollapsedSiblingDisabled reports whether an unstamped collapsed
// sibling carries the user's Disabled block, which the first exact record for
// the raw name inherits (checkToolApprovals new-tool branch). A pending or
// changed lock on the sibling is deliberately NOT reported: it is a review
// hold, not a user decision, and the exact record's own status represents
// it. The hit is logged at Warn with both keys so the first-start churn after
// upgrade is diagnosable.
func (r *Runtime) legacyCollapsedSiblingDisabled(serverName, rawName string, prior map[string]*storage.ToolApprovalRecord) bool {
	sibling := legacyCollapsedSibling(rawName, prior)
	if sibling == nil || !sibling.Disabled {
		return false
	}
	r.logger.Warn("Legacy collapsed approval record is user-disabled; carrying the block onto the namespaced tool's exact-name record",
		zap.String("server", serverName),
		zap.String("tool", rawName),
		zap.String("legacy_key", sibling.ToolName),
		zap.String("legacy_status", sibling.Status))
	return true
}

// stampConsultedLegacySibling ends the legacy consults for a collapsed
// record once the pass has filed (or adopted onto) the exact record of the
// namespaced tool it was consulted for — PROVIDED the record no longer
// restricts: it is re-read and stamped identity-keyed so a later "v3:erase"
// inherits nothing from it. A record that still RESTRICTS (user-disabled,
// pending or changed) is never stamped by a consult (astra r1 P3): filing one
// namespaced identity does not prove every pre-105 tool the collapsed record
// protected has been migrated. "v1:erase" and "v2:erase" both collapsed to
// "erase", and pre-105 a user block on "erase" bound BOTH through the
// collapsed key; if v2:erase is absent from the pass that files v1:erase and
// returns later, a stamp here would let it file approved+enabled under a
// lifted gate — the operator's block evaporating silently (fail-open). So a
// restricting record keeps waiting, consulted by every namespaced identity
// that collapses to it, and is stamped by the first write that leaves it
// unrestricted (saveReadToolApproval: the operator enabling or approving it
// by its own key, a served bare name that stops restricting). The accepted
// cost is exactly origin/main's behaviour: a user-disabled orphan "erase"
// keeps lending its block to every future "*:erase" (one un-hide per new
// tool), and a pending/changed orphan lends its lock, until the operator
// acts on "erase" itself. The fresh read (never the pre-pass listing) is
// what keeps this from acting on a record the same pass already updated,
// e.g. when the bare name is served too and was processed first — but it
// only decides the log line: the stamp itself goes through
// StampToolApprovalsIdentityKeyed, which re-reads the record inside one
// write transaction under the manager lock and touches only IdentityKeyed
// on a record that is still unstamped and still unrestricting at that
// instant (astra r2 C1). Nothing serialises a discovery pass against an
// operator SetToolEnabled / BlockTools on the sibling, and writing the
// detached copy back would have discarded a Disabled=true that landed in
// the window AND stamped the record — the same defect the sweep
// (stampRemainingLegacyToolApprovals) was cured of.
func (r *Runtime) stampConsultedLegacySibling(serverName, rawName string, prior map[string]*storage.ToolApprovalRecord) {
	sibling := legacyCollapsedSibling(rawName, prior)
	if sibling == nil {
		return
	}
	rec, err := r.storageManager.GetToolApproval(serverName, sibling.ToolName)
	if err != nil || rec == nil || rec.IdentityKeyed {
		return
	}
	if rec.Restricts() {
		r.logger.Info("Legacy collapsed approval record consulted for a namespaced tool still restricts; left unstamped so it keeps binding every tool that collapses to it",
			zap.String("server", serverName), zap.String("tool", rawName), zap.String("legacy_key", sibling.ToolName))
		return
	}
	if r.consultStampBeforeWrite != nil {
		r.consultStampBeforeWrite()
	}
	names, err := r.storageManager.StampToolApprovalsIdentityKeyed(serverName, []string{sibling.ToolName})
	if err != nil {
		r.logger.Debug("Failed to stamp consulted legacy collapsed approval record",
			zap.String("server", serverName), zap.String("legacy_key", sibling.ToolName), zap.Error(err))
		return
	}
	if len(names) == 0 {
		// Restricted or stamped by a concurrent write since the read above:
		// the operator's decision is the last word and stays unstamped.
		return
	}
	r.logger.Info("Legacy collapsed approval record consulted for its namespaced tool; stamped identity-keyed, legacy consults for it end",
		zap.String("server", serverName), zap.String("tool", rawName), zap.String("legacy_key", sibling.ToolName))
}

// neverBaselinedGate is the slice of checkToolApprovals' gate resolution that
// adoptLegacyLockOrBaseline needs to decide what a never-baselined exact
// record becomes when no legacy sibling carries a lock for it.
type neverBaselinedGate struct {
	enforceNewTools    bool
	autoApproveChanges bool
	scanMode           bool
}

// lifted reports whether a new, unreviewed tool would auto-approve on this
// server (checkToolApprovals' new-tool branch minus the baseline pass, which
// can never apply here: the never-baselined record itself is approved, so the
// server already has a baseline).
func (g neverBaselinedGate) lifted() bool {
	return !g.enforceNewTools || g.autoApproveChanges
}

// adoptLegacyLockOrBaseline handles an APPROVED exact record with an EMPTY
// ApprovedHash (see the call site in checkToolApprovals). The exact record was
// synthesized by the pre-105 user toggle and carries no approval decision, so
// the decision is derived from the UNSTAMPED collapsed sibling — the record
// pre-105 discovery actually kept for this tool — and from the server's gate:
//
//   - sibling pending/changed: the lock and its rug-pull evidence (Previous*
//     contract, scan-hold reason) are adopted onto the exact record so the
//     tool stays held under its own name until an operator approves it BY
//     that name;
//   - sibling approved with a contract: the exact record is baselined ONLY
//     when the sibling's approved contract equals the current one hashed
//     under the collapsed name (any of the hash formulas the rug-pull guard
//     itself accepts, or identical stored text); otherwise the tool changed
//     while it had no live baseline — while the proxy was down, across the
//     upgrade, or under a compromised upstream — and it is marked changed
//     with the sibling's contract as the Previous* evidence, exactly what the
//     pre-105 binary would have done to the collapsed record;
//   - no usable sibling: under an active gate the record is filed pending
//     like any new, unreviewed tool (scan trust: a green offline scan
//     approves it, anything else holds it with the evidence); under a lifted
//     gate (quarantine off, skip_quarantine, trust_mode auto) it is
//     baselined to the current contract, which is what a new tool would get.
//
// Whenever an unstamped sibling exists, its user block (Disabled) is OR'd
// onto the exact record on every branch: pre-105 disable_all / block_all
// wrote the block under the collapsed key for this very tool, and the
// toggle-shaped exact record (enabled) would otherwise shadow it the moment
// it is baselined or adopts the lock — an operator-disabled namespaced tool
// must stay disabled after the upgrade, not dispatch on every path.
//
// Either way the current contract fields are refreshed and the record is
// saved through saveReadToolApproval with the caller's wasUnstamped (round
// 6): a never-baselined record that adopts a lock or a block still
// RESTRICTS, and a pre-105 record that restricts keeps waiting unstamped —
// the pre-105 bulk toggle collapsed names too, so an empty-hash "erase" it
// minted may be the only record of a block on "ns:erase". It is stamped by
// the first write that leaves it unrestricted (an approval by its own name)
// or by the pass that files the namespaced tool. A failed save leaves the
// in-memory record adopted / held / baselined so this pass still answers
// correctly, and the next pass retries the write. The record then flows
// through the ordinary approved / pending / changed handling in
// checkToolApprovals.
func (r *Runtime) adoptLegacyLockOrBaseline(serverName string, existing *storage.ToolApprovalRecord, wasUnstamped bool, prior map[string]*storage.ToolApprovalRecord, tool *config.ToolMetadata, currentHash, schemaJSON, outputSchemaJSON string, gate neverBaselinedGate) {
	toolName := existing.ToolName
	existing.CurrentHash = currentHash
	existing.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
	existing.CurrentDescription = tool.Description
	existing.CurrentSchema = schemaJSON
	existing.CurrentOutputSchema = outputSchemaJSON

	sibling := legacyCollapsedSibling(toolName, prior)
	if sibling != nil && sibling.Disabled && !existing.Disabled {
		existing.Disabled = true
		r.logger.Warn("Legacy collapsed approval record is user-disabled; carrying the block onto the never-baselined exact-name record",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.String("legacy_key", sibling.ToolName),
			zap.String("legacy_status", sibling.Status))
	}
	switch {
	case sibling != nil && (sibling.Status == storage.ToolApprovalStatusPending || sibling.Status == storage.ToolApprovalStatusChanged):
		existing.Status = sibling.Status
		existing.PreviousDescription = sibling.PreviousDescription
		existing.PreviousSchema = sibling.PreviousSchema
		existing.PreviousOutputSchema = sibling.PreviousOutputSchema
		existing.HeldReason = sibling.HeldReason
		existing.HeldVerdict = sibling.HeldVerdict
		existing.HeldSignals = append([]string(nil), sibling.HeldSignals...)
		r.logger.Warn("Never-baselined exact approval record adopts the lock of its legacy collapsed record",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.String("legacy_key", sibling.ToolName),
			zap.String("adopted_status", sibling.Status))
	case sibling != nil && sibling.Status == storage.ToolApprovalStatusApproved && sibling.ApprovedHash != "":
		if legacySiblingApprovesContract(sibling, tool, schemaJSON, outputSchemaJSON) {
			existing.ApprovedHash = currentHash
			r.logger.Info("Never-baselined exact approval record baselined from its legacy collapsed record's approved contract; rug-pull detection resumes",
				zap.String("server", serverName),
				zap.String("tool", toolName),
				zap.String("legacy_key", sibling.ToolName))
			break
		}
		// The contract the operator approved (under the collapsed key) is not
		// the one the upstream serves now: a rug pull the exact record would
		// otherwise have hidden forever. Hold it with the approved contract as
		// the evidence, as the collapsed record itself would have been marked.
		existing.Status = storage.ToolApprovalStatusChanged
		existing.PreviousDescription = sibling.CurrentDescription
		existing.PreviousSchema = sibling.CurrentSchema
		existing.PreviousOutputSchema = sibling.CurrentOutputSchema
		existing.ClearScanHold()
		r.logger.Warn("Never-baselined exact approval record differs from its legacy collapsed record's approved contract; held as changed (potential rug pull)",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.String("legacy_key", sibling.ToolName),
			zap.String("legacy_approved_hash", sibling.ApprovedHash),
			zap.String("current_hash", currentHash))
		r.emitToolQuarantineEvent(serverName, toolName, "tool_description_changed",
			sibling.ApprovedHash, currentHash,
			sibling.CurrentDescription, tool.Description,
			sibling.CurrentSchema, schemaJSON)
	case gate.lifted():
		existing.ApprovedHash = currentHash
		r.logger.Info("Never-baselined exact approval record baselined to its current contract (quarantine gate lifted for the server); rug-pull detection resumes",
			zap.String("server", serverName),
			zap.String("tool", toolName))
	case gate.scanMode:
		if clean, evidence := r.scanChangeIsClean(serverName, tool); clean {
			existing.ApprovedHash = currentHash
			existing.ApprovedAt = time.Now().UTC()
			existing.ApprovedBy = "scan-approved"
			existing.ClearScanHold()
			r.logger.Info("Never-baselined exact approval record scan-approved (trust_mode: scan, clean verdict); rug-pull detection resumes",
				zap.String("server", serverName),
				zap.String("tool", toolName))
		} else {
			existing.Status = storage.ToolApprovalStatusPending
			evidence.applyTo(existing)
			r.logger.Warn("Never-baselined exact approval record has no legacy baseline and did not scan clean; filed pending under its own name",
				zap.String("server", serverName),
				zap.String("tool", toolName))
		}
	default:
		// Active gate, nothing to inherit: the tool was never reviewed under
		// any name, so it is pending like any new tool until an operator
		// approves it by its own name.
		existing.Status = storage.ToolApprovalStatusPending
		existing.ClearScanHold()
		r.logger.Warn("Never-baselined exact approval record has no legacy baseline; filed pending under its own name for review",
			zap.String("server", serverName),
			zap.String("tool", toolName))
	}

	if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
		r.logger.Error("Failed to save never-baselined tool approval record",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.Error(saveErr))
		return
	}
	r.stampConsultedLegacySibling(serverName, toolName, prior)
}

// adoptLegacyLockForNewTool handles a raw name with NO exact record whose
// UNSTAMPED collapsed sibling carries a pending/changed lock — the lock
// pre-105 discovery filed for this very tool under the collapsed key. Under
// an active gate (activeGate: quarantine enforced for the server, no
// auto-approve opt-in; scan trust included) the first exact record is
// created by adopting that lock with everything the review UI needs — the
// Previous* contract of a rug pull, the scan-hold reason/verdict/signals, the
// user's Disabled block — and the record is saved (stamped) and returned so
// checkToolApprovals runs it through the ORDINARY pending/changed handling:
//
//   - pending: CurrentHash is the sibling's (hashed under the collapsed
//     name), so the baseline-pass promotion's priorHashMatches guard can
//     never fire — the tool stays pending until approved by its own name
//     (FR-009), exactly as origin/main held the collapsed record;
//   - changed: the live contract is the current one and the sibling's
//     approved contract the before-evidence; a revert to it restores the
//     record, anything else stays held (a green scan may approve it under
//     scan trust, as for any changed record).
//
// Under a lifted gate it returns nil: the lock never bound and the
// new-tool ladder's promote rule applies. A stamped sibling is never
// consulted (FR-009: nothing is inherited from a genuine sibling tool).
func (r *Runtime) adoptLegacyLockForNewTool(serverName, toolName string, prior map[string]*storage.ToolApprovalRecord, tool *config.ToolMetadata, currentHash, schemaJSON, outputSchemaJSON string, activeGate bool) *storage.ToolApprovalRecord {
	if !activeGate {
		return nil
	}
	sibling := legacyCollapsedSibling(toolName, prior)
	if sibling == nil {
		return nil
	}
	if sibling.Status != storage.ToolApprovalStatusPending && sibling.Status != storage.ToolApprovalStatusChanged {
		return nil
	}

	record := &storage.ToolApprovalRecord{
		ServerName:           serverName,
		ToolName:             toolName,
		Status:               sibling.Status,
		HashSchemaVersion:    storage.OutputSchemaHashSchemaVersion,
		ApprovedAt:           sibling.ApprovedAt,
		ApprovedBy:           sibling.ApprovedBy,
		PreviousDescription:  sibling.PreviousDescription,
		PreviousSchema:       sibling.PreviousSchema,
		PreviousOutputSchema: sibling.PreviousOutputSchema,
		HeldReason:           sibling.HeldReason,
		HeldVerdict:          sibling.HeldVerdict,
		HeldSignals:          append([]string(nil), sibling.HeldSignals...),
		Disabled:             r.legacyCollapsedSiblingDisabled(serverName, toolName, prior),
	}
	if sibling.Status == storage.ToolApprovalStatusPending {
		// What was pending: the sibling's recorded contract. The pending
		// branch refreshes it to the live one right after it has compared
		// the two.
		record.CurrentHash = sibling.CurrentHash
		record.CurrentDescription = sibling.CurrentDescription
		record.CurrentSchema = sibling.CurrentSchema
		record.CurrentOutputSchema = sibling.CurrentOutputSchema
	} else {
		record.CurrentHash = currentHash
		record.CurrentDescription = tool.Description
		record.CurrentSchema = schemaJSON
		record.CurrentOutputSchema = outputSchemaJSON
	}

	if saveErr := r.saveToolApproval(record); saveErr != nil {
		r.logger.Error("Failed to save tool approval record adopting its legacy collapsed record's lock",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.Error(saveErr))
	} else {
		r.stampConsultedLegacySibling(serverName, toolName, prior)
	}
	r.logger.Warn("New exact approval record adopts the lock of its legacy collapsed record",
		zap.String("server", serverName),
		zap.String("tool", toolName),
		zap.String("legacy_key", sibling.ToolName),
		zap.String("adopted_status", sibling.Status),
		zap.Bool("disabled", record.Disabled))

	if sibling.Status == storage.ToolApprovalStatusChanged {
		r.emitToolQuarantineEvent(serverName, toolName, "tool_description_changed",
			sibling.ApprovedHash, currentHash,
			sibling.PreviousDescription, tool.Description,
			sibling.PreviousSchema, schemaJSON)
	} else {
		r.emitToolQuarantineEvent(serverName, toolName, "tool_discovered", "", currentHash,
			"", tool.Description, "", schemaJSON)
	}
	return record
}

// legacySiblingApprovesContract reports whether an unstamped, approved
// collapsed record's approved contract is the tool's CURRENT contract. The
// pre-105 producer hashed the contract under the COLLAPSED name, so the
// current description / schemas are hashed under that name with every formula
// the rug-pull guard in checkToolApprovals accepts for a pre-output-schema
// record (current, legacy, with-annotations); as a last resort the stored
// text is compared when the sibling's stored current contract is the one it
// approved. Anything else is a contract the operator never approved.
func legacySiblingApprovesContract(sibling *storage.ToolApprovalRecord, tool *config.ToolMetadata, schemaJSON, outputSchemaJSON string) bool {
	collapsed := sibling.ToolName
	if sibling.ApprovedHash == calculateToolApprovalHashWithOutputSchema(collapsed, tool.Description, schemaJSON, outputSchemaJSON, tool.Annotations) {
		return true
	}
	if sibling.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion {
		if sibling.ApprovedHash == calculateLegacyToolApprovalHash(collapsed, tool.Description, schemaJSON) ||
			sibling.ApprovedHash == calculateHashWithAnnotations(collapsed, tool.Description, schemaJSON, tool.Annotations) {
			return true
		}
	}
	if sibling.ApprovedHash != sibling.CurrentHash || sibling.CurrentDescription == "" {
		return false
	}
	descMatch := sibling.CurrentDescription == tool.Description
	schemaMatch := sibling.CurrentSchema == "" || normalizeJSON(sibling.CurrentSchema) == normalizeJSON(schemaJSON)
	outputMatch := sibling.CurrentOutputSchema == "" || normalizeJSON(sibling.CurrentOutputSchema) == normalizeJSON(outputSchemaJSON)
	return descMatch && schemaMatch && outputMatch
}

func (r *Runtime) markOutputSchemaHashMigrationCompleteIfReady() {
	if r.storageManager == nil {
		return
	}

	records, err := r.storageManager.ListToolApprovals("")
	if err != nil {
		r.logger.Debug("Failed to list tool approvals for output schema hash migration",
			zap.Error(err))
		return
	}

	for _, record := range records {
		if record.Status == storage.ToolApprovalStatusApproved && record.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion {
			return
		}
	}

	if err := r.storageManager.SetSchemaVersion(storage.OutputSchemaHashSchemaVersion); err != nil {
		r.logger.Debug("Failed to mark output schema hash migration complete",
			zap.Error(err))
		return
	}

	r.logger.Info("Output schema hash migration completed")
}

// serverEligibleForIndexing reports whether the named server may (re)enter the
// search index: it must exist in config, be enabled, and NOT be quarantined.
// applyDifferentialToolUpdate does not itself withhold a quarantined server's
// tools (checkToolApprovals computes enforceQuarantine but only logs it), so
// every path that feeds it — the approval-driven reindex and the single-server
// DiscoverAndIndexToolsForServer reached by the upstream_servers "refresh" op
// and the reactive discovery callbacks — must gate on this first. Otherwise a
// quarantined server's (possibly poisoned) tool descriptions leak into the
// index the search-side quarantine model deliberately withholds (issue #873).
// A disabled server likewise has no business (re)entering the index.
func (r *Runtime) serverEligibleForIndexing(serverName string) bool {
	for _, candidate := range r.Config().Servers {
		if candidate.Name == serverName {
			return candidate.Enabled && !candidate.Quarantined
		}
	}
	return false
}

// applyServerDiffIfEligible is the guarded per-server index write shared by the
// discovery sweep's primary loop and its last-good fallback (issue #873). It
// re-checks eligibility immediately before the write so a server quarantined or
// disabled mid-sweep — or one that somehow reached this point (e.g. a stale
// last-good snapshot for a since-quarantined server) — is never (re)indexed.
// Returns true only when the differential update actually ran.
func (r *Runtime) applyServerDiffIfEligible(ctx context.Context, serverName string, tools []*config.ToolMetadata) bool {
	if !r.serverEligibleForIndexing(serverName) {
		r.logger.Info("Skipping sweep index write for ineligible server (disabled or quarantined)",
			zap.String("server", serverName))
		return false
	}
	if err := r.applyDifferentialToolUpdate(ctx, serverName, tools); err != nil {
		r.logger.Error("Failed to apply differential update for server during sweep",
			zap.String("server", serverName),
			zap.Error(err))
		return false
	}
	return true
}

// reindexServerToolsAfterApprovalChange reconciles the search index with a
// just-mutated set of tool-approval records for one server (issue #873).
// Approval mutations (ApproveTools / BlockTools / SetToolEnabled and their bulk
// variants) only write BBolt records and emit events; indexing otherwise
// happens solely in applyDifferentialToolUpdate, reached from discovery (boot
// sweep, periodic sweep, connect, tools/list_changed). Without this an approved
// tool is callable immediately (call-time gates read BBolt live) but stays
// invisible to retrieve_tools/describe_tool until the next sweep, and a
// blocked/disabled tool lingers in the index just as long.
//
// Callers invoke this in a goroutine so request handlers return fast.
func (r *Runtime) reindexServerToolsAfterApprovalChange(serverName string) {
	if r.indexManager == nil || r.storageManager == nil {
		return
	}

	// SECURITY-CRITICAL GUARD: only reindex a server that exists, is enabled,
	// and is NOT quarantined (see serverEligibleForIndexing).
	if !r.serverEligibleForIndexing(serverName) {
		return
	}

	// Prefer the last-good snapshot so the reindex is a pure local diff with no
	// network round-trip. It is populated by the full sweep and by
	// DiscoverAndIndexToolsForServer.
	snapshot := r.lastGoodToolsSnapshot(serverName)
	if len(snapshot) == 0 {
		// No snapshot captured yet — fall back to a full single-server
		// rediscover, which indexes and populates the snapshot for next time.
		// Use the authoritative path so a server now reporting zero tools has
		// stale entries cleared rather than resurfaced (issue #873).
		if err := r.RefreshServerTools(r.AppContext(), serverName); err != nil {
			r.logger.Debug("Approval-driven rediscovery failed",
				zap.String("server", serverName), zap.Error(err))
		}
		return
	}

	// TOCTOU GUARD (issue #873): eligibility was checked above, before this
	// (fast, local) snapshot diff. Re-check immediately before the index write
	// so a server quarantined in the interim — whose tools QuarantineServer
	// already deleted from the index — is not re-populated. A microsecond window
	// remains between this check and the mutation inside applyDifferentialToolUpdate.
	if !r.serverEligibleForIndexing(serverName) {
		return
	}

	// Re-run the differential update against the current approval records.
	// Approved records now pass checkToolApprovals and get indexed;
	// blocked/disabled ones are removed. Rug-pull safety is preserved: if the
	// tool mutated after approval, checkToolApprovals re-flags it changed and it
	// stays blocked.
	if err := r.applyDifferentialToolUpdate(r.AppContext(), serverName, snapshot); err != nil {
		r.logger.Warn("Failed to reindex tools after approval change",
			zap.String("server", serverName), zap.Error(err))
	}
}

// ApproveTools approves specific tools for a server, updating their status to approved.
func (r *Runtime) ApproveTools(serverName string, toolNames []string, approvedBy string) error {
	if r.storageManager == nil {
		return nil
	}

	approved := 0
	for _, toolName := range toolNames {
		record, err := r.storageManager.GetToolApproval(serverName, toolName)
		if err != nil {
			r.logger.Warn("Tool approval record not found for approval",
				zap.String("server", serverName),
				zap.String("tool", toolName),
				zap.Error(err))
			continue
		}

		if err := r.enforceInvariant(serverName, toolName, record.Status, storage.ToolApprovalStatusApproved, ReasonUserApprove); err != nil {
			return err
		}

		// Spec 105 FR-009: an operator write on a pre-105 record must not
		// end its legacy consult while it still restricts (saveReadToolApproval).
		wasUnstamped := !record.IdentityKeyed

		record.Status = storage.ToolApprovalStatusApproved
		record.ApprovedHash = record.CurrentHash
		record.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
		record.ApprovedAt = time.Now().UTC()
		record.ApprovedBy = approvedBy
		record.PreviousDescription = ""
		record.PreviousSchema = ""
		record.PreviousOutputSchema = ""
		record.ClearScanHold()

		if err := r.saveReadToolApproval(record, wasUnstamped); err != nil {
			return err
		}
		approved++

		r.logger.Info("Tool approved",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.String("approved_by", approvedBy))

		// Emit activity event
		r.emitToolQuarantineEvent(serverName, toolName, "tool_approved",
			"", record.ApprovedHash, "", record.CurrentDescription, "", record.CurrentSchema)
	}

	// Notify SSE subscribers that the server's tool-quarantine counts changed.
	// Without this, a Servers/overview page open in another tab/window keeps
	// showing the stale "N pending approval" badge until the user manually
	// reloads — see issue #438. Emit once per call (not per tool) to keep
	// the bus quiet on bulk approvals.
	if approved > 0 {
		r.emitServersChanged("tools_approved", map[string]any{
			"server":         serverName,
			"approved_count": approved,
			"approved_by":    approvedBy,
		})
		// Index the newly approved tools now (issue #873) instead of waiting for
		// the next discovery sweep. Async so the caller returns fast. Also covers
		// ApproveAllTools and approveBaselineToolsForServer, which delegate here.
		go r.reindexServerToolsAfterApprovalChange(serverName)
	}

	return nil
}

// setToolEnabledNoEmit applies the visibility toggle without firing the
// servers.changed SSE event. Returns (changed, err) where `changed` reports
// whether record.Disabled actually flipped. The per-tool activity event
// (emitToolQuarantineEvent) still fires on a real flip — that's the audit
// trail consumers expect per-tool. SSE-level emission is the caller's job:
//   - SetToolEnabled (single-toggle wrapper) emits per call.
//   - SetAllToolsEnabled (bulk) emits exactly once after the loop.
//
// Why split this out: emitServersChanged ultimately materialises an SSE
// payload that requires a ListServers call plus an N-row BBolt scan per
// server. Calling it inside a bulk loop is K×(1+N) BBolt ops where K-1 of
// those builds get coalesced away. Mirrors ApproveAllTools, which already
// emits once after its loop instead of per item.
//
// A tool approval record is created on demand when one does not yet exist —
// without this, callers would only be able to toggle tools that had already
// transited the quarantine flow. The toggle expresses user visibility intent,
// not a quarantine decision, so the synthesized record never mints an
// approval nobody made (Spec 105 FR-009, research D4): while the tool-level
// quarantine gate is ACTIVE for the server a record-less tool is pending at
// every gate, and the record is filed `pending` — with the toggle's Disabled
// flag and the snapshot's current contract — so it stays refused until an
// operator approves it by its own name (an "approved" record with an empty
// hash would read as ready and dispatch for every caller until the next
// discovery pass re-filed it pending). Under a lifted gate (quarantine off,
// skip_quarantine, trust_mode auto) a new tool auto-approves anyway, so the
// record is `approved` and baselined to the snapshot's current contract
// (ApprovedHash = current hash) so rug-pull detection works from the first
// discovery; a tool the snapshot does not hold is approved without a hash
// and baselined by the next pass (adoptLegacyLockOrBaseline).
//
// Critical: we ONLY synthesize on storage.ErrToolApprovalNotFound. Any other
// GetToolApproval error (decode failure, closed DB, mmap remap during
// compaction, …) is propagated to the caller. Without this check, a transient
// I/O / unmarshal error could silently demote a `pending`/`changed` record to
// `approved` — exactly the rug-pull bypass Spec 032 was designed to prevent.
func (r *Runtime) setToolEnabledNoEmit(serverName, toolName string, enabled bool, updatedBy string) (bool, error) {
	if r.storageManager == nil {
		return false, nil
	}

	record, err := r.storageManager.GetToolApproval(serverName, toolName)
	// Spec 105 FR-009: a pre-105 record the operator toggles between the
	// upgrade and the server's first discovery may be the collapsed record
	// of a namespaced tool not filed yet; while it still restricts after the
	// write it keeps its legacy consult (saveReadToolApproval). A record
	// synthesized here is this binary's own and is stamped.
	wasUnstamped := false
	switch {
	case err == nil:
		// existing record — keep its Status, just flip Disabled below.
		wasUnstamped = !record.IdentityKeyed
	case errors.Is(err, storage.ErrToolApprovalNotFound):
		// First time we've seen this tool: synthesize the record the
		// discovery producer would have filed for it under this server's
		// gate (see the function comment).
		record = r.newToggleSynthesizedRecord(serverName, toolName, updatedBy)
	default:
		// Real read error — refuse to write. See comment above for rationale.
		return false, fmt.Errorf("read tool approval %s:%s: %w", serverName, toolName, err)
	}

	if record.Disabled == !enabled {
		// Already in the desired state — no write, no audit event, no SSE.
		// Matches the prior bulk-path pre-check; lifted here so single-toggle
		// also avoids no-op BBolt writes.
		return false, nil
	}

	record.Disabled = !enabled

	if err := r.saveReadToolApproval(record, wasUnstamped); err != nil {
		return false, err
	}

	action := "tool_enabled"
	if !enabled {
		action = "tool_disabled"
	}

	r.emitToolQuarantineEvent(serverName, toolName, action,
		record.ApprovedHash, record.CurrentHash,
		"", record.CurrentDescription,
		"", record.CurrentSchema)

	return true, nil
}

// newToggleSynthesizedRecord builds the record setToolEnabledNoEmit files for
// a record-less tool (see its function comment): pending under an active
// tool-level quarantine gate, approved and baselined to the snapshot's current
// contract under a lifted one. The caller sets Disabled.
func (r *Runtime) newToggleSynthesizedRecord(serverName, toolName, updatedBy string) *storage.ToolApprovalRecord {
	record := &storage.ToolApprovalRecord{
		ServerName: serverName,
		ToolName:   toolName,
	}
	// The snapshot's current contract, when the StateView lists the raw name.
	if tool := r.snapshotToolMetadata(serverName, toolName); tool != nil {
		schemaJSON := tool.ParamsJSON
		if schemaJSON == "" {
			schemaJSON = "{}"
		}
		schemaJSON = normalizeJSON(schemaJSON)
		outputSchemaJSON := normalizeJSON(tool.OutputSchemaJSON)
		record.CurrentHash = calculateToolApprovalHashWithOutputSchema(toolName, tool.Description, schemaJSON, outputSchemaJSON, tool.Annotations)
		record.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
		record.CurrentDescription = tool.Description
		record.CurrentSchema = schemaJSON
		record.CurrentOutputSchema = outputSchemaJSON
	}

	if r.resolveToolQuarantineGate(serverName).active() {
		record.Status = storage.ToolApprovalStatusPending
		r.logger.Info("Tool toggled before any approval record existed; filed pending under an active quarantine gate until approved by name",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.String("updated_by", updatedBy))
		return record
	}
	record.Status = storage.ToolApprovalStatusApproved
	record.ApprovedHash = record.CurrentHash
	record.ApprovedAt = time.Now().UTC()
	record.ApprovedBy = updatedBy
	return record
}

// snapshotToolMetadata returns the StateView's current contract for a raw
// tool name on a server — the same fields the discovery producer hashes —
// or nil when the snapshot does not list the exact name. The StateView holds
// raw names verbatim (Spec 105 FR-009), so only the exact name matches.
func (r *Runtime) snapshotToolMetadata(serverName, toolName string) *config.ToolMetadata {
	if r.supervisor == nil {
		return nil
	}
	snapshot := r.supervisor.StateView().Snapshot()
	if snapshot == nil {
		return nil
	}
	status, ok := snapshot.Servers[serverName]
	if !ok || status == nil {
		return nil
	}
	for i := range status.Tools {
		info := status.Tools[i]
		if info.Name != toolName {
			continue
		}
		paramsJSON := ""
		if info.InputSchema != nil {
			if raw, err := json.Marshal(info.InputSchema); err == nil {
				paramsJSON = string(raw)
			}
		}
		return &config.ToolMetadata{
			ServerName:       serverName,
			Name:             info.Name,
			RawName:          info.Name,
			Description:      info.Description,
			ParamsJSON:       paramsJSON,
			OutputSchemaJSON: info.OutputSchemaJSON,
			Annotations:      info.Annotations,
		}
	}
	return nil
}

// SetToolEnabled sets whether a tool is enabled for exposure to MCP clients.
// Thin wrapper over setToolEnabledNoEmit that adds the per-toggle SSE
// servers.changed emit so single-tool consumers (CLI, REST) see the state
// transition without polling. The emit is skipped when the call was a
// no-op (already in the desired state) to avoid wasted SSE traffic.
func (r *Runtime) SetToolEnabled(serverName, toolName string, enabled bool, updatedBy string) error {
	changed, err := r.setToolEnabledNoEmit(serverName, toolName, enabled, updatedBy)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	action := "tool_enabled"
	if !enabled {
		action = "tool_disabled"
	}

	r.emitServersChanged(action, map[string]any{
		"server":     serverName,
		"tool":       toolName,
		"enabled":    enabled,
		"updated_by": updatedBy,
	})

	// Reconcile the index with the new visibility state (issue #873): a disabled
	// tool is removed, a re-enabled tool re-indexed.
	go r.reindexServerToolsAfterApprovalChange(serverName)

	return nil
}

// SetAllToolsEnabled bulk-toggles every tool currently known for a server to
// the given enabled state. Returns the count of tools whose state was changed
// (i.e. excluding tools already in the desired state).
//
// Tool inventory comes from the StateView when available (so it covers tools
// the user has seen even if not yet indexed) and falls back to the search
// index. Tools without an approval record get one synthesized — see
// setToolEnabledNoEmit for the rationale.
//
// SSE emission: the loop calls setToolEnabledNoEmit (no per-tool
// servers.changed). A single trailing emitServersChanged fires after the
// loop when at least one tool actually flipped, mirroring the
// ApproveAllTools pattern. With the lazy-build coalescer (Spec 047 §B2 +
// PR #463) the bulk operation pays exactly one payload build no matter how
// many tools changed.
func (r *Runtime) SetAllToolsEnabled(serverName string, enabled bool, updatedBy string) (int, error) {
	if r.storageManager == nil {
		return 0, nil
	}
	if serverName == "" {
		return 0, fmt.Errorf("server name required")
	}

	toolNames, err := r.collectKnownToolNames(serverName)
	if err != nil {
		return 0, err
	}
	if len(toolNames) == 0 {
		return 0, nil
	}

	changed := 0
	for _, toolName := range toolNames {
		// Never enable a tool the config denies — user-owned Disabled flag is
		// irrelevant here; enforcement is in isToolCallable, but we avoid a
		// misleading record.Disabled=false for a hard-off tool.
		if enabled && r.IsToolConfigDenied(serverName, toolName) {
			continue
		}
		flipped, setErr := r.setToolEnabledNoEmit(serverName, toolName, enabled, updatedBy)
		if setErr != nil {
			r.logger.Warn("Failed to toggle tool in bulk operation",
				zap.String("server", serverName),
				zap.String("tool", toolName),
				zap.Bool("enabled", enabled),
				zap.Error(setErr))
			continue
		}
		if flipped {
			changed++
		}
	}

	if changed > 0 {
		action := "tools_enabled"
		if !enabled {
			action = "tools_disabled"
		}
		r.emitServersChanged(action, map[string]any{
			"server":     serverName,
			"enabled":    enabled,
			"changed":    changed,
			"updated_by": updatedBy,
		})
		// Reconcile the index with the bulk visibility change (issue #873).
		go r.reindexServerToolsAfterApprovalChange(serverName)
	}

	return changed, nil
}

// collectKnownToolNames returns the set of RAW tool names currently known for
// a server — the exact names approval records are keyed by (Spec 105
// FR-009), colons included: a raw "ns:erase" is "ns:erase", never "erase".
// Prefers the StateView snapshot (covers in-memory tools; it holds raw names
// verbatim), falling back to the search index (canonical "server:raw" ids,
// read through their RawName), and finally to whatever approval records
// already exist for the server. Stripping everything before the first colon
// here would collapse a namespaced tool onto its sibling's key and leave the
// namespaced tool itself untoggled by disable_all / block_all.
func (r *Runtime) collectKnownToolNames(serverName string) ([]string, error) {
	seen := make(map[string]struct{})
	add := func(name string) {
		if name == "" {
			return
		}
		seen[name] = struct{}{}
	}

	if r.supervisor != nil {
		snapshot := r.supervisor.StateView().Snapshot()
		if status, ok := snapshot.Servers[serverName]; ok {
			for _, tool := range status.Tools {
				add(tool.Name)
			}
		}
	}

	if r.indexManager != nil {
		if tools, err := r.indexManager.GetToolsByServer(serverName); err == nil {
			for _, tool := range tools {
				add(config.RawToolName(tool))
			}
		}
	}

	if records, err := r.storageManager.ListToolApprovals(serverName); err == nil {
		for _, record := range records {
			add(record.ToolName)
		}
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out, nil
}

// approveBaselineToolsForServer promotes a server's pending tool-approval
// records to approved as baseline trust when the server itself is
// approved/unquarantined.
//
// Trust model (Spec 032, MCP-2081/MCP-2100, request_confirmation 7cfce731):
// approving/unquarantining a server == trusting its CURRENT tool snapshot. So
// pending (newly-discovered, never-reviewed) tools inherit that baseline trust
// automatically. Tool-level quarantine is then reserved for status=changed
// (rug-pull) records only.
//
// CRITICAL: this promotes status=pending ONLY. status=changed records are left
// untouched so that re-approving a server later never silently clears a genuine
// rug-pull flag (preserves Spec 032's rug-pull guarantee). This is precisely why
// it does NOT reuse ApproveAllTools, which promotes both pending AND changed.
func (r *Runtime) approveBaselineToolsForServer(serverName string) error {
	if r.storageManager == nil {
		return nil
	}

	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		return err
	}

	var pendingTools []string
	for _, record := range records {
		if record.Status == storage.ToolApprovalStatusPending {
			pendingTools = append(pendingTools, record.ToolName)
		}
	}

	if len(pendingTools) == 0 {
		return nil
	}

	// ApproveTools sets ApprovedHash=CurrentHash, runs enforceInvariant
	// (pending→approved is permitted), and emits activity + a single SSE event.
	if err := r.ApproveTools(serverName, pendingTools, "system:server-approval-baseline"); err != nil {
		return err
	}

	r.logger.Info("Baseline-approved pending tools on server approval",
		zap.String("server", serverName),
		zap.Int("count", len(pendingTools)))

	return nil
}

// ApproveAllTools approves all pending/changed tools for a server.
func (r *Runtime) ApproveAllTools(serverName string, approvedBy string) (int, error) {
	if r.storageManager == nil {
		return 0, nil
	}

	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		return 0, err
	}

	var toolNames []string
	for _, record := range records {
		if record.Status == storage.ToolApprovalStatusPending || record.Status == storage.ToolApprovalStatusChanged {
			toolNames = append(toolNames, record.ToolName)
		}
	}

	if len(toolNames) == 0 {
		return 0, nil
	}

	if err := r.ApproveTools(serverName, toolNames, approvedBy); err != nil {
		return 0, err
	}

	return len(toolNames), nil
}

// BlockTools atomically "blocks" the named tools for a server. A block is an
// approve+disable performed as a single, all-or-nothing record write per tool:
// the tool's quarantine status is promoted to approved (clearing any pending /
// changed flag, exactly like ApproveTools) AND its Disabled flag is set so it is
// hidden from MCP clients. Because both mutations land in one SaveToolApproval,
// a tool is never observably left in the approved+enabled state — that is the
// invariant this endpoint exists to guarantee.
//
// Why a single combined write instead of ApproveTools followed by
// SetToolEnabled(disabled): two sequential saves leave a window where a crash or
// I/O error after the approve but before the disable yields an approved+enabled
// tool — the exact state callers want to avoid. Folding both into one write
// removes that window.
//
// Config-denied tools (enabled_tools / disabled_tools) need no special handling:
// block only ever disables, so it can never enable a tool the operator forbids.
//
// Returns the number of tools actually blocked. Missing approval records are
// skipped with a warning (mirrors ApproveTools), not treated as a hard error.
func (r *Runtime) BlockTools(serverName string, toolNames []string, blockedBy string) (int, error) {
	if r.storageManager == nil {
		return 0, nil
	}

	blocked := 0
	for _, toolName := range toolNames {
		record, err := r.storageManager.GetToolApproval(serverName, toolName)
		if err != nil {
			r.logger.Warn("Tool approval record not found for block",
				zap.String("server", serverName),
				zap.String("tool", toolName),
				zap.Error(err))
			continue
		}

		// The approve half of a block is a user action, so the pending/changed
		// → approved transition is permitted by the quarantine invariant.
		if err := r.enforceInvariant(serverName, toolName, record.Status, storage.ToolApprovalStatusApproved, ReasonUserApprove); err != nil {
			return blocked, err
		}

		// Spec 105 FR-009: a block on a pre-105 record keeps its legacy
		// consult — the blocked record always restricts (saveReadToolApproval).
		wasUnstamped := !record.IdentityKeyed

		// Approve + disable in a single write — all-or-nothing.
		record.Status = storage.ToolApprovalStatusApproved
		record.ApprovedHash = record.CurrentHash
		record.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
		record.ApprovedAt = time.Now().UTC()
		record.ApprovedBy = blockedBy
		record.PreviousDescription = ""
		record.PreviousSchema = ""
		record.PreviousOutputSchema = ""
		record.ClearScanHold()
		record.Disabled = true

		if err := r.saveReadToolApproval(record, wasUnstamped); err != nil {
			return blocked, err
		}
		blocked++

		r.logger.Info("Tool blocked (approved + disabled)",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.String("blocked_by", blockedBy))

		// Single per-tool audit event describing the block as one action.
		r.emitToolQuarantineEvent(serverName, toolName, "tool_blocked",
			"", record.ApprovedHash, "", record.CurrentDescription, "", record.CurrentSchema)
	}

	// One SSE emit per call (not per tool) so an open Servers/overview page
	// refreshes its quarantine badge — mirrors ApproveTools.
	if blocked > 0 {
		r.emitServersChanged("tools_blocked", map[string]any{
			"server":        serverName,
			"blocked_count": blocked,
			"blocked_by":    blockedBy,
		})
		// Evict the now-disabled tools from the index promptly (issue #873).
		// Covers BlockAllTools, which delegates here.
		go r.reindexServerToolsAfterApprovalChange(serverName)
	}

	return blocked, nil
}

// BlockAllTools blocks (approve+disable) every pending/changed tool for a
// server. Mirrors ApproveAllTools' selection set so the two bulk operations
// dismiss the same quarantine queue — approve keeps tools visible, block hides
// them. Returns the number of tools blocked.
func (r *Runtime) BlockAllTools(serverName string, blockedBy string) (int, error) {
	if r.storageManager == nil {
		return 0, nil
	}

	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		return 0, err
	}

	var toolNames []string
	for _, record := range records {
		if record.Status == storage.ToolApprovalStatusPending || record.Status == storage.ToolApprovalStatusChanged {
			toolNames = append(toolNames, record.ToolName)
		}
	}

	if len(toolNames) == 0 {
		return 0, nil
	}

	return r.BlockTools(serverName, toolNames, blockedBy)
}

// emitToolQuarantineEvent emits an activity event for tool quarantine changes.
func (r *Runtime) emitToolQuarantineEvent(serverName, toolName, action, oldHash, newHash, oldDesc, newDesc, oldSchema, newSchema string) {
	metadata := map[string]interface{}{
		"action":    action,
		"tool_name": toolName,
	}
	if oldHash != "" {
		metadata["old_hash"] = oldHash
	}
	if newHash != "" {
		metadata["new_hash"] = newHash
	}
	// Truncate descriptions at 64KB for storage
	const maxDescLen = 64 * 1024
	if oldDesc != "" {
		if len(oldDesc) > maxDescLen {
			oldDesc = oldDesc[:maxDescLen]
		}
		metadata["old_description"] = oldDesc
	}
	if newDesc != "" {
		if len(newDesc) > maxDescLen {
			newDesc = newDesc[:maxDescLen]
		}
		metadata["new_description"] = newDesc
	}
	if oldSchema != "" {
		if len(oldSchema) > maxDescLen {
			oldSchema = oldSchema[:maxDescLen]
		}
		metadata["old_schema"] = oldSchema
	}
	if newSchema != "" {
		if len(newSchema) > maxDescLen {
			newSchema = newSchema[:maxDescLen]
		}
		metadata["new_schema"] = newSchema
	}

	// Marshal metadata to JSON string for the event payload
	metadataJSON, _ := json.Marshal(metadata)

	payload := map[string]any{
		"server_name": serverName,
		"tool_name":   toolName,
		"action":      action,
		"metadata":    string(metadataJSON),
	}
	r.publishEvent(newEvent(EventTypeActivityToolQuarantineChange, payload))
}

// IsToolConfigDenied reports whether toolName is denied by the server's static
// enabled_tools / disabled_tools config. Evaluated at call time — nothing is
// written to BBolt. Returns false (allow) when the server is unknown or has no
// filter configured.
func (r *Runtime) IsToolConfigDenied(serverName, toolName string) bool {
	for _, sc := range r.Config().Servers {
		if sc.Name == serverName {
			return !sc.IsToolAllowedByConfig(toolName)
		}
	}
	return false
}

// ClassifyDisabledTool returns the single machine-branchable reason a tool is
// not callable, by fixed first-match precedence (Spec 049). Pure, request-time,
// read-only — nothing is written to BBolt. Only meaningful for tools that are
// already known non-callable; it never lies (indeterminate → unknown).
func (r *Runtime) ClassifyDisabledTool(serverName, toolName string) contracts.DisabledToolStatus {
	// Resolve the server config. Unknown server → unknown (never a misleading
	// remediation for a server we cannot reason about).
	var sc *config.ServerConfig
	for _, candidate := range r.Config().Servers {
		if candidate.Name == serverName {
			sc = candidate
			break
		}
	}
	if sc == nil {
		return contracts.DisabledStatusUnknown
	}

	// 1. Whole server off.
	if !sc.Enabled {
		return contracts.DisabledStatusServerDisabled
	}

	// 2. Operator config policy — outranks user/pending; the user cannot lift
	//    this from the UI.
	if !sc.IsToolAllowedByConfig(toolName) {
		return contracts.DisabledStatusByConfig
	}

	// 3/4. User-disabled vs pending security approval, from the approval record.
	record, err := r.GetToolApproval(serverName, toolName)
	switch {
	case err == nil && record != nil:
		if record.Disabled {
			return contracts.DisabledStatusByUser
		}
		if record.Status == storage.ToolApprovalStatusPending ||
			record.Status == storage.ToolApprovalStatusChanged {
			return contracts.DisabledStatusPendingApproval
		}
	case errors.Is(err, storage.ErrToolApprovalNotFound):
		// No record — fall through to unknown below.
	}

	// 5. Indeterminate (storage error, or no concrete reason found) — never
	//    emit a wrong remediation.
	return contracts.DisabledStatusUnknown
}
