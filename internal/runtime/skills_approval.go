package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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

var errSkillsApprovalNotImplemented = errors.New("skills approval is not implemented yet")

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
	return 0, errSkillsApprovalNotImplemented
}

// SetSkillsBlockedAt sets (blocked true) or clears (false) the blocked mark
// on the named skills of one server and returns how many records changed.
// The mark is a person's decision: no refresh, content change or automatic
// approval clears it. Refused as a whole, with nothing written, when the
// server was replaced or a named skill has no record.
func (r *Runtime) SetSkillsBlockedAt(ctx context.Context, serverName string, skillPaths []string, blocked bool, by string, expectedIncarnation int64) (int, error) {
	return 0, errSkillsApprovalNotImplemented
}
