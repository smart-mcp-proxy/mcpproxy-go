package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Review-bound decisions of a person on skills (Spec 125, plan D10).

// StaleSkillReviewError refuses an approval whose review is out of date:
// Changed lists the skills whose current content hash differs from the one
// the reviewer saw, Unreviewed the requested skills the request carried no
// hash for. Nothing was written.
type StaleSkillReviewError struct {
	Server     string
	Changed    []string
	Unreviewed []string
}

func (e *StaleSkillReviewError) Error() string {
	var parts []string
	if len(e.Changed) > 0 {
		parts = append(parts, "changed since the review: "+strings.Join(e.Changed, ", "))
	}
	if len(e.Unreviewed) > 0 {
		parts = append(parts, "not reviewed: "+strings.Join(e.Unreviewed, ", "))
	}
	return fmt.Sprintf("skill review of server %s is out of date (%s); nothing was approved", e.Server, strings.Join(parts, "; "))
}

// newStaleSkillReviewError returns nil when nothing is stale.
func newStaleSkillReviewError(server string, changed, unreviewed []string) *StaleSkillReviewError {
	if len(changed) == 0 && len(unreviewed) == 0 {
		return nil
	}
	sort.Strings(changed)
	sort.Strings(unreviewed)
	return &StaleSkillReviewError{Server: server, Changed: changed, Unreviewed: unreviewed}
}

// SkillsUnusableError refuses an approval that names a skill which cannot be
// approved: it is unusable (a failed check, or a "dynamic" file list, which
// has no content an approval could be bound to). Nothing was written.
type SkillsUnusableError struct {
	Server string
	Skills []string
}

func (e *SkillsUnusableError) Error() string {
	return fmt.Sprintf("skills of server %s cannot be approved (unusable): %s", e.Server, strings.Join(e.Skills, ", "))
}

// ApproveSkillsReviewedAt approves the named skills (by skill path) of one
// server for their current content, bound to what the reviewer saw: expected
// maps each skill path to the content hash (sk1-…) of the review, and
// expectedIncarnation is the server identity of the review (0 binds to the
// incarnation current when the call starts). The whole request is refused,
// with nothing written, when
//   - the server was replaced (commitview.ErrServerReplaced),
//   - a named skill has no record (ErrSkillNotFound, wrapped with the path),
//   - a named skill has no expected hash or its current hash differs
//     (*StaleSkillReviewError),
//   - a named skill is unusable (*SkillsUnusableError).
//
// A skill that is already approved for its current content is left as it is
// and not counted. The blocked mark is never touched. ctx carries the actor
// for the activity rows; approvedBy is stored on the record.
func (r *Runtime) ApproveSkillsReviewedAt(ctx context.Context, serverName string, skillPaths []string, expected map[string]string, approvedBy string, expectedIncarnation int64) (int, error) {
	paths := dedupeStrings(skillPaths)
	return r.mutateSkillsAt(ctx, serverName, paths, expectedIncarnation, func(recs []*storage.SkillApprovalRecord, now time.Time) ([]skillChange, error) {
		var changed, unreviewed, unusable []string
		for _, rec := range recs {
			want, ok := expected[rec.SkillPath]
			switch {
			case !ok:
				unreviewed = append(unreviewed, rec.SkillPath)
			case rec.CurrentHash != want:
				changed = append(changed, rec.SkillPath)
			}
		}
		if err := newStaleSkillReviewError(serverName, changed, unreviewed); err != nil {
			return nil, err
		}
		for _, rec := range recs {
			if rec.Status == storage.SkillStatusUnusable || rec.CurrentHash == "" {
				unusable = append(unusable, rec.SkillPath)
			}
		}
		if len(unusable) > 0 {
			sort.Strings(unusable)
			return nil, &SkillsUnusableError{Server: serverName, Skills: unusable}
		}
		var out []skillChange
		for _, rec := range recs {
			if rec.Status == storage.SkillStatusApproved && rec.Approved != nil && rec.ApprovedHash == rec.CurrentHash {
				continue
			}
			c := *rec
			applySkillDecision(&c, skillDecision{status: storage.SkillStatusApproved, approve: true, approvedBy: approvedBy}, now)
			c.UpdatedAt = now
			if enforceSkillInvariant(&c, r.logger) {
				continue
			}
			out = append(out, skillChange{rec: &c, action: "approved", previousHash: rec.ApprovedHash, approvedBy: approvedBy})
		}
		return out, nil
	})
}

