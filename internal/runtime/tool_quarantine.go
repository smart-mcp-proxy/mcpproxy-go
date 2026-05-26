package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/hash"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
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
			ReasonContentMatch, ReasonDescriptionMatch, ReasonUserApprove:
			return nil
		default:
			return fmt.Errorf("invariant violation: changed→approved with reason %q "+
				"(requires user action or description revert)", reason)
		}
	case storage.ToolApprovalStatusPending:
		switch reason {
		case ReasonUserApprove, ReasonAutoApprove:
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

// ToolApprovalResult contains the result of checking tool approvals for a server.
type ToolApprovalResult struct {
	// BlockedTools is the set of tool names that should not be indexed (pending, changed, or disabled).
	BlockedTools map[string]bool
	// PendingCount is the number of newly discovered tools awaiting approval.
	PendingCount int
	// ChangedCount is the number of tools whose description/schema changed since approval.
	ChangedCount int
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
	cfg := r.Config()
	globalEnabled := cfg.IsQuarantineEnabled()

	serverSkipped := false
	serverQuarantined := false
	for _, sc := range cfg.Servers {
		if sc.Name == serverName {
			serverSkipped = sc.IsQuarantineSkipped()
			serverQuarantined = sc.Quarantined
			break
		}
	}

	// Quarantine enforcement levels:
	// 1. enforceNewTools: block NEW tools for review (unless quarantine is disabled or server skipped)
	// 2. enforceQuarantine: full quarantine mode for servers explicitly quarantined
	//
	// Even trusted (non-quarantined) servers should have new tools reviewed when quarantine
	// is globally enabled. This prevents injection attacks via new tool additions on
	// compromised servers. Only skip_quarantine=true explicitly opts out.
	// Changed tools (rug pull) are always blocked when globalEnabled is true (line ~438).
	enforceNewTools := globalEnabled && !serverSkipped
	enforceQuarantine := globalEnabled && !serverSkipped && serverQuarantined

	result := &ToolApprovalResult{
		BlockedTools: make(map[string]bool),
	}

	schemaVersion, schemaVersionErr := r.storageManager.GetSchemaVersion()
	outputSchemaHashMigration := schemaVersionErr == nil && schemaVersion < storage.OutputSchemaHashSchemaVersion
	migratedOutputSchemaApprovals := false

	for _, tool := range tools {
		// Extract the bare tool name (without server prefix)
		toolName := extractToolName(tool.Name)

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

		if existing != nil && existing.Status == storage.ToolApprovalStatusApproved && outputSchemaHashMigration && existing.HashSchemaVersion < storage.OutputSchemaHashSchemaVersion {
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
				if saveErr := r.storageManager.SaveToolApproval(existing); saveErr != nil {
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
			// No existing record - this is a new tool.
			if !enforceNewTools {
				// Quarantine disabled or server has skip_quarantine - auto-approve
				now := time.Now().UTC()
				record := &storage.ToolApprovalRecord{
					ServerName:          serverName,
					ToolName:            toolName,
					CurrentHash:         currentHash,
					ApprovedHash:        currentHash,
					HashSchemaVersion:   storage.OutputSchemaHashSchemaVersion,
					Status:              storage.ToolApprovalStatusApproved,
					ApprovedBy:          "auto",
					ApprovedAt:          now,
					CurrentDescription:  tool.Description,
					CurrentSchema:       schemaJSON,
					CurrentOutputSchema: outputSchemaJSON,
				}

				if saveErr := r.storageManager.SaveToolApproval(record); saveErr != nil {
					r.logger.Error("Failed to save auto-approved tool record",
						zap.String("server", serverName),
						zap.String("tool", toolName),
						zap.Error(saveErr))
					continue
				}

				r.logger.Info("New tool discovered, auto-approved (quarantine disabled or server skipped)",
					zap.String("server", serverName),
					zap.String("tool", toolName))

				r.emitToolQuarantineEvent(serverName, toolName, "tool_auto_approved", "", currentHash,
					"", tool.Description, "", schemaJSON)

				continue
			}

			// Quarantine enabled — new tool requires user review before use.
			// This applies to ALL servers (including trusted ones) to prevent
			// injection attacks via new tool additions on compromised servers.
			record := &storage.ToolApprovalRecord{
				ServerName:          serverName,
				ToolName:            toolName,
				CurrentHash:         currentHash,
				HashSchemaVersion:   storage.OutputSchemaHashSchemaVersion,
				Status:              storage.ToolApprovalStatusPending,
				CurrentDescription:  tool.Description,
				CurrentSchema:       schemaJSON,
				CurrentOutputSchema: outputSchemaJSON,
			}

			if saveErr := r.storageManager.SaveToolApproval(record); saveErr != nil {
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

			result.BlockedTools[toolName] = true
			result.PendingCount++

			r.emitToolQuarantineEvent(serverName, toolName, "tool_discovered", "", currentHash,
				"", tool.Description, "", schemaJSON)

			continue
		}

		if existing.Disabled {
			result.BlockedTools[toolName] = true
		}

		// Existing record found - check if hash matches
		if existing.ApprovedHash == currentHash {
			needsSave := false
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
				if saveErr := r.storageManager.SaveToolApproval(existing); saveErr != nil {
					r.logger.Debug("Failed to update tool approval record",
						zap.String("server", serverName),
						zap.String("tool", toolName),
						zap.Error(saveErr))
				}
			}
			continue
		}

		if existing.Status == storage.ToolApprovalStatusPending {
			// Still pending - update current info
			existing.CurrentHash = currentHash
			existing.CurrentDescription = tool.Description
			existing.CurrentSchema = schemaJSON
			existing.CurrentOutputSchema = outputSchemaJSON
			if saveErr := r.storageManager.SaveToolApproval(existing); saveErr != nil {
				r.logger.Debug("Failed to update pending tool approval",
					zap.String("server", serverName),
					zap.String("tool", toolName),
					zap.Error(saveErr))
			}

			if enforceQuarantine {
				result.BlockedTools[toolName] = true
				result.PendingCount++
			}
			continue
		}

		// If tool was previously marked "changed", check if the tool has reverted
		// to its PREVIOUS (pre-change) description. Only auto-approve if the
		// description matches the APPROVED version, not the current (changed) one.
		// This prevents the bug where a changed tool gets auto-approved on the
		// next checkToolApprovals pass because CurrentDescription was already
		// updated to the new description.
		if existing.Status == storage.ToolApprovalStatusChanged {
			// Only restore if the tool reverted to the PREVIOUS (approved) description
			if existing.PreviousDescription != "" && tool.Description == existing.PreviousDescription {
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
				if saveErr := r.storageManager.SaveToolApproval(existing); saveErr == nil {
					r.logger.Info("Changed tool restored (reverted to previous description)",
						zap.String("server", serverName),
						zap.String("tool", toolName))
				}
				continue
			}
			// Tool still has the changed description — keep it blocked
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
				if saveErr := r.storageManager.SaveToolApproval(existing); saveErr != nil {
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
				if saveErr := r.storageManager.SaveToolApproval(existing); saveErr == nil {
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
			if descMatch && outputSchemaMatch {
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
				if saveErr := r.storageManager.SaveToolApproval(existing); saveErr == nil {
					r.logger.Info("Tool auto-approved (description matches, schema format differs)",
						zap.String("server", serverName),
						zap.String("tool", toolName))
				}
				continue
			}

			// Hash differs AND description differs - genuine tool change (rug pull)
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

			if saveErr := r.storageManager.SaveToolApproval(existing); saveErr != nil {
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

	if len(result.BlockedTools) > 0 {
		r.logger.Info("Tool-level quarantine: tools blocked",
			zap.String("server", serverName),
			zap.Int("pending", result.PendingCount),
			zap.Int("changed", result.ChangedCount),
			zap.Int("total_blocked", len(result.BlockedTools)))
	}

	return result, nil
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

		record.Status = storage.ToolApprovalStatusApproved
		record.ApprovedHash = record.CurrentHash
		record.HashSchemaVersion = storage.OutputSchemaHashSchemaVersion
		record.ApprovedAt = time.Now().UTC()
		record.ApprovedBy = approvedBy
		record.PreviousDescription = ""
		record.PreviousSchema = ""
		record.PreviousOutputSchema = ""

		if err := r.storageManager.SaveToolApproval(record); err != nil {
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
// transited the quarantine flow (i.e. only when QuarantineEnabled is on and
// SkipQuarantine is off on the server). The synthesized record's Status is
// "approved" so the new entry never reintroduces a tool into quarantine.
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
	switch {
	case err == nil:
		// existing record — keep its Status, just flip Disabled below.
	case errors.Is(err, storage.ErrToolApprovalNotFound):
		// First time we've seen this tool. The tool has been seen by MCP
		// (we wouldn't be toggling it otherwise), so "approved" is the
		// correct admin state — the toggle expresses user visibility
		// intent, not a quarantine decision.
		record = &storage.ToolApprovalRecord{
			ServerName: serverName,
			ToolName:   toolName,
			Status:     storage.ToolApprovalStatusApproved,
			ApprovedAt: time.Now().UTC(),
			ApprovedBy: updatedBy,
		}
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

	if err := r.storageManager.SaveToolApproval(record); err != nil {
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
	}

	return changed, nil
}

// collectKnownToolNames returns the set of tool short-names (no "server:"
// prefix) currently known for a server. Prefers the StateView snapshot
// (covers in-memory tools), falling back to the search index, and finally
// to whatever approval records already exist for the server.
func (r *Runtime) collectKnownToolNames(serverName string) ([]string, error) {
	seen := make(map[string]struct{})
	add := func(name string) {
		if name == "" {
			return
		}
		// Strip "server:" prefix if present.
		if idx := strings.Index(name, ":"); idx != -1 {
			name = name[idx+1:]
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
				add(tool.Name)
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
