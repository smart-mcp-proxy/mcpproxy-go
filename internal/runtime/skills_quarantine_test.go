package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func approvedPrev(status string) *storage.SkillApprovalRecord {
	return &storage.SkillApprovalRecord{
		Status: status, ApprovedHash: "sk1-old", CurrentHash: "sk1-old",
		Approved: &storage.SkillContent{},
	}
}

func TestDecideSkill_Table(t *testing.T) {
	cleanScan := skillScanState{clean: true, complete: true}
	dirtyScan := skillScanState{clean: false, complete: true}
	partial := skillScanState{clean: true, complete: false}
	modes := []config.TrustMode{config.TrustModeAuto, config.TrustModeScan, config.TrustModeManual, config.TrustMode("bogus")}

	for _, q := range []bool{true, false} {
		for _, mode := range modes {
			eff := mode
			if mode != config.TrustModeAuto && mode != config.TrustModeScan {
				eff = config.TrustModeManual
			}
			trustAll := eff == config.TrustModeAuto || !q

			// NEW skill (no prev, and a pending one).
			for _, prev := range []*storage.SkillApprovalRecord{nil, {Status: storage.SkillStatusPending}} {
				for name, sc := range map[string]skillScanState{"clean": cleanScan, "dirty": dirtyScan, "partial": partial} {
					d := decideSkill(prev, true, "sk1-new", mode, q, sc)
					switch {
					case trustAll:
						assert.Equal(t, skillDecision{storage.SkillStatusApproved, true, "auto", skillActionAutoApproved}, d, "new %v %v %s", q, mode, name)
					case eff == config.TrustModeScan && name == "clean":
						assert.Equal(t, skillDecision{storage.SkillStatusApproved, true, "scan", skillActionAutoApproved}, d)
					default:
						assert.Equal(t, skillDecision{status: storage.SkillStatusPending}, d, "new %v %v %s", q, mode, name)
					}
				}
			}

			// CHANGED skill: has an approved copy, hash differs.
			for name, sc := range map[string]skillScanState{"clean": cleanScan, "dirty": dirtyScan, "partial": partial} {
				d := decideSkill(approvedPrev(storage.SkillStatusApproved), true, "sk1-new", mode, q, sc)
				if trustAll {
					assert.Equal(t, skillDecision{storage.SkillStatusApproved, true, "auto", skillActionAutoApproved}, d)
				} else {
					assert.Equal(t, skillDecision{status: storage.SkillStatusChanged, action: skillActionHeldChanged}, d, "changed %v %v %s", q, mode, name)
				}
			}
			// Already held with this very content: nothing new to record.
			held := approvedPrev(storage.SkillStatusChanged)
			held.CurrentHash = "sk1-new"
			d := decideSkill(held, true, "sk1-new", mode, q, cleanScan)
			if !trustAll {
				assert.Equal(t, skillDecision{status: storage.SkillStatusChanged}, d)
			}
			// Held, and the content changed again: the new content is held, and recorded.
			held.CurrentHash = "sk1-between"
			d = decideSkill(held, true, "sk1-new", mode, q, cleanScan)
			if !trustAll {
				assert.Equal(t, skillDecision{status: storage.SkillStatusChanged, action: skillActionHeldChanged}, d)
			}
			// Back from unusable with other content than the approved one: held, and recorded.
			d = decideSkill(approvedPrev(storage.SkillStatusUnusable), true, "sk1-new", mode, q, cleanScan)
			if !trustAll {
				assert.Equal(t, skillDecision{status: storage.SkillStatusChanged, action: skillActionHeldChanged}, d)
			}

			// SAME as approved.
			d = decideSkill(approvedPrev(storage.SkillStatusApproved), true, "sk1-old", mode, q, dirtyScan)
			assert.Equal(t, skillDecision{status: storage.SkillStatusApproved}, d)
			d = decideSkill(approvedPrev(storage.SkillStatusChanged), true, "sk1-old", mode, q, dirtyScan)
			assert.Equal(t, skillDecision{status: storage.SkillStatusApproved, action: skillActionRestored}, d)
			d = decideSkill(approvedPrev(storage.SkillStatusUnusable), true, "sk1-old", mode, q, dirtyScan)
			assert.Equal(t, skillDecision{status: storage.SkillStatusApproved, action: skillActionRestored}, d)

			// Unusable in every mode.
			d = decideSkill(nil, false, "", mode, q, cleanScan)
			assert.Equal(t, skillDecision{status: storage.SkillStatusUnusable, action: skillActionUnusable}, d)
			d = decideSkill(approvedPrev(storage.SkillStatusApproved), false, "", mode, q, cleanScan)
			assert.Equal(t, skillDecision{status: storage.SkillStatusUnusable, action: skillActionUnusable}, d)
			d = decideSkill(approvedPrev(storage.SkillStatusUnusable), false, "", mode, q, cleanScan)
			assert.Equal(t, skillDecision{status: storage.SkillStatusUnusable}, d)
		}
	}
}

func TestEnforceSkillInvariant(t *testing.T) {
	ok := &storage.SkillApprovalRecord{Status: storage.SkillStatusApproved, CurrentHash: "h", ApprovedHash: "h", Approved: &storage.SkillContent{}}
	assert.False(t, enforceSkillInvariant(ok, nil))
	assert.Equal(t, storage.SkillStatusApproved, ok.Status)

	bad := &storage.SkillApprovalRecord{Status: storage.SkillStatusApproved, CurrentHash: "new", ApprovedHash: "old", Approved: &storage.SkillContent{}}
	require.True(t, enforceSkillInvariant(bad, nil))
	assert.Equal(t, storage.SkillStatusChanged, bad.Status)

	none := &storage.SkillApprovalRecord{Status: storage.SkillStatusApproved, CurrentHash: "new"}
	require.True(t, enforceSkillInvariant(none, nil))
	assert.Equal(t, storage.SkillStatusPending, none.Status)
}

func TestApplySkillDecision_KeepsBlocked(t *testing.T) {
	rec := &storage.SkillApprovalRecord{Blocked: true, CurrentHash: "h", Current: storage.SkillContent{Files: []storage.SkillFile{{RelPath: "SKILL.md"}}}}
	applySkillDecision(rec, skillDecision{status: storage.SkillStatusApproved, approve: true, approvedBy: "auto"}, time.Now())
	assert.True(t, rec.Blocked)
	assert.Equal(t, "h", rec.ApprovedHash)
	require.NotNil(t, rec.Approved)
	assert.Equal(t, "auto", rec.ApprovedBy)
	assert.False(t, enforceSkillInvariant(rec, nil))
}
