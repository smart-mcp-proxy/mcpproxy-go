package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// calculateToolApprovalHash computes a stable SHA-256 hash for tool-level quarantine.
// Uses toolName + description + schemaJSON only.
// Annotations are intentionally EXCLUDED because:
// 1. They are metadata hints, not functional changes to the tool
// 2. They may not be stable across reconnections (some servers omit them)
// 3. Including them caused false "tool_description_changed" spam on every reconnect
// 4. This matches the upstream client's hash.ComputeToolHash approach
// The annotations parameter is kept for API compatibility but ignored.
func calculateToolApprovalHash(toolName, description, schemaJSON string, annotations *config.ToolAnnotations) string {
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte("|"))
	h.Write([]byte(description))
	h.Write([]byte("|"))
	// Normalize JSON schema to prevent key-order differences from causing
	// false "tool_description_changed" events. Parse → sort keys → serialize.
	h.Write([]byte(normalizeJSON(schemaJSON)))
	// Annotations excluded from hash — see comment above
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeJSON parses a JSON string and re-serializes with sorted keys.
// Returns the original string if parsing fails (non-JSON content).
func normalizeJSON(s string) string {
	if s == "" {
		return s
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return s // Not valid JSON, return as-is
	}
	normalized, err := json.Marshal(parsed)
	if err != nil {
		return s
	}
	return string(normalized)
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
	// BlockedTools is the set of tool names that should not be indexed (pending or changed).
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

	for _, tool := range tools {
		// Extract the bare tool name (without server prefix)
		toolName := extractToolName(tool.Name)

		// Serialize schema for hashing
		schemaJSON := tool.ParamsJSON
		if schemaJSON == "" {
			// Try to serialize from any parsed schema if available
			schemaJSON = "{}"
		}

		// Normalize JSON schema before hashing and storage to ensure stable key ordering
		schemaJSON = normalizeJSON(schemaJSON)

		// Calculate current hash (includes annotations for rug-pull detection)
		currentHash := calculateToolApprovalHash(toolName, tool.Description, schemaJSON, tool.Annotations)

		// Look up existing approval record
		existing, err := r.storageManager.GetToolApproval(serverName, toolName)

		if err != nil {
			// No existing record - this is a new tool.
			if !enforceNewTools {
				// Quarantine disabled or server has skip_quarantine - auto-approve
				now := time.Now().UTC()
				record := &storage.ToolApprovalRecord{
					ServerName:         serverName,
					ToolName:           toolName,
					CurrentHash:        currentHash,
					ApprovedHash:       currentHash,
					Status:             storage.ToolApprovalStatusApproved,
					ApprovedBy:         "auto",
					ApprovedAt:         now,
					CurrentDescription: tool.Description,
					CurrentSchema:      schemaJSON,
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
				ServerName:         serverName,
				ToolName:           toolName,
				CurrentHash:        currentHash,
				Status:             storage.ToolApprovalStatusPending,
				CurrentDescription: tool.Description,
				CurrentSchema:      schemaJSON,
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
				needsSave = true
				r.logger.Info("Tool restored to approved (hash matches after formula update)",
					zap.String("server", serverName),
					zap.String("tool", toolName))
			}
			// Update current hash/description in case they differ from storage
			if existing.CurrentHash != currentHash {
				existing.CurrentHash = currentHash
				existing.CurrentDescription = tool.Description
				existing.CurrentSchema = schemaJSON
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
			if storedDesc == "" {
				storedDesc = tool.Description
			}
			if storedSchema == "" {
				storedSchema = schemaJSON
			}
			rehashedFromStored := calculateToolApprovalHash(toolName, storedDesc, storedSchema, nil)

			// Also check legacy and with-annotations formulas
			legacyHash := calculateLegacyToolApprovalHash(toolName, tool.Description, schemaJSON)
			annotationsHash := calculateHashWithAnnotations(toolName, tool.Description, schemaJSON, tool.Annotations)

			isFormulaChange := rehashedFromStored == currentHash ||
				existing.ApprovedHash == legacyHash ||
				existing.ApprovedHash == annotationsHash

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
			if descMatch && schemaMatch {
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
				zap.Int("stored_desc_len", len(existing.CurrentDescription)),
				zap.Int("current_desc_len", len(tool.Description)),
				zap.Int("stored_schema_len", len(existing.CurrentSchema)),
				zap.Int("current_schema_len", len(schemaJSON)))

			// LAST RESORT: If description matches (most important for security),
			// auto-approve even if schema normalization differs.
			// Schema formatting differences are NOT security concerns.
			if descMatch {
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
			if existing.Status == storage.ToolApprovalStatusApproved {
				// Transitioning from approved to changed
				oldDesc = existing.CurrentDescription
				oldSchema = existing.CurrentSchema
			}

			existing.Status = storage.ToolApprovalStatusChanged
			existing.PreviousDescription = oldDesc
			existing.PreviousSchema = oldSchema
			existing.CurrentHash = currentHash
			existing.CurrentDescription = tool.Description
			existing.CurrentSchema = schemaJSON

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

	if len(result.BlockedTools) > 0 {
		r.logger.Info("Tool-level quarantine: tools blocked",
			zap.String("server", serverName),
			zap.Int("pending", result.PendingCount),
			zap.Int("changed", result.ChangedCount),
			zap.Int("total_blocked", len(result.BlockedTools)))
	}

	return result, nil
}

// ApproveTools approves specific tools for a server, updating their status to approved.
func (r *Runtime) ApproveTools(serverName string, toolNames []string, approvedBy string) error {
	if r.storageManager == nil {
		return nil
	}

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
		record.ApprovedAt = time.Now().UTC()
		record.ApprovedBy = approvedBy
		record.PreviousDescription = ""
		record.PreviousSchema = ""

		if err := r.storageManager.SaveToolApproval(record); err != nil {
			return err
		}

		r.logger.Info("Tool approved",
			zap.String("server", serverName),
			zap.String("tool", toolName),
			zap.String("approved_by", approvedBy))

		// Emit activity event
		r.emitToolQuarantineEvent(serverName, toolName, "tool_approved",
			"", record.ApprovedHash, "", record.CurrentDescription, "", record.CurrentSchema)
	}

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
