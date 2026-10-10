package storage

import (
	"fmt"
	"sort"
	"strings"
)

// ToolApprovalApplyResult reports what an approval write actually applied
// (UX-02). Every list holds unique tool names.
type ToolApprovalApplyResult struct {
	// Approved names the records written approved by this call.
	Approved []string
	// Missing names requested tools that have no approval record.
	Missing []string
	// Skipped names records left as they were: not pending under a
	// pending-only (server-approval baseline) promotion.
	Skipped []string
}

// StaleToolReviewError rejects an approval bound to reviewed definitions
// (expected hashes) when a tool's current definition no longer matches what
// the operator reviewed, or when a held tool was never part of the review.
// Nothing is written when it is returned; the operator must fetch the review
// again (UX-02 cross-review: approval is bound to the reviewed definition).
type StaleToolReviewError struct {
	Server string
	// Changed names tools whose current definition differs from the reviewed one.
	Changed []string
	// Unreviewed names held tools that the review did not include.
	Unreviewed []string
	// AtActivation reports that the review went out of date after a server
	// approval committed but before it unquarantined the server: the server
	// was left quarantined, so none of its tools became reachable.
	AtActivation bool
}

func (e *StaleToolReviewError) Error() string {
	var parts []string
	if len(e.Changed) > 0 {
		parts = append(parts, fmt.Sprintf("definition changed since review: %s", strings.Join(e.Changed, ", ")))
	}
	if len(e.Unreviewed) > 0 {
		parts = append(parts, fmt.Sprintf("not in the review: %s", strings.Join(e.Unreviewed, ", ")))
	}
	outcome := "nothing was approved"
	if e.AtActivation {
		outcome = "the server stays quarantined"
	}
	return fmt.Sprintf("tool review for server '%s' is out of date (%s); %s — fetch the review again", e.Server, strings.Join(parts, "; "), outcome)
}

// Tools returns every stale tool name, sorted.
func (e *StaleToolReviewError) Tools() []string {
	out := append(append([]string(nil), e.Changed...), e.Unreviewed...)
	sort.Strings(out)
	return out
}
