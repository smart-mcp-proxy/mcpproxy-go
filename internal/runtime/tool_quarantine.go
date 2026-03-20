package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// calculateToolApprovalHash computes a stable SHA-256 hash for tool-level quarantine.
// Uses toolName + description + schemaJSON + annotationsJSON for consistent detection of changes.
// Annotations are included to detect "annotation rug-pulls" (e.g., flipping destructiveHint).
func calculateToolApprovalHash(toolName, description, schemaJSON string, annotations *config.ToolAnnotations) string {
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

// calculateLegacyToolApprovalHash computes the old hash format (without annotations).
// Used for backward compatibility: tools approved before annotation tracking can be
// silently re-approved if only the hash formula changed (not the actual content).
func calculateLegacyToolApprovalHash(toolName, description, schemaJSON string) string {
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte("|"))
	h.Write([]byte(description))
	h.Write([]byte("|"))
	h.Write([]byte(schemaJSON))
	return hex.EncodeToString(h.Sum(nil))
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

	// Auto-approve new tools when:
	// - Global quarantine is disabled, OR
	// - Server has skip_quarantine=true, OR
	// - Server is NOT quarantined (user trusts this server)
	// Changed tools are still blocked regardless (rug pull detection).
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

		// Calculate current hash (includes annotations for rug-pull detection)
		currentHash := calculateToolApprovalHash(toolName, tool.Description, schemaJSON, tool.Annotations)

		// Look up existing approval record
		existing, err := r.storageManager.GetToolApproval(serverName, toolName)

		if err != nil {
			// No existing record - this is a new tool.
			// If the server is trusted (quarantine not enforced), auto-approve immediately.
			// This prevents blocking tools on upgrade for existing trusted servers.
			if !enforceQuarantine {
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

				r.logger.Info("New tool discovered, auto-approved (server trusted)",
					zap.String("server", serverName),
					zap.String("tool", toolName))

				// Emit activity event
				r.emitToolQuarantineEvent(serverName, toolName, "tool_auto_approved", "", currentHash,
					"", tool.Description, "", schemaJSON)

				continue
			}

			// Server IS quarantined - mark tool as pending
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
				zap.String("tool", toolName))

			result.BlockedTools[toolName] = true
			result.PendingCount++

			// Emit activity event
			r.emitToolQuarantineEvent(serverName, toolName, "tool_discovered", "", currentHash,
				"", tool.Description, "", schemaJSON)

			continue
		}

		// Existing record found - check if hash matches
		if existing.ApprovedHash == currentHash {
			if existing.Status != storage.ToolApprovalStatusApproved {
				// Hash matches but status is not approved (e.g., falsely marked "changed"
				// by a previous binary with a different hash formula). Restore to approved.
				existing.Status = storage.ToolApprovalStatusApproved
				existing.PreviousDescription = ""
				existing.PreviousSchema = ""
				r.logger.Info("Tool restored to approved (hash matches after formula update)",
					zap.String("server", serverName),
					zap.String("tool", toolName))
			}
			// Update current hash/description in case they differ from storage
			if existing.CurrentHash != currentHash || existing.Status == storage.ToolApprovalStatusApproved {
				existing.CurrentHash = currentHash
				existing.CurrentDescription = tool.Description
				existing.CurrentSchema = schemaJSON
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

		if existing.ApprovedHash != "" && existing.ApprovedHash != currentHash {
			// Before marking as changed, check if this is a legacy hash migration.
			// Tools previously marked "changed" due to hash formula upgrade should be restored.
			legacyHash := calculateLegacyToolApprovalHash(toolName, tool.Description, schemaJSON)
			if existing.ApprovedHash == legacyHash {
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
					r.logger.Info("Tool approval hash migrated to include annotations (was falsely changed)",
						zap.String("server", serverName),
						zap.String("tool", toolName))
				}
				continue
			}

			// Hash differs from approved hash - tool description/schema changed (rug pull)
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
