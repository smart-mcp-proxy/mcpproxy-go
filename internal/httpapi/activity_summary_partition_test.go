package httpapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Audit finding F2 (#1046): the Activity Log's own filter tiles did not sum.
// The row read `Total (24h) 42 · Success 15 · Errors 4 · Blocked 0 · Rejected 0`
// — four buckets adding to 19 under a denominator of 42 — because the status
// field is a closed vocabulary for TOOL CALLS only, while the activity log is
// wider than tool calls: a quarantine change stores its action there, a policy
// decision its verdict. Those rows were in the total and in no bucket.
//
// The response now carries the residual as `other_count`, which makes the five
// tiles a partition of the total BY CONSTRUCTION. This test is the thing that
// keeps it one: a new status value, or a new record type that stores something
// unexpected in Status, has to land in a bucket or fail here.
func TestActivitySummaryBucketsPartitionTheTotal(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Minute)
	srv := newCallParityServer(t, ts)

	summary := getSummary(t, srv)

	sum := summary.SuccessCount + summary.ErrorCount + summary.BlockedCount +
		summary.RejectedCount + summary.OtherCount
	assert.Equal(t, summary.TotalCount, sum,
		"F2: success + error + blocked + rejected + other must equal the total the tiles sit under")

	// And the residual is not a dumping ground that quietly swallows the log:
	// exactly the two tool_auto_approved quarantine rows of the fixture belong
	// to it. If a real status leaks in here, this number moves.
	assert.Equal(t, 2, summary.OtherCount,
		"only the quarantine auto-approvals fall outside the tool-call status vocabulary")

	// The call population is a DIFFERENT cut of the same records and must not be
	// confused with the status partition — that conflation is F1/F24.
	assert.Less(t, summary.CallCount, summary.TotalCount,
		"the fixture contains events that are not calls")
}

// A percentile that hit the unbounded overflow bucket is a FLOOR, not a bound,
// so two rows sitting on the last histogram bound are not equally slow. "Sort
// by p95 latency" exists to surface the slowest tools and the response is
// truncated to top-N, so losing that tie-break can drop the genuinely slow tool
// off the chart in favour of one that merely touched the ceiling.
func TestUsageP95SortPutsOverflowFirstOnATie(t *testing.T) {
	rows := []contracts.UsageToolStat{
		{Server: "a", Tool: "bounded", P95Ms: 10000, P95Exceeds: false},
		{Server: "b", Tool: "overflowing", P95Ms: 10000, P95Exceeds: true},
		{Server: "c", Tool: "fast", P95Ms: 5, P95Exceeds: false},
	}
	sortUsageRows(rows, "p95")

	assert.Equal(t, "overflowing", rows[0].Tool, "past the last bound outranks sitting on it")
	assert.Equal(t, "bounded", rows[1].Tool)
	assert.Equal(t, "fast", rows[2].Tool)
}
