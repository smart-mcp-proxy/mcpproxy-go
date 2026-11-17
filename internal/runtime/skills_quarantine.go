package runtime

import (
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 (skills over MCP), PR-2, plan D8: the one rule that decides a skill
// record's status after a refresh (FR-013).

// skillScanState is what the scan of the current content says: clean means a
// "clean" verdict with zero findings, complete means every byte was covered.
type skillScanState struct{ clean, complete bool }

// Activity actions of an automatic transition (decision.action).
const (
	skillActionAutoApproved = "auto_approved"
	skillActionHeldChanged  = "held_changed"
	skillActionRestored     = "restored"
	skillActionUnusable     = "unusable"
)

// skillDecision is the outcome of decideSkill.
type skillDecision struct {
	status     string // storage.SkillStatus*
	approve    bool   // set Approved = Current, ApprovedHash = CurrentHash, ApprovedAt, ApprovedBy
	approvedBy string // "auto" or "scan" when approve is set
	action     string // "" or one of the skillAction* values
}

// decideSkill is the whole status rule. It never reads or writes Blocked.
// An unknown trust mode is treated as manual.
func decideSkill(prev *storage.SkillApprovalRecord, usable bool, currentHash string, mode config.TrustMode, quarantineOn bool, scan skillScanState) skillDecision {
	prevStatus := ""
	if prev != nil {
		prevStatus = prev.Status
	}
	// 1. Not usable: the approved copy on the record stays untouched.
	if !usable {
		d := skillDecision{status: storage.SkillStatusUnusable}
		if prev == nil || prevStatus != storage.SkillStatusUnusable {
			d.action = skillActionUnusable
		}
		return d
	}
	hasApproved := prev != nil && prev.Approved != nil && prev.ApprovedHash != ""
	// 2. Content equals the approved copy.
	if hasApproved && currentHash == prev.ApprovedHash {
		d := skillDecision{status: storage.SkillStatusApproved}
		if prevStatus == storage.SkillStatusChanged || prevStatus == storage.SkillStatusUnusable {
			d.action = skillActionRestored
		}
		return d
	}
	// 3. Trust everything.
	if mode == config.TrustModeAuto || !quarantineOn {
		return skillDecision{status: storage.SkillStatusApproved, approve: true, approvedBy: "auto", action: skillActionAutoApproved}
	}
	// 4. A new skill.
	if !hasApproved {
		if mode == config.TrustModeScan && scan.clean && scan.complete {
			return skillDecision{status: storage.SkillStatusApproved, approve: true, approvedBy: "scan", action: skillActionAutoApproved}
		}
		return skillDecision{status: storage.SkillStatusPending}
	}
	// 5. A changed skill is held in manual and scan mode, whatever the scan says.
	d := skillDecision{status: storage.SkillStatusChanged}
	if prevStatus == storage.SkillStatusApproved {
		d.action = skillActionHeldChanged
	}
	return d
}

// enforceSkillInvariant is the guard where a decision becomes a record: a
// record may only be written as approved when it carries an approved copy
// whose hash equals the current hash. Anything else is demoted (changed when
// an approved copy exists, pending otherwise) and logged at error level. It
// reports whether it had to demote.
func enforceSkillInvariant(rec *storage.SkillApprovalRecord, logger *zap.Logger) bool {
	if rec.Status != storage.SkillStatusApproved {
		return false
	}
	if rec.Approved != nil && rec.ApprovedHash != "" && rec.ApprovedHash == rec.CurrentHash && rec.CurrentHash != "" {
		return false
	}
	from := rec.Status
	if rec.Approved != nil && rec.ApprovedHash != "" {
		rec.Status = storage.SkillStatusChanged
	} else {
		rec.Status = storage.SkillStatusPending
	}
	if logger != nil {
		logger.Error("Refused an illegal skill promotion to approved",
			zap.String("server", rec.ServerName), zap.String("skill", rec.SkillURI),
			zap.String("from", from), zap.String("to", rec.Status),
			zap.String("current_hash", rec.CurrentHash), zap.String("approved_hash", rec.ApprovedHash))
	}
	return true
}

// applySkillDecision writes a decision onto rec (already merged with its
// previous state). Blocked is untouched.
func applySkillDecision(rec *storage.SkillApprovalRecord, d skillDecision, now time.Time) {
	rec.Status = d.status
	if d.approve {
		cp := rec.Current
		cp.Files = append([]storage.SkillFile(nil), rec.Current.Files...)
		cp.Frontmatter = append([]byte(nil), rec.Current.Frontmatter...)
		rec.Approved = &cp
		rec.ApprovedHash = rec.CurrentHash
		rec.ApprovedAt = now
		rec.ApprovedBy = d.approvedBy
	}
}
