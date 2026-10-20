package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/hash"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// calculateToolApprovalHash computes a stable SHA-256 hash for tool-level quarantine.
// Uses toolName + description + input schema JSON + output schema JSON + the
// safety-relevant annotation hints (RT-03, see safetyHintsFingerprint).
//
// Only the four behavioral hints are folded in — never the free-form title or
// the raw annotations JSON, which caused false "tool_description_changed" spam
// on reconnects when the whole annotations object was hashed. The hints are
// compared by their EFFECTIVE value (an omitted hint means its MCP default), so
// a server that omits annotations and one that spells out the defaults hash
// identically. A flip such as destructive → readOnly-lying changes the hash
// and lands on the rug-pull gate like any contract change. Every field is
// length-prefixed (issue #1606, storage.UnambiguousHashSchemaVersion); the
// older pipe-joined formula is calculateToolApprovalHashV4.
func calculateToolApprovalHash(toolName, description, schemaJSON string, annotations *config.ToolAnnotations) string {
	return calculateToolApprovalHashWithOutputSchema(toolName, description, schemaJSON, "", annotations)
}

func calculateToolApprovalHashWithOutputSchema(toolName, description, schemaJSON, outputSchemaJSON string, annotations *config.ToolAnnotations) string {
	// HashSchemaVersion 5 (storage.UnambiguousHashSchemaVersion, issue #1606):
	// every field is length-prefixed and an absent output schema has its own
	// marker, so no two different contracts share an encoding. Schemas are
	// JSON-normalized so a key-order-only difference is not a change; the
	// safety hints are their effective fingerprint (RT-03), "" for the MCP
	// defaults.
	h := sha256.New()
	writeApprovalHashField(h, toolApprovalHashTag)
	writeApprovalHashField(h, toolName)
	writeApprovalHashField(h, description)
	writeApprovalHashField(h, normalizeJSON(schemaJSON))
	if normalized := normalizeJSON(outputSchemaJSON); normalized != "" {
		writeApprovalHashField(h, "output:present")
		writeApprovalHashField(h, normalized)
	} else {
		writeApprovalHashField(h, "output:absent")
	}
	writeApprovalHashField(h, safetyHintsFingerprint(annotations))
	return hex.EncodeToString(h.Sum(nil))
}

// toolApprovalHashTag domain-separates the v5 approval hash from every older
// formula and from the review fingerprint.
const toolApprovalHashTag = "mcpproxy-tool-approval-v5"

// writeApprovalHashField writes one length-prefixed field (8-byte big-endian
// length, then the bytes).
func writeApprovalHashField(w io.Writer, field string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(field)))
	_, _ = w.Write(n[:])
	_, _ = w.Write([]byte(field))
}

