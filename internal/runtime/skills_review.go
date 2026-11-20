package runtime

import (
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 T031/T045: held skills in the review queue and the attention list.
// These are administrator-only entries: the REST layer (skillVisibleTo in
// internal/httpapi) removes them for every scoped caller.

// ReviewKindSkillReview is the review-queue kind of a server whose only owed
// review is held skills (its tools are settled and it is not quarantined).
const ReviewKindSkillReview = "skill_review"

// heldSkillCount counts the records owed a review (pending or changed, not
// blocked, listed), whatever the configuration. The caller decides whether
// the server's skills are read.
func heldSkillCount(recs []*storage.SkillApprovalRecord) int {
	n := 0
	for _, rec := range recs {
		if rec != nil && !rec.Blocked && rec.Listed &&
			(rec.Status == storage.SkillStatusPending || rec.Status == storage.SkillStatusChanged) {
			n++
		}
	}
	return n
}

// heldSkills counts the skills of a server that are owed a review: status
// pending or changed, not blocked, listed, on a server whose skills are read
// (the feature on, the server enabled, not quarantined and not excluded; plan
// D6, the rule skillsServerExposed shares with availability). since is when
// the oldest of them started waiting: first seen for a pending skill, the last
// record write for a changed one (records keep no separate change stamp).
func (r *Runtime) heldSkills(cfg *config.Config, sc *config.ServerConfig) (pending, changed int, since time.Time, err error) {
	if r.storageManager == nil || !skillsServerExposed(cfg, sc) {
		return 0, 0, time.Time{}, nil
	}
	recs, err := r.storageManager.ListSkillApprovals(sc.Name)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	for _, rec := range recs {
		if rec == nil || rec.Blocked || !rec.Listed {
			continue
		}
		var at time.Time
		switch rec.Status {
		case storage.SkillStatusPending:
			pending++
			at = rec.FirstSeen
		case storage.SkillStatusChanged:
			changed++
			at = rec.UpdatedAt
		default:
			continue
		}
		if !at.IsZero() && (since.IsZero() || at.Before(since)) {
			since = at
		}
	}
	return pending, changed, since, nil
}
