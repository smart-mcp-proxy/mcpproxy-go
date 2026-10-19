package toolreview

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrorCodeReviewOutOfDate is the stable error_code of the HTTP 409 an
// approval bound to an out-of-date review answers (Spec 117 FR-008).
const ErrorCodeReviewOutOfDate = "review_out_of_date"

// ErrorCodeReviewNotRecorded is the stable error_code of the HTTP 503 a
// transition answers while the server's last capture could not be fully
// recorded (Spec 117 FR-018).
const ErrorCodeReviewNotRecorded = "review_not_recorded"

// ErrReviewNotRecorded reports that a server's latest tool capture could not
// be fully recorded (a storage or index write failed part-way through the
// pass), so the stored definitions may be older than what the upstream now
// serves (Spec 117 FR-018). While a server is in this state its tools are not
// dispatched and nothing is approved or enabled for it; the next complete
// capture clears it. HTTP maps it to 503, MCP to a tool error with this text.
var ErrReviewNotRecorded = errors.New("tool definitions could not be recorded; retry the capture")

// StaleToolReviewError reports that a review-bound approval (Spec 117 PR-2,
// issue #1566) no longer matches the tool definitions the core holds: a tool
// the approval would make reachable changed (definition or safety hints)
// since the review the client sent, appeared after it, or a block target no
// longer exists. Nothing was approved.
//
// Error() always contains "out of date" and never "dangerous": the macOS tray
// (#1572) and the Web review screen reload the review on "out of date" and key
// their force-approval dialog on "dangerous".
type StaleToolReviewError struct {
	Server string
	// Changed: the review fingerprint differs from the reviewed one.
	Changed []string
	// Unreviewed: in the validated set but absent from expected_hashes (or
	// sent with an empty value).
	Unreviewed []string
	// MissingBlocks: block targets that no longer have an approval record
	// (bound requests only).
	MissingBlocks []string
}

// NewStaleToolReviewError returns the error with every list sorted and
// deduplicated, or nil when all lists are empty.
func NewStaleToolReviewError(server string, changed, unreviewed, missingBlocks []string) *StaleToolReviewError {
	e := &StaleToolReviewError{
		Server:        server,
		Changed:       sortedUnique(changed),
		Unreviewed:    sortedUnique(unreviewed),
		MissingBlocks: sortedUnique(missingBlocks),
	}
	if len(e.Changed)+len(e.Unreviewed)+len(e.MissingBlocks) == 0 {
		return nil
	}
	return e
}

func (e *StaleToolReviewError) Error() string {
	var parts []string
	if len(e.Changed) > 0 {
		parts = append(parts, "definition changed since review: "+strings.Join(e.Changed, ", "))
	}
	if len(e.Unreviewed) > 0 {
		parts = append(parts, "not in the review: "+strings.Join(e.Unreviewed, ", "))
	}
	if len(e.MissingBlocks) > 0 {
		parts = append(parts, "block target no longer exists: "+strings.Join(e.MissingBlocks, ", "))
	}
	return fmt.Sprintf("tool review for server '%s' is out of date (%s); nothing was approved — fetch the review again", e.Server, strings.Join(parts, "; "))
}

func sortedUnique(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