// calculateToolApprovalHashV4 is the approval hash formula of
// HashSchemaVersion 4 (storage.AnnotationHashSchemaVersion) and below, kept
// byte-identical so the v4 → v5 migration can check that a stored ApprovedHash
// covers its stored contract. It is ambiguous (issue #1606): the fields are
// joined with an unescaped '|' and an absent output schema is left out, so it
// must never be the hash a record is approved under.
func calculateToolApprovalHashV4(toolName, description, schemaJSON, outputSchemaJSON string, annotations *config.ToolAnnotations) string {
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
	// Safety hints are folded in only when they differ from the MCP defaults,
	// so tools with default hints keep the pre-RT-03 hash (no migration).
	if hints := safetyHintsFingerprint(annotations); hints != "" {
		h.Write([]byte("|hints:"))
		h.Write([]byte(hints))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// safetyHintsFingerprint renders the EFFECTIVE values of the safety-relevant
// MCP tool annotation hints, or "" when they are all at their defaults
// (readOnlyHint=false, destructiveHint=true, idempotentHint=false,
// openWorldHint=true — the most conservative reading). These hints steer
// read/write routing (call_tool_read vs call_tool_write) and review tiers, so a
// flip is a contract change the approval gate must see (RT-03).
func safetyHintsFingerprint(annotations *config.ToolAnnotations) string {
	readOnly, destructive, idempotent, openWorld := false, true, false, true
	if annotations != nil {
		if annotations.ReadOnlyHint != nil {
			readOnly = *annotations.ReadOnlyHint
		}
		if annotations.DestructiveHint != nil {
			destructive = *annotations.DestructiveHint
		}
		if annotations.IdempotentHint != nil {
			idempotent = *annotations.IdempotentHint
		}
		if annotations.OpenWorldHint != nil {
			openWorld = *annotations.OpenWorldHint
		}
	}
	if !readOnly && destructive && !idempotent && openWorld {
		return ""
	}
	return fmt.Sprintf("readOnly=%t,destructive=%t,idempotent=%t,openWorld=%t", readOnly, destructive, idempotent, openWorld)
}

// storedSafetyHintsMatch reports whether a record's stored annotation snapshot
// carries the same effective safety hints as the live tool. A record written
// before the hints were part of the hash (HashSchemaVersion below
// storage.AnnotationHashSchemaVersion) whose snapshot was never captured (nil)
// has UNKNOWN hints; it is accepted so an upgrade does not force a mass
// re-review — the first pass under this binary then baselines the live hints.
func storedSafetyHintsMatch(stored *config.ToolAnnotations, hashSchemaVersion uint64, live *config.ToolAnnotations) bool {
	if stored == nil && hashSchemaVersion < storage.AnnotationHashSchemaVersion {
		return true
	}
	return safetyHintsFingerprint(stored) == safetyHintsFingerprint(live)
}

// legacyHintsKnownToDiffer is true for a record whose approval hash predates
// the annotation hints (HashSchemaVersion below
// storage.AnnotationHashSchemaVersion) when its stored snapshot shows
// effective safety hints different from the live ones. Such a record's hash
// equality proves nothing about the hints — a flip from non-default hints to
// the defaults re-hashes to the old, hint-free value — so every legacy hash
// shortcut (hash match, formula migration, output-schema backfill, sibling
// adoption) must consult this first (Sol r1 P1).
func legacyHintsKnownToDiffer(stored *config.ToolAnnotations, hashSchemaVersion uint64, live *config.ToolAnnotations) bool {
	return hashSchemaVersion < storage.AnnotationHashSchemaVersion && !storedSafetyHintsMatch(stored, hashSchemaVersion, live)
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
	// ReasonScanApproved marks a NEW tool auto-approved because the server runs
	// in trust_mode: scan AND a synchronous, offline TPA scan of the tool
	// returned a green (clean) verdict (spec 086 stage 2). It never clears a
	// changed (rug-pull) record (RT-02): a clean verdict is not proof a changed
	// contract is safe, so changed→approved needs an operator.
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
			ReasonAutoApproveChanges:
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

// baselineScanResult is the per-tool outcome of the first-seen (baseline)
// toolset scan (R2-1, round-2 red team). It is computed ONCE per baseline pass,
// before the record loop, from a single engine run over the whole toolset.
type baselineScanResult struct {
	// held maps a tool name (config.ToolMetadata.Name, as scanned) to the hold
	// evidence that keeps it pending. A tool absent from the map is clean.
	held map[string]*scanHoldEvidence
	// coverageOK mirrors scanner.ScanToolMetadataVerdict's coverage flag. When
	// false EVERY tool of the pass is held (fail closed, FR-014), with
	// ToolHeldReasonScanCoverage for tools that carried no finding of their own.
	coverageOK bool
	verdict    string
}

// holdFor returns the evidence that holds tool (nil when the tool may be
// auto-baselined). A nil result (no scan ran) holds nothing — callers only
// consult it on a baseline pass, where scanBaselineToolset always runs.
func (b *baselineScanResult) holdFor(tool string) *scanHoldEvidence {
	if b == nil {
		return nil
	}
	if e, ok := b.held[tool]; ok {
		return e
	}
	if !b.coverageOK {
		return &scanHoldEvidence{Reason: storage.ToolHeldReasonScanCoverage, Verdict: b.verdict}
	}
	return nil
}

// scanBaselineToolset runs the same synchronous, offline TPA scan the
// trust_mode: scan change gate uses (scanChangeIsClean) over a trusted server's
// FIRST-SEEN toolset, so the MCP-2931 baseline auto-approval never admits a
// poisoned contract unscanned (round-2 red team, R2-1: an unquarantined
// first-seen server auto-baselined an 18-vector poisoned catalog and the
// poisoned tools were callable; only an operator-triggered scan afterwards
// said "dangerous").
//
// The whole toolset is scanned in ONE engine run, with every OTHER connected
// server's tools as peers, so the cross-server shadowing/impersonation checks
// see the same context the async full-server scan gets, and sibling tools of
// the same server see each other (a clone of a peer's tool is a finding on the
// clone). The verdict is then read PER TOOL from the findings' locations: a
// tool with no finding of its own is clean and auto-baselines exactly as
// before (no new friction for a clean server), a tool with a hard finding
// ("dangerous") or a soft one ("warnings") lands pending with the findings
// recorded as hold evidence (FR-018), and a degraded or bundle-less scan holds
// every tool (fail closed, FR-014) — the same green-only rule as the change
// gate. Pure in-process string work: no Docker, network or filesystem I/O,
// so it is safe under the server's approval lock (the caller holds it; see
// checkToolApprovals).
func (r *Runtime) scanBaselineToolset(serverName string, tools []*config.ToolMetadata) *baselineScanResult {
	peers := r.collectPeerToolMetadata(serverName)
	verdict, findings, coverageOK := scanner.ScanToolMetadataVerdict(serverName, tools, peers)
	res := &baselineScanResult{held: make(map[string]*scanHoldEvidence), coverageOK: coverageOK, verdict: verdict}
	prefix := serverName + ":"
	for _, f := range findings {
		if f.Tier != scanner.TierHard && f.Tier != scanner.TierSoft {
			continue // only tiered (baseline) findings gate approval (FR-021)
		}
		name := strings.TrimPrefix(f.Location, prefix)
		e := res.held[name]
		if e == nil {
			e = &scanHoldEvidence{Reason: storage.ToolHeldReasonScanFindings, Verdict: "warnings"}
			res.held[name] = e
		}
		if f.Tier == scanner.TierHard {
			e.Verdict = "dangerous"
		}
		e.Signals = append(e.Signals, f.Signals...)
	}
	if coverageOK && len(res.held) == 0 {
		return res
	}
	heldNames := make([]string, 0, len(res.held))
	for name := range res.held {
		heldNames = append(heldNames, name)
	}
	sort.Strings(heldNames)
	r.logger.Info("baseline scan held first-seen tools for review (non-green verdict)",
		zap.String("server", serverName),
		zap.String("verdict", verdict),
		zap.Bool("coverage_ok", coverageOK),
		zap.Int("tools", len(tools)),
		zap.Strings("held_tools", heldNames))
	return res
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

// refreshHeldContractSnapshot records a further change to an already-held
// (changed) contract: the Current* snapshot is replaced with the live
// contract, stale scan-hold evidence of the superseded contract is dropped,
// and a tool_description_changed event names the move. ApprovedHash,
// HashSchemaVersion and the Previous* (approved-contract) evidence are kept,
// so the record stays changed until an operator confirms or the server
// reverts. When the live contract is the recorded one the snapshot is only
// re-stamped: a legacy held record whose hint-free CurrentHash equals the
// hint-aware hash of the live contract (default hints) gets the current
// CurrentHashSchemaVersion and the live annotation snapshot, so an operator
// approval copies a versioned hash instead of keeping the legacy
// unknown-hints wildcard a later annotation flip could match (Sol r5 P1).
func (r *Runtime) refreshHeldContractSnapshot(serverName, toolName string, existing *storage.ToolApprovalRecord, wasUnstamped bool, tool *config.ToolMetadata, schemaJSON, outputSchemaJSON, currentHash string) {
	if existing == nil {
		return
	}
	// The live contract is the recorded one only when the hash AND each
	// definition field match (Spec 117 PR-2 Sol r2): a CurrentHash a v4
	// binary computed joins the fields with an unescaped '|', so two
	// different contracts can share it (issue #1606), and the review
	// fingerprint must still follow the live one.
	sameDefinition := existing.CurrentHash == currentHash &&
		existing.CurrentDescription == tool.Description &&
		normalizeJSON(existing.CurrentSchema) == normalizeJSON(schemaJSON) &&
		normalizeJSON(existing.CurrentOutputSchema) == normalizeJSON(outputSchemaJSON)
	if sameDefinition {
		captured := capturedToolAnnotations(tool.Annotations)
		// RAW hints (Sol r2 P2, FR-017): an unset hint and its spelled-out
		// default hash alike, but the review fingerprint tells them apart, so
		// the recorded snapshot must follow the raw value.
		if existing.CurrentHashSchemaVersion >= storage.ToolApprovalHashSchemaVersion &&
			existing.CurrentAnnotations != nil &&
			reviewHintString(existing.CurrentAnnotations) == reviewHintString(captured) {
			return
		}
		existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
		existing.CurrentAnnotations = captured
		if r.storageManager == nil {
			return
		}
		if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
			r.logger.Error("Failed to stamp the held contract snapshot",
				zap.String("server", serverName), zap.String("tool", toolName), zap.Error(saveErr))
		}
		return
	}
	contentMoved := existing.CurrentDescription != tool.Description ||
		existing.CurrentSchema != schemaJSON ||
		existing.CurrentOutputSchema != outputSchemaJSON ||
		!reflect.DeepEqual(existing.CurrentAnnotations, tool.Annotations)
	oldHash, oldDesc, oldSchema := existing.CurrentHash, existing.CurrentDescription, existing.CurrentSchema
	existing.CurrentHash = currentHash
	existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
	existing.CurrentDescription = tool.Description
	existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
	existing.CurrentSchema = schemaJSON
	existing.CurrentOutputSchema = outputSchemaJSON
	if contentMoved {
		existing.ClearScanHold()
	}
	if r.storageManager == nil {
		return
	}
	if saveErr := r.saveReadToolApproval(existing, wasUnstamped); saveErr != nil {
		r.logger.Error("Failed to refresh the held contract snapshot",
			zap.String("server", serverName), zap.String("tool", toolName), zap.Error(saveErr))
		return
	}
	if !contentMoved {
		return
	}
	r.logger.Warn("Held tool contract changed again while awaiting confirmation",
		zap.String("server", serverName),
		zap.String("tool", toolName),
		zap.String("approved_hash", existing.ApprovedHash),
		zap.String("previous_current_hash", oldHash),
		zap.String("current_hash", currentHash))
	r.emitToolQuarantineEvent(serverName, toolName, "tool_description_changed",
		oldHash, currentHash, oldDesc, tool.Description, oldSchema, schemaJSON)
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
	// scanMode: trust_mode scan — a NEW tool auto-approves ONLY on a green
	// in-process TPA verdict, else it is held (fail closed). A CHANGED
	// contract is always held for operator confirmation, whatever the
	// verdict (RT-02); the scan only contributes hold evidence.
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

// resolveToolQuarantineGate resolves the gate for a server (trust mode:
// manual → every change/addition held; auto → all auto-approved; scan →
// additions green-scan-gated, changes held).
//
// Spec 116 D16: the policy is read from STORAGE — the server's row and
// meta.quarantine_enabled, which the config commit writes in its storage step
// under the server's approval lock — not from the published config. The commit
// releases the approval lock before it publishes, so a decision made in that
// window from the published config would use the old trust mode (review
// R3.2). Callers hold the server's approval lock. A database no commit has
// written yet (some unit-test fixtures) falls back to the published config.
func (r *Runtime) resolveToolQuarantineGate(serverName string) toolQuarantineGate {
	cfg := r.Config()
	globalEnabled := cfg.IsQuarantineEnabled()
	var entry *config.ServerConfig
	for _, sc := range cfg.Servers {
		if sc.Name == serverName {
			entry = sc
			break
		}
	}
	if r.storageManager != nil {
		if committed, err := r.storageManager.ConfigCommitted(); err == nil && committed {
			if enabled, ok, qerr := r.storageManager.StoredQuarantineEnabled(); qerr == nil && ok {
				globalEnabled = enabled
			}
			if row, rerr := r.storageManager.GetStoredServer(serverName); rerr == nil {
				entry = row.Config
			}
		}
	}
	gate := toolQuarantineGate{globalEnabled: globalEnabled}
	if entry != nil {
		mode := entry.EffectiveTrustMode()
		gate.serverSkipped = entry.IsQuarantineSkipped()
		gate.serverQuarantined = entry.Quarantined
		gate.autoApproveChanges = mode == config.TrustModeAuto
		gate.scanMode = mode == config.TrustModeScan
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

	// UX-02: the whole pass — the gate read, the prior-record snapshot, the
	// isBaselinePass decision and every write — runs under the server's
	// tool-approval lock, so overlapping discovery passes and operator
	// approvals apply one after another instead of overwriting each other.
	defer r.lockToolApprovals(serverName)()
	return r.checkToolApprovalsHeld(serverName, tools)
}

// checkToolApprovalsHeld is checkToolApprovals for a caller that already holds
// serverName's approval lock (Spec 116 D15: the discovery pass makes its final
// eligibility check, the approval decisions and its index writes in one hold).
func (r *Runtime) checkToolApprovalsHeld(serverName string, tools []*config.ToolMetadata) (*ToolApprovalResult, error) {
	return r.checkToolApprovalsResolved(serverName, tools, nil)
}

// checkToolApprovalsResolved is checkToolApprovalsHeld with the server's
// baseline already resolved under the same lock hold (resolveToolBaselineHeld),
// so a caller that must abort BEFORE publishing anything can run the baseline
// step first (Spec 117 FR-027a). A nil baseline is resolved here.
func (r *Runtime) checkToolApprovalsResolved(serverName string, tools []*config.ToolMetadata, baseline *toolBaseline) (*ToolApprovalResult, error) {
	if r.storageManager == nil {
		return &ToolApprovalResult{BlockedTools: make(map[string]bool)}, nil
	}
	r.beginApprovalPassHeld(serverName)

	// Determine if quarantine is enforced for this server. Storage is
	// consulted too (fail closed): QuarantineServer flips it under this lock
	// before the runtime config catches up, and a just-quarantined server must
	// never be auto-baselined.
	gate := r.resolveToolQuarantineGate(serverName)
	if !gate.serverQuarantined && r.serverQuarantinedInStorage(serverName) {
		gate.serverQuarantined = true
		gate.enforceQuarantine = gate.enforceNewTools
	}
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
	//
	// Spec 117 PR-1 (#1567): the decision is no longer inferred from the
	// records alone. A durable per-server marker records that the baseline
	// was decided, so a replaced inventory or an authoritative empty refresh
	// (which delete the approved records) never makes the server "new" again.
	// The marker step runs before the first record write of this pass and
	// fails closed (resolveToolBaselineHeld).
	if baseline == nil {
		resolved, err := r.resolveToolBaselineHeld(serverName)
		if err != nil {
			return nil, err
		}
		baseline = resolved
	}
	priorByName := baseline.prior
	serverHasBaseline := baseline.hasBaseline
	isBaselinePass := enforceNewTools && !serverQuarantined && !serverHasBaseline

	// R2-1: the baseline is no longer trusted blind. The first-seen toolset is
	// scanned once (offline, in-process) and only tools with a green verdict
	// auto-baseline; the rest land pending with the scan evidence. Computed
	// before the loop so the decision is one snapshot for the whole pass.
	//
	// trust_mode: auto never reaches this scan: auto is a lifted gate
	// (ServerConfig.IsQuarantineSkipped → !enforceNewTools, so isBaselinePass is
	// false and new tools are approved "auto"), which is the operator's
	// explicit opt-out of scanning and holding. Pinned by
	// TestCheckToolApprovals_AutoTrustMode_IsNotScanGated.
	var baselineScan *baselineScanResult
	if isBaselinePass {
		baselineScan = r.scanBaselineToolset(serverName, tools)
	}

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
		if r.toolApprovalReadHook != nil {
			r.toolApprovalReadHook(serverName, toolName)
		}

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
			// RT-03 (Sol r1 P1): the backfill re-hashes with the LIVE hints, so
			// it may only run when the stored snapshot does not show different
			// hints; a known flip goes through the rug-pull path below.
			hintsMatch := storedSafetyHintsMatch(existing.CurrentAnnotations, existing.HashSchemaVersion, tool.Annotations)
			if descMatch && schemaMatch && hintsMatch {
				backfilledHash := calculateToolApprovalHashWithOutputSchema(toolName, storedDesc, storedSchema, outputSchemaJSON, tool.Annotations)
				existing.ApprovedHash = backfilledHash
				existing.CurrentHash = backfilledHash
				existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.CurrentDescription = storedDesc
				existing.CurrentSchema = normalizeJSON(storedSchema)
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
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
				// Green-only, like the scan-mode change gate below: a first-seen
				// tool the offline scan flagged (or could not cover) is pending
				// with its evidence, never baselined (R2-1).
				if evidence := baselineScan.holdFor(tool.Name); evidence == nil {
					autoApprove, approvedBy = true, "auto-baseline"
				} else {
					holdEvidence = evidence
				}
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
					ServerName:               serverName,
					ToolName:                 toolName,
					CurrentHash:              currentHash,
					CurrentHashSchemaVersion: storage.ToolApprovalHashSchemaVersion,
					ApprovedHash:             currentHash,
					HashSchemaVersion:        storage.ToolApprovalHashSchemaVersion,
					Status:                   storage.ToolApprovalStatusApproved,
					ApprovedBy:               approvedBy,
					ApprovedAt:               now,
					CurrentDescription:       tool.Description,
					CurrentAnnotations:       capturedToolAnnotations(tool.Annotations),
					CurrentSchema:            schemaJSON,
					CurrentOutputSchema:      outputSchemaJSON,
					Disabled:                 legacyDisabled,
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
				ServerName:               serverName,
				ToolName:                 toolName,
				CurrentHash:              currentHash,
				CurrentHashSchemaVersion: storage.ToolApprovalHashSchemaVersion,
				HashSchemaVersion:        storage.ToolApprovalHashSchemaVersion,
				Status:                   storage.ToolApprovalStatusPending,
				CurrentDescription:       tool.Description,
				CurrentAnnotations:       capturedToolAnnotations(tool.Annotations),
				CurrentSchema:            schemaJSON,
				CurrentOutputSchema:      outputSchemaJSON,
				Disabled:                 legacyDisabled,
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

		// Existing record found - check if hash matches. A pre-RT-03 hash
		// carries no hints, so for such a record equality only counts when
		// its stored snapshot does not show different hints (Sol r1 P1).
		// A CHANGED record's stamp describes its current (changed) hash, not
		// its possibly hint-free ApprovedHash, so its hash match must also
		// agree with the approved hints recorded as before-evidence.
		hashMatches := existing.ApprovedHash == currentHash &&
			!legacyHintsKnownToDiffer(existing.CurrentAnnotations, existing.HashSchemaVersion, tool.Annotations) &&
			(existing.Status != storage.ToolApprovalStatusChanged || previousHintsProveRevert(existing.PreviousAnnotations, tool.Annotations))
		if hashMatches {
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
				existing.PreviousAnnotations = nil
				existing.PreviousSchema = ""
				existing.PreviousOutputSchema = ""
				existing.ClearScanHold()
				needsSave = true
				r.logger.Info("Tool restored to approved (hash matches after formula update)",
					zap.String("server", serverName),
					zap.String("tool", toolName))
			}
			// Update current hash/description/schema in case they differ from storage
			if existing.CurrentHash != currentHash || existing.CurrentOutputSchema != outputSchemaJSON || existing.HashSchemaVersion < storage.ToolApprovalHashSchemaVersion {
				existing.CurrentHash = currentHash
				existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.CurrentDescription = tool.Description
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				needsSave = true
			}
			if !reflect.DeepEqual(existing.CurrentAnnotations, tool.Annotations) {
				existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
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
			priorHashMatches := existing.CurrentHash == currentHash ||
				v4PendingHashMatchesLive(existing, toolName, tool, schemaJSON, outputSchemaJSON)

			// Update current info to the live snapshot.
			existing.CurrentHash = currentHash
			existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
			existing.CurrentDescription = tool.Description
			existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
			existing.CurrentSchema = schemaJSON
			existing.CurrentOutputSchema = outputSchemaJSON

			// Decide whether this pending record is promoted to approved:
			//   - baseline migration: trusted server, no prior baseline, hash unchanged (MCP-2931 #4)
			//     AND the baseline scan is green for this tool (R2-1). A first-seen
			//     tool the scan held is a pending record on a server that still
			//     has no baseline (an all-held pass writes no marker, FR-025), so
			//     without this gate the very next identical list would promote it
			//     as a "stranded" pending record and clear the scan evidence —
			//     the EXP-4 TOCTOU lab run found exactly that. The evidence on the
			//     record is refreshed against the LIVE contract below, so it
			//     always describes what the operator is looking at.
			//   - auto_approve_tool_changes: operator opted in (MCP-2931 #3)
			var baselineHold *scanHoldEvidence
			if isBaselinePass {
				baselineHold = baselineScan.holdFor(tool.Name)
			}
			promote := false
			var promoteReason TransitionReason
			var promoteBy string
			switch {
			case isBaselinePass && priorHashMatches && baselineHold == nil:
				promote, promoteReason, promoteBy = true, ReasonBaselineTrust, "auto-baseline"
			case autoApproveChanges:
				promote, promoteReason, promoteBy = true, ReasonAutoApproveChanges, "auto-approve-changes"
			}
			if baselineHold != nil {
				// Record why the LIVE contract is held. Evidence the record
				// already carries from elsewhere (a legacy collapsed lock, a
				// hold from an earlier contract) is left in place when this
				// scan is clean: it is cleared on promotion (ClearScanHold
				// below), never by a pass that does not promote.
				baselineHold.applyTo(existing)
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
				existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.ApprovedAt = time.Now().UTC()
				existing.ApprovedBy = promoteBy
				existing.PreviousDescription = ""
				existing.PreviousAnnotations = nil
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
			if revertedToPreviousContract(existing, tool.Description, schemaJSON, outputSchemaJSON, tool.Annotations, currentHash) {
				if err := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonDescriptionRevert); err != nil {
					result.BlockedTools[toolName] = true
					result.ChangedCount++
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.ApprovedHash = currentHash
				// The restored ApprovedHash is recomputed here, so it carries
				// the current formula version (Sol r4 P1).
				existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.CurrentHash = currentHash
				existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.CurrentDescription = tool.Description
				existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousAnnotations = nil
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
				existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.ApprovedAt = time.Now().UTC()
				existing.ApprovedBy = "auto-approve-changes"
				existing.CurrentDescription = tool.Description
				existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousAnnotations = nil
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
			// The held contract moved on again (A → held B → held C, Sol r3
			// P2): refresh the Current* snapshot so review output and the
			// ingested-contract scan input (RT-06) describe what the server
			// serves now. ApprovedHash and the Previous* evidence of the
			// approved contract stay untouched, so the record stays changed
			// and a revert to the approved contract is still recognised.
			r.refreshHeldContractSnapshot(serverName, toolName, existing, wasUnstamped, tool, schemaJSON, outputSchemaJSON, currentHash)
			// trust_mode: scan (spec 086 stage 2, tightened by RT-02): a
			// still-changed record is never re-baselined by the scan. The scan
			// re-runs only to keep the hold evidence current (FR-018): matched
			// TPA/check ids on a non-green verdict, cleared on a green one.
			if scanMode {
				// RT-02: a clean verdict never re-approves a changed contract;
				// it only refreshes (clears) the hold evidence. The record stays
				// changed until an operator confirms it.
				_, evidence := r.scanChangeIsClean(serverName, tool)
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

		if existing.ApprovedHash != "" && !hashMatches {
			// Before marking as changed, check if this is a hash formula migration.
			// Recompute what the approved hash WOULD be using the STORED description+schema
			// with the CURRENT formula. If it matches the current hash, the tool hasn't
			// actually changed — only the hash formula did.
			//
			// An EMPTY stored field is a wildcard (the live value stands in)
			// only on a record that predates complete contract capture
			// (HashSchemaVersion below storage.OutputSchemaHashSchemaVersion):
			// there it means "never captured". From that version on every
			// writer captures the full Current* snapshot, so an empty stored
			// description or output schema is the DECLARED contract ("none"),
			// and a later addition of one is a contract change that must be
			// held, never migrated or content-matched (Sol r4 P1).
			uncaptured := existing.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion
			storedDesc := existing.CurrentDescription
			storedSchema := existing.CurrentSchema
			storedOutputSchema := existing.CurrentOutputSchema
			if storedDesc == "" && uncaptured {
				storedDesc = tool.Description
			}
			if storedSchema == "" && uncaptured {
				storedSchema = schemaJSON
			}
			if storedOutputSchema == "" && uncaptured {
				storedOutputSchema = outputSchemaJSON
			}
			// The stored annotation snapshot is part of the stored contract
			// (RT-03): a pre-RT-03 record whose hints are unchanged re-hashes to
			// the current hash here and is migrated without review.
			rehashedFromStored := calculateToolApprovalHashWithOutputSchema(toolName, storedDesc, storedSchema, storedOutputSchema, existing.CurrentAnnotations)
			hintsMatch := storedSafetyHintsMatch(existing.CurrentAnnotations, existing.HashSchemaVersion, tool.Annotations)

			// Also check legacy and with-annotations formulas
			legacyHash := calculateLegacyToolApprovalHash(toolName, tool.Description, schemaJSON)
			annotationsHash := calculateHashWithAnnotations(toolName, tool.Description, schemaJSON, tool.Annotations)

			// A v4 record (issue #1606) is re-baselined under the v5 formula
			// only when its pipe-joined ApprovedHash covers the stored
			// contract: the stored contract is then the approved one, and the
			// checks below compare it field by field with the live contract.
			// A v4 hash that does not cover the stored snapshot proves nothing
			// (another contract may collide with it), so every no-review
			// shortcut below is closed and the record is held as changed.
			storedApprovalProven := v4ApprovedHashCoversStored(existing, toolName)

			isFormulaChange := storedApprovalProven && rehashedFromStored == currentHash
			if existing.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion && hintsMatch {
				// The legacy formulas are hashed from the LIVE contract and carry
				// no (or raw, unnormalized) hints: they only prove a formula
				// change when the stored snapshot agrees with the live hints.
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
				existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.CurrentDescription = tool.Description
				existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousAnnotations = nil
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
			descMatch := tool.Description == existing.CurrentDescription || (existing.CurrentDescription == "" && uncaptured)
			var schemaMatch bool
			if (existing.CurrentSchema == "" && uncaptured) || schemaJSON == existing.CurrentSchema {
				schemaMatch = true
			} else {
				// Normalize both schemas and compare
				schemaMatch = normalizeJSON(schemaJSON) == normalizeJSON(existing.CurrentSchema)
			}
			var outputSchemaMatch bool
			if (existing.CurrentOutputSchema == "" && uncaptured) || outputSchemaJSON == existing.CurrentOutputSchema {
				outputSchemaMatch = true
			} else {
				outputSchemaMatch = normalizeJSON(outputSchemaJSON) == normalizeJSON(existing.CurrentOutputSchema)
			}
			if storedApprovalProven && descMatch && schemaMatch && outputSchemaMatch && hintsMatch {
				if err := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonContentMatch); err != nil {
					result.BlockedTools[toolName] = true
					result.ChangedCount++
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.ApprovedHash = currentHash
				existing.CurrentHash = currentHash
				existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.CurrentDescription = tool.Description
				existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousAnnotations = nil
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
				zap.Bool("annotation_hints_match", hintsMatch),
				zap.Int("stored_desc_len", len(existing.CurrentDescription)),
				zap.Int("current_desc_len", len(tool.Description)),
				zap.Int("stored_schema_len", len(existing.CurrentSchema)),
				zap.Int("current_schema_len", len(schemaJSON)))

			// LAST RESORT: If description and output schema match AND the input
			// schema differs only in serialization (RT-01), auto-approve. Output
			// schema is part of the approved contract and must not be bypassed here.
			//
			// RT-01 (red-team 2026-10-11): this branch used to fire on ANY input
			// schema difference once the description matched, in every trust mode
			// except scan — a "sneaky" rug pull that keeps the description
			// byte-identical and only adds a parameter (e.g. an exfiltration sink)
			// was silently auto-approved in the default manual mode. It now fires
			// only when the two schemas are structurally identical (same property
			// set, same required set, same types and constraints — only ordering
			// that normalizeJSON does not absorb, such as the order of the
			// "required" list, may differ). Any structural difference falls
			// through to the change gate below and is held for review.
			if storedApprovalProven && descMatch && outputSchemaMatch && hintsMatch && inputSchemaSerializationEquivalent(existing.CurrentSchema, schemaJSON) {
				if err := r.enforceInvariant(serverName, toolName, existing.Status, storage.ToolApprovalStatusApproved, ReasonDescriptionMatch); err != nil {
					result.BlockedTools[toolName] = true
					result.ChangedCount++
					continue
				}
				existing.Status = storage.ToolApprovalStatusApproved
				existing.ApprovedHash = currentHash
				existing.CurrentHash = currentHash
				existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.CurrentDescription = tool.Description
				existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
				existing.CurrentSchema = schemaJSON
				existing.CurrentOutputSchema = outputSchemaJSON
				existing.PreviousDescription = ""
				existing.PreviousAnnotations = nil
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
				existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
				existing.ApprovedAt = time.Now().UTC()
				existing.ApprovedBy = "auto-approve-changes"
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

			// trust_mode: scan (spec 086 stage 2, tightened by RT-02) — a genuine
			// tool change (rug pull) is ALWAYS held for human review, exactly as in
			// manual mode. A synchronous, offline TPA scan of the new definition
			// still runs so a non-green ("warnings"/"dangerous"), degraded or
			// absent verdict is recorded as hold evidence; a green verdict no
			// longer auto-approves, because paraphrased pulls scan clean.
			// The hold evidence (verdict + matched TPA/check ids) rides along onto
			// the changed record so the tool-approval surfaces can explain the hold
			// (FR-018).
			var holdEvidence *scanHoldEvidence
			if scanMode {
				clean, evidence := r.scanChangeIsClean(serverName, tool)
				if clean {
					// RT-02 (red-team 2026-10-11): the detector is defeatable by
					// paraphrase, so a clean verdict is not proof that a changed
					// contract is safe. Hold it for operator confirmation exactly
					// like manual mode, and say so loudly: the mark-changed path
					// below also emits the tool_description_changed activity event.
					r.logger.Warn("Tool change held for operator confirmation (trust_mode: scan, clean verdict does not auto-approve a changed contract)",
						zap.String("server", serverName),
						zap.String("tool", toolName),
						zap.String("approved_hash", existing.ApprovedHash),
						zap.String("current_hash", currentHash))
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
			existing.PreviousAnnotations = cloneToolAnnotations(existing.CurrentAnnotations)
			if existing.PreviousAnnotations == nil && existing.HashSchemaVersion >= storage.AnnotationHashSchemaVersion {
				// The approved contract's hints were hashed: nil means "all
				// defaults", which the revert check must still compare (RT-03).
				existing.PreviousAnnotations = &config.ToolAnnotations{}
			}
			existing.PreviousSchema = oldSchema
			existing.PreviousOutputSchema = oldOutputSchema
			if !storedApprovalProven {
				// The stored snapshot is not proven to be the approved
				// contract (a v4 ApprovedHash that does not cover it, issue
				// #1606), so it is no before-evidence either: recorded as
				// Previous*, a later pass would "revert" to it and approve
				// it. Without Previous* the record stays changed until an
				// operator confirms it (revertedToPreviousContract).
				existing.PreviousDescription = ""
				existing.PreviousAnnotations = nil
				existing.PreviousSchema = ""
				existing.PreviousOutputSchema = ""
			}
			existing.CurrentHash = currentHash
			existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
			existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
			existing.CurrentDescription = tool.Description
			existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
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
				zap.Bool("annotation_hints_changed", !hintsMatch),
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

	if !serverHasBaseline {
		r.markDiscoveryToolBaselineHeld(serverName)
	}

	if len(result.BlockedTools) > 0 {
		r.logger.Info("Tool-level quarantine: tools blocked",
			zap.String("server", serverName),
			zap.Int("pending", result.PendingCount),
			zap.Int("changed", result.ChangedCount),
			zap.Int("total_blocked", len(result.BlockedTools)))
	}
	if result.PendingCount > 0 || result.ChangedCount > 0 {
		r.emitReviewChanged(serverName)
	}

	// Spec 117 FR-018: a pass whose record writes did not all land is an
	// incomplete capture. The records may still describe an older definition
	// than the one listed, so the caller must not publish this capture.
	if failed := r.approvalPassWriteFailures(serverName); failed > 0 {
		return result, fmt.Errorf("%w: %d approval record write(s) failed for server %s", errCaptureIncomplete, failed, serverName)
	}
	return result, nil
}

// v4ApprovedHashCoversStored reports whether a record approved under the
// pipe-joined v4 formula (HashSchemaVersion storage.AnnotationHashSchemaVersion)
// has an ApprovedHash that is the v4 hash of its stored contract. Only then is
// the stored snapshot evidence of what was approved, which the v4 → v5
// migration needs before it re-baselines the record (issue #1606). Records of
// any other version are not judged here and report true: older ones keep
// their own migration rules, current ones carry an unambiguous hash.
func v4ApprovedHashCoversStored(existing *storage.ToolApprovalRecord, toolName string) bool {
	if existing.HashSchemaVersion != storage.AnnotationHashSchemaVersion {
		return true
	}
	return existing.ApprovedHash == calculateToolApprovalHashV4(toolName,
		existing.CurrentDescription, existing.CurrentSchema, existing.CurrentOutputSchema, existing.CurrentAnnotations)
}

// v4PendingHashMatchesLive reports whether a pending record whose CurrentHash
// was computed with the v4 formula recorded exactly the live contract: every
// stored field equals the live one and CurrentHash is the v4 hash of it. The
// baseline migration (MCP-2931 #4) then still promotes a stranded pending
// record across the v4 → v5 upgrade; a colliding contract never matches,
// because the fields are compared one by one (issue #1606).
func v4PendingHashMatchesLive(existing *storage.ToolApprovalRecord, toolName string, tool *config.ToolMetadata, schemaJSON, outputSchemaJSON string) bool {
	if existing.CurrentHashSchemaVersion != storage.AnnotationHashSchemaVersion || existing.CurrentHash == "" {
		return false
	}
	if existing.CurrentDescription != tool.Description ||
		normalizeJSON(existing.CurrentSchema) != normalizeJSON(schemaJSON) ||
		normalizeJSON(existing.CurrentOutputSchema) != normalizeJSON(outputSchemaJSON) ||
		safetyHintsFingerprint(existing.CurrentAnnotations) != safetyHintsFingerprint(tool.Annotations) {
		return false
	}
	return existing.CurrentHash == calculateToolApprovalHashV4(toolName, tool.Description, schemaJSON, outputSchemaJSON, tool.Annotations)
}

// revertedToPreviousContract reports whether a changed record's live contract
// is the one it was approved for BEFORE the change: description AND
// input/output schemas (astra r1 P1). A schema-only change keeps the
// description equal, so a description-only comparison would restore the
// record — and baseline the changed schema — on the very pass that marked it
// changed (adoptLegacyLockOrBaseline marks and falls through in one pass; the
// ordinary rug-pull path on the next). A Previous* schema the record never
// stored (a pre-output-schema record, or a producer that recorded only the
// description) is not evidence of the approved contract and is never a
// wildcard (Sol r10 P1): without a stored previous input schema the revert is
// proven only when the live contract hashes, under the current formula, to
// the record's ApprovedHash; otherwise the record stays held for an operator.
func revertedToPreviousContract(existing *storage.ToolApprovalRecord, description, schemaJSON, outputSchemaJSON string, annotations *config.ToolAnnotations, currentHash string) bool {
	if description != existing.PreviousDescription {
		return false
	}
	if existing.PreviousDescription == "" && !approvedHashProvesEmptyDescription(existing) {
		return false
	}
	// RT-03: an annotation-only flip keeps description and schemas equal, so
	// the hints must match too or the flip would be "reverted" on the very
	// next pass. The mark-changed path records a non-nil snapshot once the
	// hints are hashed; a nil Previous snapshot (a record marked changed by an
	// older release, or adopted from one) means the approved hints are
	// UNKNOWN. Unknown is not a wildcard (Sol r1 P1): the revert is only
	// proven when the live hints are the MCP defaults — the most conservative
	// reading, which cannot lower any routing or review tier. Live non-default
	// hints keep the record changed until an operator confirms them.
	if !previousHintsProveRevert(existing.PreviousAnnotations, annotations) {
		return false
	}
	if existing.PreviousSchema == "" {
		// Unknown approved input schema (a legacy changed record): only the
		// approved hash itself is positive proof of the input schema.
		if existing.ApprovedHash == "" || currentHash == "" ||
			existing.HashSchemaVersion != storage.ToolApprovalHashSchemaVersion ||
			existing.ApprovedHash != currentHash {
			return false
		}
	} else if normalizeJSON(existing.PreviousSchema) != normalizeJSON(schemaJSON) {
		return false
	}
	// The output schema compares literally: an empty previous output schema
	// is either the approved contract's declared "none" or (on a record whose
	// approval predates output-schema capture) unknown. Neither proves that a
	// live output schema is the approved one, so a tool that GAINED an output
	// schema is never "reverted" by it (Sol r4 P1); an operator confirms it.
	return normalizeJSON(existing.PreviousOutputSchema) == normalizeJSON(outputSchemaJSON)
}

// approvedHashProvesEmptyDescription reports whether a changed record's EMPTY
// PreviousDescription is the approved contract's declared description rather
// than an unrecorded one: its ApprovedHash must be the hash, under the v5 or
// the v4 formula, of the recorded previous contract. Without this a tool
// approved with no description under v4 could never be reverted after the
// v5 upgrade, because its v4 ApprovedHash no longer equals the live hash (Sol
// r2 P2, issue #1606). The previous fields are still compared one by one with
// the live contract below, so a colliding contract never qualifies.
func approvedHashProvesEmptyDescription(existing *storage.ToolApprovalRecord) bool {
	if existing.ApprovedHash == "" || existing.PreviousSchema == "" {
		return false
	}
	name, schema, output, hints := existing.ToolName, existing.PreviousSchema, existing.PreviousOutputSchema, existing.PreviousAnnotations
	return existing.ApprovedHash == calculateToolApprovalHashWithOutputSchema(name, "", schema, output, hints) ||
		existing.ApprovedHash == calculateToolApprovalHashV4(name, "", schema, output, hints)
}

// previousHintsProveRevert reports whether a changed record's live safety
// hints are the approved ones it changed from. A nil previous snapshot means
// the approved hints are unknown; only the MCP defaults (the most conservative
// reading) are then accepted.
func previousHintsProveRevert(previous, live *config.ToolAnnotations) bool {
	if previous == nil {
		return safetyHintsFingerprint(live) == ""
	}
	return safetyHintsFingerprint(previous) == safetyHintsFingerprint(live)
}

// saveToolApproval persists an approval record through the ONLY write seam
// the runtime uses, stamping it as identity-keyed first (Spec 105 FR-009,
// storage.ToolApprovalRecord.IdentityKeyed): every record this binary writes
// is keyed by the exact raw tool name, and the stamp is what tells the legacy
// consults (legacyCollapsedSibling) that a record is a genuine sibling rather
// than a pre-105 collapsed one.
func (r *Runtime) saveToolApproval(record *storage.ToolApprovalRecord) error {
	record.IdentityKeyed = true
	return r.persistToolApproval(record)
}

// persistToolApproval is the storage write behind both save helpers. A
// failure is counted for the pass that holds the server's approval lock, so a
// discovery pass whose record writes did not all land reports an incomplete
// capture (Spec 117 FR-018) instead of a logged and ignored error.
func (r *Runtime) persistToolApproval(record *storage.ToolApprovalRecord) error {
	var err error
	if f := r.testHooks.toolApprovalWriteFault; f != nil {
		err = f(record.ServerName, record.ToolName, "save")
	}
	if err == nil {
		err = r.storageManager.SaveToolApproval(record)
	}
	if err != nil {
		r.noteApprovalWriteFailure(record.ServerName, record.ToolName)
	}
	return err
}

// cloneToolAnnotations preserves a point-in-time upstream snapshot. Annotation
// pointers are optional and callers may reuse them across discovery passes, so
// approval records must not retain aliases to mutable boolean values.
func cloneToolAnnotations(annotations *config.ToolAnnotations) *config.ToolAnnotations {
	if annotations == nil {
		return nil
	}
	clone := *annotations
	if annotations.ReadOnlyHint != nil {
		value := *annotations.ReadOnlyHint
		clone.ReadOnlyHint = &value
	}
	if annotations.DestructiveHint != nil {
		value := *annotations.DestructiveHint
		clone.DestructiveHint = &value
	}
	if annotations.IdempotentHint != nil {
		value := *annotations.IdempotentHint
		clone.IdempotentHint = &value
	}
	if annotations.OpenWorldHint != nil {
		value := *annotations.OpenWorldHint
		clone.OpenWorldHint = &value
	}
	return &clone
}

// capturedToolAnnotations marks a definition written by this runtime as
// captured even when its upstream omitted annotation hints. Historical stored
// nil remains meaningful to reviewTier as "unknown"; only a newly captured
// definition gets the non-nil empty record that means "unannotated".
func capturedToolAnnotations(annotations *config.ToolAnnotations) *config.ToolAnnotations {
	if annotations == nil {
		return &config.ToolAnnotations{}
	}
	return cloneToolAnnotations(annotations)
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
		return r.persistToolApproval(record)
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
	existing.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
	existing.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
	existing.CurrentDescription = tool.Description
	existing.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
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
		existing.PreviousAnnotations = cloneToolAnnotations(sibling.PreviousAnnotations)
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
		existing.PreviousAnnotations = cloneToolAnnotations(sibling.CurrentAnnotations)
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
		HashSchemaVersion:    storage.ToolApprovalHashSchemaVersion,
		ApprovedAt:           sibling.ApprovedAt,
		ApprovedBy:           sibling.ApprovedBy,
		PreviousDescription:  sibling.PreviousDescription,
		PreviousAnnotations:  cloneToolAnnotations(sibling.PreviousAnnotations),
		CurrentAnnotations:   cloneToolAnnotations(sibling.CurrentAnnotations),
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
		record.CurrentHashSchemaVersion = sibling.CurrentHashSchemaVersion
		record.CurrentDescription = sibling.CurrentDescription
		record.CurrentSchema = sibling.CurrentSchema
		record.CurrentOutputSchema = sibling.CurrentOutputSchema
	} else {
		record.CurrentHash = currentHash
		record.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
		record.CurrentDescription = tool.Description
		record.CurrentAnnotations = cloneToolAnnotations(tool.Annotations)
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
	if legacyHintsKnownToDiffer(sibling.CurrentAnnotations, sibling.HashSchemaVersion, tool.Annotations) {
		// Every formula below except the current one is hint-free (or, for
		// the live-annotations formula, hashed from the LIVE hints), so a
		// pre-RT-03 sibling whose snapshot shows other hints never approves
		// the live contract (Sol r1 P1).
		return false
	}
	if sibling.ApprovedHash == calculateToolApprovalHashWithOutputSchema(collapsed, tool.Description, schemaJSON, outputSchemaJSON, tool.Annotations) {
		return true
	}
	// An empty stored schema is a wildcard only on a sibling that predates
	// complete contract capture, exactly as in the main gate's formula
	// migration: from storage.OutputSchemaHashSchemaVersion on an empty
	// stored output schema is the DECLARED contract ("none"), so a later
	// (possibly poisoned) output schema is a change, never adopted (Sol r5 P1).
	uncaptured := sibling.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion
	descMatch := sibling.CurrentDescription == tool.Description
	schemaMatch := (sibling.CurrentSchema == "" && uncaptured) || normalizeJSON(sibling.CurrentSchema) == normalizeJSON(schemaJSON)
	outputMatch := (sibling.CurrentOutputSchema == "" && uncaptured) || normalizeJSON(sibling.CurrentOutputSchema) == normalizeJSON(outputSchemaJSON)
	hintsMatch := storedSafetyHintsMatch(sibling.CurrentAnnotations, sibling.HashSchemaVersion, tool.Annotations)
	// Every older formula is pipe-joined and ambiguous (issue #1606): a
	// different contract can hash to the sibling's approved value. A legacy
	// hash match therefore only counts when the sibling's stored contract
	// does not contradict the live one (an uncaptured empty field is the
	// one wildcard).
	storedAgrees := (descMatch || (sibling.CurrentDescription == "" && uncaptured)) && schemaMatch && outputMatch
	if sibling.HashSchemaVersion < storage.AnnotationHashSchemaVersion && storedAgrees &&
		sibling.ApprovedHash == calculateToolApprovalHashV4(collapsed, tool.Description, schemaJSON, outputSchemaJSON, nil) {
		// Pre-RT-03 hash (no annotation hints) with unchanged or unknown hints.
		return true
	}
	if sibling.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion && storedAgrees {
		if sibling.ApprovedHash == calculateLegacyToolApprovalHash(collapsed, tool.Description, schemaJSON) ||
			sibling.ApprovedHash == calculateHashWithAnnotations(collapsed, tool.Description, schemaJSON, tool.Annotations) {
			return true
		}
	}
	if sibling.HashSchemaVersion == storage.AnnotationHashSchemaVersion {
		// A v4 sibling captured the full contract (an empty description is
		// declared, not unknown). Its approval covers the live contract only
		// when its v4 ApprovedHash is the hash of its stored contract and
		// that contract is exactly the live one (issue #1606).
		return v4ApprovedHashCoversStored(sibling, collapsed) && descMatch && schemaMatch && outputMatch && hintsMatch
	}
	if sibling.ApprovedHash != sibling.CurrentHash || sibling.CurrentDescription == "" {
		return false
	}
	return descMatch && schemaMatch && outputMatch && hintsMatch
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
	return r.applyServerDiffIfEligibleGuarded(ctx, serverName, tools, 0, inventoryRef{}, nil)
}

// applyServerDiffIfEligibleGuarded is applyServerDiffIfEligible for the
// inventory inv, with the under-lock stale check of
// applyDifferentialToolUpdateGuarded. An inventory the capture-order gate
// refuses (Spec 117 PR-3) writes nothing and reports false.
func (r *Runtime) applyServerDiffIfEligibleGuarded(ctx context.Context, serverName string, tools []*config.ToolMetadata, instanceGen int64, inv inventoryRef, stale func() bool) bool {
	if !r.serverEligibleForIndexing(serverName) {
		r.logger.Info("Skipping sweep index write for ineligible server (disabled or quarantined)",
			zap.String("server", serverName))
		return false
	}
	if err := r.applyInventoryGuarded(ctx, serverName, tools, instanceGen, inv, stale, nil); err != nil {
		if errors.Is(err, errLocalSnapshotPurged) || errors.Is(err, errDiscoveryStaleGeneration) || errors.Is(err, errDiscoveryIneligible) || errors.Is(err, errInventoryStale) || errors.Is(err, errInventoryNotCurrent) {
			return false
		}
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
	// DiscoverAndIndexToolsForServer. Selection and application happen under one
	// captureSnapMu hold so a concurrent capture cannot be overwritten by a
	// stale snapshot (which would look like a revert and clear a changed hold).
	if r.reindexFromLastGoodSnapshot(serverName) {
		return
	}

	// No snapshot captured yet — fall back to a full single-server
	// rediscover, which indexes and populates the snapshot for next time.
	// Use the authoritative path so a server now reporting zero tools has
	// stale entries cleared rather than resurfaced (issue #873).
	if err := r.RefreshServerTools(r.AppContext(), serverName); err != nil {
		r.logger.Debug("Approval-driven rediscovery failed",
			zap.String("server", serverName), zap.Error(err))
	}
}

// reindexFromLastGoodSnapshot re-runs the differential update from the
// last-good snapshot under captureSnapMu. It reports false when no snapshot
// exists yet, or (Spec 117 PR-3, FR-037) when the snapshot no longer
// describes the live connection — its client was replaced, retired or
// reconnected, or a newer capture with definitions was applied — so the caller
// re-lists the server instead of re-applying older definitions.
func (r *Runtime) reindexFromLastGoodSnapshot(serverName string) bool {
	r.captureSnapMu.Lock()
	defer r.captureSnapMu.Unlock()

	snapshot, inv, seq, snapshotGen := r.lastGoodSnapshotWithCaptureGen(serverName)
	if len(snapshot) == 0 {
		return false
	}
	if hook := r.localReindexAfterSnapshot; hook != nil {
		hook()
	}

	// TOCTOU GUARD (issue #873): re-check eligibility immediately before the
	// index write so a server quarantined in the interim is not re-populated.
	if !r.serverEligibleForIndexing(serverName) {
		return true
	}

	// Re-run the differential update against the current approval records.
	// Rug-pull safety is preserved: if the tool mutated after approval,
	// checkToolApprovals re-flags it changed and it stays blocked.
	// Spec 116 PR3 (FR-021b): the snapshot is bound to the commit generation
	// of the instance it was listed from. After an endpoint change it is
	// rejected by the final check, and the caller re-lists the current
	// instance instead of reapplying the old endpoint's tools.
	stale := func() bool { return r.lastGoodPurgedSince(serverName, seq) }
	err := r.applyInventoryGuarded(r.AppContext(), serverName, snapshot, snapshotGen, inventoryRef{capture: inv, mode: inventoryReconcile}, stale, nil)
	switch {
	case errors.Is(err, errDiscoveryStaleGeneration):
		r.logger.Info("Last-good snapshot belongs to a superseded instance generation; re-listing the server",
			zap.String("server", serverName), zap.Int64("snapshot_generation", snapshotGen))
		return false
	case errors.Is(err, errInventoryNotCurrent), errors.Is(err, errInventoryStale):
		r.logger.Info("Approval reindex re-lists the server: its last-good snapshot no longer describes the live connection",
			zap.String("server", serverName))
		return false
	case err != nil && !errors.Is(err, errLocalSnapshotPurged) && !errors.Is(err, errDiscoveryIneligible):
		r.logger.Warn("Failed to reindex tools after approval change",
			zap.String("server", serverName), zap.Error(err))
	}
	return true
}

// ApproveTools approves specific tools for a server, updating their status to approved.
// It is bound to the server's incarnation when it starts (Spec 116 FR-014):
// if the server is removed or replaced before the write, it writes nothing and
// returns ErrServerReplaced.
func (r *Runtime) ApproveTools(serverName string, toolNames []string, approvedBy string) error {
	return r.ApproveToolsAt(serverName, toolNames, approvedBy, 0)
}

// ApproveToolsAt is ApproveTools bound to expectedIncarnation, the incarnation
// the caller reviewed (the REST expected_incarnation). 0 binds to the
// incarnation current when the call starts.
func (r *Runtime) ApproveToolsAt(serverName string, toolNames []string, approvedBy string, expectedIncarnation int64) error {
	_, err := r.approveToolsAndNotify(serverName, toolNames, approvedBy, false, expectedIncarnation)
	return err
}

// approveToolsAndNotify runs approveToolsLocked under the server's
// tool-approval lock, after the incarnation check, then — with the lock
// released — emits the SSE events and spawns the reindex (which re-enters
// checkToolApprovals and takes the lock itself). Returns the number of
// records actually approved.
func (r *Runtime) approveToolsAndNotify(serverName string, toolNames []string, approvedBy string, pendingOnly bool, expectedIncarnation int64) (int, error) {
	if r.storageManager == nil {
		return 0, nil
	}
	inc, err := r.CaptureApprovalIncarnation(serverName, expectedIncarnation)
	if err != nil {
		return 0, err
	}
	unlock := r.lockToolApprovals(serverName)
	if err := r.checkApprovalIncarnationHeld(serverName, inc); err != nil {
		unlock()
		return 0, err
	}
	approved, err := r.approveToolsLocked(serverName, toolNames, approvedBy, pendingOnly)
	unlock()
	r.notifyToolsApproved(serverName, approved, approvedBy)
	return approved, err
}

// approveToolsLocked approves the named tools. The caller holds the server's
// tool-approval lock. Each record is re-read here, under the lock, so the
// decision is made on the current record, never on a listing taken earlier.
// With pendingOnly (server-approval baseline trust) only records still
// "pending" are promoted: a record that turned "changed" (rug-pull) is left
// for explicit review, and approved records are not rewritten. Missing
// records are skipped. Returns the number of records written approved.
func (r *Runtime) approveToolsLocked(serverName string, toolNames []string, approvedBy string, pendingOnly bool) (int, error) {
	// Spec 117 FR-018: while the server's latest capture is incomplete the
	// stored definitions may be older than the live ones; approving them
	// would approve a definition nobody reviewed.
	if err := r.reviewNotRecordedHeld(serverName); err != nil {
		return 0, err
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
		if pendingOnly && record.Status != storage.ToolApprovalStatusPending {
			continue
		}

		if err := r.enforceInvariant(serverName, toolName, record.Status, storage.ToolApprovalStatusApproved, ReasonUserApprove); err != nil {
			return approved, err
		}

		// Spec 105 FR-009: an operator write on a pre-105 record must not
		// end its legacy consult while it still restricts (saveReadToolApproval).
		wasUnstamped := !record.IdentityKeyed

		record.Status = storage.ToolApprovalStatusApproved
		record.ApproveCurrentHash()
		// HashSchemaVersion follows the copied hash (ApproveCurrentHash):
		// a legacy hint-free CurrentHash keeps its legacy version (Sol r2
		// P1), a CurrentHash this binary recomputed carries the current one
		// (Sol r4 P1).
		record.ApprovedAt = time.Now().UTC()
		record.ApprovedBy = approvedBy
		record.PreviousDescription = ""
		record.PreviousAnnotations = nil
		record.PreviousSchema = ""
		record.PreviousOutputSchema = ""
		record.ClearScanHold()

		if err := r.saveReadToolApproval(record, wasUnstamped); err != nil {
			return approved, err
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
	return approved, nil
}

// notifyToolsApproved publishes the approval to SSE subscribers and reindexes.
// Called WITHOUT the tool-approval lock held.
func (r *Runtime) notifyToolsApproved(serverName string, approved int, approvedBy string) {
	// Notify SSE subscribers that the server's tool-quarantine counts changed.
	// Without this, a Servers/overview page open in another tab/window keeps
	// showing the stale "N pending approval" badge until the user manually
	// reloads — see issue #438. Emit once per call (not per tool) to keep
	// the bus quiet on bulk approvals.
	if approved > 0 {
		r.emitReviewChanged(serverName)
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
//
// inc is the incarnation the caller captured (Spec 116 FR-014); a record is
// never written, or synthesized, for a server that was removed or replaced
// since.
func (r *Runtime) setToolEnabledNoEmit(serverName, toolName string, enabled bool, updatedBy string, inc int64) (bool, error) {
	if r.storageManager == nil {
		return false, nil
	}
	defer r.lockToolApprovals(serverName)()
	if err := r.checkApprovalIncarnationHeld(serverName, inc); err != nil {
		return false, err
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
		record = r.newToggleSynthesizedRecord(serverName, toolName, updatedBy, inc)
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
	// Spec 117 FR-018: enabling an approved record makes it reachable; not
	// while the server's latest capture is incomplete. Disabling stays
	// available.
	if enabled && record.Status == storage.ToolApprovalStatusApproved {
		if err := r.reviewNotRecordedHeld(serverName); err != nil {
			return false, err
		}
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
func (r *Runtime) newToggleSynthesizedRecord(serverName, toolName, updatedBy string, inc int64) *storage.ToolApprovalRecord {
	record := &storage.ToolApprovalRecord{
		ServerName: serverName,
		ToolName:   toolName,
	}
	// The snapshot's current contract, when the StateView lists the raw name.
	if tool := r.snapshotToolMetadata(serverName, toolName, inc); tool != nil {
		schemaJSON := tool.ParamsJSON
		if schemaJSON == "" {
			schemaJSON = "{}"
		}
		schemaJSON = normalizeJSON(schemaJSON)
		outputSchemaJSON := normalizeJSON(tool.OutputSchemaJSON)
		record.CurrentHash = calculateToolApprovalHashWithOutputSchema(toolName, tool.Description, schemaJSON, outputSchemaJSON, tool.Annotations)
		record.CurrentHashSchemaVersion = storage.ToolApprovalHashSchemaVersion
		record.HashSchemaVersion = storage.ToolApprovalHashSchemaVersion
		record.CurrentDescription = tool.Description
		record.CurrentSchema = schemaJSON
		record.CurrentOutputSchema = outputSchemaJSON
		// The hint-aware hash needs its hint snapshot (RT-03): a record
		// with nil annotations re-hashes as default hints, so a later
		// non-default → default flip would pass as a formula migration
		// (Sol r7 P2).
		record.CurrentAnnotations = capturedToolAnnotations(tool.Annotations)
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
	record.ApproveCurrentHash()
	record.ApprovedAt = time.Now().UTC()
	record.ApprovedBy = updatedBy
	return record
}

// viewToolsAt returns the StateView's tools for serverName when the view was
// reconciled from incarnation inc, and nil otherwise (Spec 116 PR2 review
// round 8, FR-014/FR-017). The StateView lags the commit: after a remove and
// same-name re-add it can still hold the removed server's tools under the
// removed incarnation, and an approval writer bound to the replacement must
// never persist those definitions as the replacement's records. inc == 0 is
// an unbound capture (a database no Spec 116 commit has written) and accepts
// the view as before.
func (r *Runtime) viewToolsAt(serverName string, inc int64) []stateview.ToolInfo {
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
	if inc != 0 && status.Incarnation != inc {
		r.logger.Debug("StateView lags the bound incarnation; not reading its tools for an approval write",
			zap.String("server", serverName),
			zap.Int64("view_incarnation", status.Incarnation),
			zap.Int64("bound_incarnation", inc))
		return nil
	}
	return status.Tools
}

// snapshotToolMetadata returns the StateView's current contract for a raw
// tool name on a server — the same fields the discovery producer hashes —
// or nil when the snapshot does not list the exact name. The StateView holds
// raw names verbatim (Spec 105 FR-009), so only the exact name matches.
// inc is the incarnation the writer is bound to: a view reconciled from any
// other incarnation is not consulted (see viewToolsAt).
func (r *Runtime) snapshotToolMetadata(serverName, toolName string, inc int64) *config.ToolMetadata {
	viewTools := r.viewToolsAt(serverName, inc)
	for i := range viewTools {
		info := viewTools[i]
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
	return r.SetToolEnabledAt(serverName, toolName, enabled, updatedBy, 0)
}

// SetToolEnabledAt is SetToolEnabled bound to expectedIncarnation (0 binds to
// the incarnation current when the call starts; Spec 116 FR-014).
func (r *Runtime) SetToolEnabledAt(serverName, toolName string, enabled bool, updatedBy string, expectedIncarnation int64) error {
	if r.storageManager == nil {
		return nil
	}
	inc, err := r.CaptureApprovalIncarnation(serverName, expectedIncarnation)
	if err != nil {
		return err
	}
	changed, err := r.setToolEnabledNoEmit(serverName, toolName, enabled, updatedBy, inc)
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
	return r.SetAllToolsEnabledAt(serverName, enabled, updatedBy, 0)
}

// SetAllToolsEnabledAt is SetAllToolsEnabled bound to expectedIncarnation (0
// binds to the incarnation current when the call starts; Spec 116 FR-014).
// Every per-tool write re-checks it, so a remove and re-add midway stops the
// loop's writes on the replacement.
func (r *Runtime) SetAllToolsEnabledAt(serverName string, enabled bool, updatedBy string, expectedIncarnation int64) (int, error) {
	if r.storageManager == nil {
		return 0, nil
	}
	if serverName == "" {
		return 0, fmt.Errorf("server name required")
	}
	inc, err := r.CaptureApprovalIncarnation(serverName, expectedIncarnation)
	if err != nil {
		return 0, err
	}

	toolNames, err := r.collectKnownToolNames(serverName, inc)
	if err != nil {
		return 0, err
	}

	changed, attempted := 0, 0
	for _, toolName := range toolNames {
		// Never enable a tool the config denies — user-owned Disabled flag is
		// irrelevant here; enforcement is in isToolCallable, but we avoid a
		// misleading record.Disabled=false for a hard-off tool.
		if enabled && r.IsToolConfigDenied(serverName, toolName) {
			continue
		}
		attempted++
		flipped, setErr := r.setToolEnabledNoEmit(serverName, toolName, enabled, updatedBy, inc)
		if errors.Is(setErr, ErrServerReplaced) || errors.Is(setErr, toolreview.ErrReviewNotRecorded) {
			return changed, setErr
		}
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
	if attempted == 0 {
		// No per-tool write ran, so no write checked the incarnation: an
		// empty or all-config-denied inventory must still refuse a request
		// bound to a removed or replaced server (review round 3).
		if err := r.WithServerApprovalLockAt(serverName, inc, func() error { return nil }); err != nil {
			return 0, err
		}
		return 0, nil
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
//
// inc is the incarnation the bulk writer is bound to (Spec 116 FR-014): the
// StateView lags the commit, so after a remove + same-name re-add it can
// still list the removed server's tools; it is consulted only when it was
// reconciled from inc (review round 8). The index and the approval records
// need no such check — the removal commit purged both.
func (r *Runtime) collectKnownToolNames(serverName string, inc int64) ([]string, error) {
	seen := make(map[string]struct{})
	add := func(name string) {
		if name == "" {
			return
		}
		seen[name] = struct{}{}
	}

	for _, tool := range r.viewToolsAt(serverName, inc) {
		add(tool.Name)
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
	_, err := r.approveBaselineToolsForServerCount(serverName)
	return err
}

// approveBaselineToolsForServerAt is the unquarantine baseline promotion bound
// to the incarnation of the commit that unquarantined the server (Spec 116
// FR-014i, review R9.5): the caller passes the incarnation from that commit's
// result (or the request's expected_incarnation) and never recaptures it by
// name. Under the server's approval lock it requires the stored row to carry
// that incarnation and to be unquarantined; otherwise it promotes nothing and
// returns ErrServerReplaced (the replacement's pending records stay pending)
// or nil (the server was re-quarantined meanwhile).
func (r *Runtime) approveBaselineToolsForServerAt(serverName string, incarnation int64) error {
	_, err := r.approveBaselineToolsForServerCountAt(serverName, incarnation)
	if errors.Is(err, ErrServerReplaced) {
		r.logger.Debug("Baseline promotion after unquarantine skipped: the server was removed or replaced",
			zap.String("server", serverName), zap.Int64("incarnation", incarnation), zap.Error(err))
	}
	return err
}

// approveBaselineToolsForServerCount is approveBaselineToolsForServer
// returning the number of records promoted. Listing and promotion run under
// one hold of the server's tool-approval lock, and approveToolsLocked re-reads
// each record (pendingOnly), so a record that became "changed" is never
// promoted and no concurrent pass can revert the promotion.
func (r *Runtime) approveBaselineToolsForServerCount(serverName string) (int, error) {
	return r.approveBaselineToolsForServerCountAt(serverName, 0)
}

// approveBaselineToolsForServerCountAt is approveBaselineToolsForServerCount
// bound to incarnation (0: unbound, the legacy callers). A bound promotion
// also requires the stored row to be unquarantined under the lock.
func (r *Runtime) approveBaselineToolsForServerCountAt(serverName string, incarnation int64) (int, error) {
	if r.storageManager == nil {
		return 0, nil
	}
	const approvedBy = "system:server-approval-baseline"

	unlock := r.lockToolApprovals(serverName)
	if incarnation != 0 {
		if err := r.checkApprovalIncarnationHeld(serverName, incarnation); err != nil {
			unlock()
			return 0, err
		}
		if r.approvalBindingEnforced() && r.serverQuarantinedInStorage(serverName) {
			unlock()
			r.logger.Debug("Baseline promotion after unquarantine skipped: the server is quarantined again",
				zap.String("server", serverName), zap.Int64("incarnation", incarnation))
			return 0, nil
		}
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		unlock()
		return 0, err
	}
	var pendingTools []string
	for _, record := range records {
		if record.Status == storage.ToolApprovalStatusPending {
			pendingTools = append(pendingTools, record.ToolName)
		}
	}
	approved := 0
	if len(pendingTools) > 0 && r.reviewDirtyHeld(serverName) {
		// Spec 117 FR-018: a non-review unquarantine leaves pending tools
		// pending while the server's latest capture is incomplete.
		r.logger.Warn("Baseline promotion after unquarantine skipped: the server's latest tool capture is incomplete",
			zap.String("server", serverName), zap.Int("pending", len(pendingTools)))
		pendingTools = nil
	}
	if len(pendingTools) > 0 {
		// approveToolsLocked sets ApprovedHash=CurrentHash and runs
		// enforceInvariant (pending→approved is permitted).
		approved, err = r.approveToolsLocked(serverName, pendingTools, approvedBy, true)
	}
	unlock()
	r.notifyToolsApproved(serverName, approved, approvedBy)
	if err != nil {
		return approved, err
	}
	if approved > 0 {
		r.logger.Info("Baseline-approved pending tools on server approval",
			zap.String("server", serverName),
			zap.Int("count", approved))
	}
	return approved, nil
}

// ApproveAllTools approves all pending/changed tools for a server. Listing
// and approval run under one hold of the server's tool-approval lock; the
// count is the number of records actually approved.
func (r *Runtime) ApproveAllTools(serverName string, approvedBy string) (int, error) {
	return r.ApproveAllToolsAt(serverName, approvedBy, 0)
}

// ApproveAllToolsAt is ApproveAllTools bound to expectedIncarnation (0 binds
// to the incarnation current when the call starts; Spec 116 FR-014).
func (r *Runtime) ApproveAllToolsAt(serverName string, approvedBy string, expectedIncarnation int64) (int, error) {
	if r.storageManager == nil {
		return 0, nil
	}
	inc, err := r.CaptureApprovalIncarnation(serverName, expectedIncarnation)
	if err != nil {
		return 0, err
	}

	unlock := r.lockToolApprovals(serverName)
	if err := r.checkApprovalIncarnationHeld(serverName, inc); err != nil {
		unlock()
		return 0, err
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		unlock()
		return 0, err
	}
	var toolNames []string
	for _, record := range records {
		if record.Status == storage.ToolApprovalStatusPending || record.Status == storage.ToolApprovalStatusChanged {
			toolNames = append(toolNames, record.ToolName)
		}
	}
	approved := 0
	if len(toolNames) > 0 {
		approved, err = r.approveToolsLocked(serverName, toolNames, approvedBy, false)
	}
	unlock()
	r.notifyToolsApproved(serverName, approved, approvedBy)
	return approved, err
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
	return r.BlockToolsAt(serverName, toolNames, blockedBy, 0)
}

// BlockToolsAt is BlockTools bound to expectedIncarnation (0 binds to the
// incarnation current when the call starts; Spec 116 FR-014).
func (r *Runtime) BlockToolsAt(serverName string, toolNames []string, blockedBy string, expectedIncarnation int64) (int, error) {
	if r.storageManager == nil {
		return 0, nil
	}
	inc, err := r.CaptureApprovalIncarnation(serverName, expectedIncarnation)
	if err != nil {
		return 0, err
	}
	unlock := r.lockToolApprovals(serverName)
	if err := r.checkApprovalIncarnationHeld(serverName, inc); err != nil {
		unlock()
		return 0, err
	}
	blocked, err := r.blockToolsLocked(serverName, toolNames, blockedBy)
	unlock()
	r.notifyToolsBlocked(serverName, blocked, blockedBy)
	return blocked, err
}

// blockToolsLocked is BlockTools' record loop. The caller holds the server's
// tool-approval lock; each record is re-read under it.
func (r *Runtime) blockToolsLocked(serverName string, toolNames []string, blockedBy string) (int, error) {
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
		record.ApproveCurrentHash()
		// HashSchemaVersion follows the copied hash (ApproveCurrentHash):
		// a legacy hint-free CurrentHash keeps its legacy version (Sol r2
		// P1), a CurrentHash this binary recomputed carries the current one
		// (Sol r4 P1).
		record.ApprovedAt = time.Now().UTC()
		record.ApprovedBy = blockedBy
		record.PreviousDescription = ""
		record.PreviousAnnotations = nil
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
	return blocked, nil
}

// notifyToolsBlocked publishes a block to SSE subscribers and reindexes.
// Called WITHOUT the tool-approval lock held.
func (r *Runtime) notifyToolsBlocked(serverName string, blocked int, blockedBy string) {
	// One SSE emit per call (not per tool) so an open Servers/overview page
	// refreshes its quarantine badge — mirrors ApproveTools.
	if blocked > 0 {
		r.emitReviewChanged(serverName)
		r.emitServersChanged("tools_blocked", map[string]any{
			"server":        serverName,
			"blocked_count": blocked,
			"blocked_by":    blockedBy,
		})
		// Evict the now-disabled tools from the index promptly (issue #873).
		// Covers BlockAllTools, which delegates here.
		go r.reindexServerToolsAfterApprovalChange(serverName)
	}
}

// BlockAllTools blocks (approve+disable) every pending/changed tool for a
// server. Mirrors ApproveAllTools' selection set so the two bulk operations
// dismiss the same quarantine queue — approve keeps tools visible, block hides
// them. Returns the number of tools blocked.
func (r *Runtime) BlockAllTools(serverName string, blockedBy string) (int, error) {
	return r.BlockAllToolsAt(serverName, blockedBy, 0)
}

// BlockAllToolsAt is BlockAllTools bound to expectedIncarnation (0 binds to
// the incarnation current when the call starts; Spec 116 FR-014).
func (r *Runtime) BlockAllToolsAt(serverName string, blockedBy string, expectedIncarnation int64) (int, error) {
	if r.storageManager == nil {
		return 0, nil
	}
	inc, err := r.CaptureApprovalIncarnation(serverName, expectedIncarnation)
	if err != nil {
		return 0, err
	}

	unlock := r.lockToolApprovals(serverName)
	if err := r.checkApprovalIncarnationHeld(serverName, inc); err != nil {
		unlock()
		return 0, err
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		unlock()
		return 0, err
	}
	var toolNames []string
	for _, record := range records {
		if record.Status == storage.ToolApprovalStatusPending || record.Status == storage.ToolApprovalStatusChanged {
			toolNames = append(toolNames, record.ToolName)
		}
	}
	blocked := 0
	if len(toolNames) > 0 {
		blocked, err = r.blockToolsLocked(serverName, toolNames, blockedBy)
	}
	unlock()
	r.notifyToolsBlocked(serverName, blocked, blockedBy)
	return blocked, err
}

// RecordToolBlocksForSecurityApproval publishes the per-tool audit events for
// records that the scanner service has already committed atomically with the
// integrity baseline. It deliberately does not write approval state again.
func (r *Runtime) RecordToolBlocksForSecurityApproval(serverName string, toolNames []string, blockedBy string) {
	for _, toolName := range toolNames {
		record, err := r.storageManager.GetToolApproval(serverName, toolName)
		if err != nil {
			r.logger.Error("Failed to read atomically blocked tool for audit event",
				zap.String("server", serverName), zap.String("tool", toolName), zap.Error(err))
			r.emitToolQuarantineEvent(serverName, toolName, "tool_blocked", "", "", "", "", "", "")
			continue
		}
		r.emitToolQuarantineEvent(serverName, toolName, "tool_blocked",
			"", record.ApprovedHash, "", record.CurrentDescription, "", record.CurrentSchema)
	}
	if len(toolNames) > 0 {
		r.emitReviewChanged(serverName)
		r.emitServersChanged("security_approval_tool_blocks", map[string]any{
			"server": serverName, "blocked_count": len(toolNames), "blocked_by": blockedBy,
		})
	}
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