// SetSkillsBlockedAt sets (blocked true) or clears (false) the blocked mark
// on the named skills of one server and returns how many records changed.
// The mark is a person's decision: no refresh, content change or automatic
// approval clears it. Refused as a whole, with nothing written, when the
// server was replaced or a named skill has no record.
func (r *Runtime) SetSkillsBlockedAt(ctx context.Context, serverName string, skillPaths []string, blocked bool, by string, expectedIncarnation int64) (int, error) {
	paths := dedupeStrings(skillPaths)
	action := "unblocked"
	if blocked {
		action = "blocked"
	}
	return r.mutateSkillsAt(ctx, serverName, paths, expectedIncarnation, func(recs []*storage.SkillApprovalRecord, now time.Time) ([]skillChange, error) {
		var out []skillChange
		for _, rec := range recs {
			if rec.Blocked == blocked {
				continue
			}
			c := *rec
			c.Blocked = blocked
			c.UpdatedAt = now
			out = append(out, skillChange{rec: &c, action: action, previousHash: rec.ApprovedHash, reason: by})
		}
		return out, nil
	})
}

// skillChange is one record a person's decision changes.
type skillChange struct {
	rec                                      *storage.SkillApprovalRecord
	action, previousHash, reason, approvedBy string
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// mutateSkillsAt is the shared shape of a person's decision on skills:
// capture the incarnation, lock, resolve every named path to a record (any
// miss refuses the whole request), let build decide what changes (it may
// refuse too), write every change in ONE transaction, and only after the lock
// is released emit one activity row per change and one skills.changed event.
func (r *Runtime) mutateSkillsAt(ctx context.Context, serverName string, paths []string, expectedIncarnation int64,
	build func(recs []*storage.SkillApprovalRecord, now time.Time) ([]skillChange, error)) (int, error) {
	sm := r.storageManager
	if sm == nil {
		return 0, errors.New("storage not initialised")
	}
	inc, err := r.CaptureApprovalIncarnation(serverName, expectedIncarnation)
	if err != nil {
		return 0, err
	}
	var changes []skillChange
	var counts map[string]int
	err = r.WithServerApprovalLockAt(serverName, inc, func() error {
		all, err := sm.ListSkillApprovals(serverName)
		if err != nil {
			return err
		}
		sort.Slice(all, func(i, j int) bool { return all[i].SkillURI < all[j].SkillURI })
		recs := make([]*storage.SkillApprovalRecord, 0, len(paths))
		for _, p := range paths {
			rec := skillByPath(all, p)
			if rec == nil {
				return fmt.Errorf("%w: %s", ErrSkillNotFound, p)
			}
			recs = append(recs, rec)
		}
		var berr error
		if changes, berr = build(recs, time.Now()); berr != nil {
			changes = nil
			return berr
		}
		if len(changes) == 0 {
			return nil
		}
		upserts := make([]*storage.SkillApprovalRecord, len(changes))
		for i, c := range changes {
			upserts[i] = c.rec
		}
		// Records only: a refresh that is reading right now has staged blobs
		// no record references yet, and collecting here would delete them.
		if err := sm.UpdateSkillApprovals(serverName, upserts); err != nil {
			changes = nil
			return err
		}
		fresh, err := sm.ListSkillApprovals(serverName)
		if err != nil {
			return err
		}
		counts = r.skillCounts(serverName, fresh)
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, c := range changes {
		r.emitSkillQuarantineChangeCtx(ctx, serverName, c.rec.SkillPath, c.rec.SkillURI, c.action, c.previousHash, c.rec.CurrentHash, c.reason, c.approvedBy)
	}
	if len(changes) > 0 {
		r.publishSkillsChanged(serverName, counts)
	}
	return len(changes), nil
}
