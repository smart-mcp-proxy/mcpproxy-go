package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// glanceRows counts the rows the TRAY GLANCE renders for these records, so the
// timeline can be compared against them directly rather than by eye.
//
// It is two stages, because the glance is NOT the activity list:
//
//	stage 1 — storage.DefaultActivityFilter, the list's own default admission.
//	  It is what the Web UI shows, and it deliberately keeps the code_execution
//	  parent row: the list is that script's drill-down home.
//	stage 2 — the glance's extra narrowing (GlanceSelection rules 1 and 3). The
//	  tray has five rows, so it drops what something else already represents: a
//	  successful code_execution (its sub-calls are tool_call records of their
//	  own), successful management chatter, and sheds that never ran.
//
// Stage 2 is spelled out literally instead of reading glanceInternalTools /
// glanceManagementBuiltins, so this stays an independent oracle rather than a
// restatement of the code under test.
func glanceRows(records []*storage.ActivityRecord) int {
	filter := storage.DefaultActivityFilter()
	rowsOnSuccess := map[string]bool{"retrieve_tools": true, "describe_tool": true}
	management := map[string]bool{"upstream_servers": true, "quarantine_security": true}

	n := 0
	for _, rec := range records {
		if !filter.Matches(rec) {
			continue
		}
		if rec.Type == storage.ActivityTypeInternalToolCall {
			if management[rec.ToolName] {
				continue // rule 1: never a row, whatever the status
			}
			if rec.Status == storage.ActivityStatusSuccess && !rowsOnSuccess[rec.ToolName] {
				continue // rule 3: on success, only the discovery built-ins row
			}
		}
		if rec.Type == storage.ActivityTypeToolCall && rec.Status == storage.ActivityStatusRejected {
			continue // spec 093: a shed call never executed
		}
		n++
	}
	return n
}

// The bars under the Recent list have to count the rows in it. They used to
// count tool_calls only, so an agent whose traffic was mostly retrieve_tools —
// which is most agents — looked at a list of work under an almost empty
// histogram.
func TestUsageAggregate_TimelineMatchesTheGlancePopulation(t *testing.T) {
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	records := []*storage.ActivityRecord{
		// Upstream dispatches.
		{Type: storage.ActivityTypeToolCall, ServerName: "github", ToolName: "search", Status: storage.ActivityStatusSuccess, DurationMs: 12, Timestamp: ts},
		{Type: storage.ActivityTypeToolCall, ServerName: "github", ToolName: "search", Status: storage.ActivityStatusError, DurationMs: 8, Timestamp: ts},
		// mcpproxy's own built-ins: visible in the glance, and until now
		// invisible in the histogram.
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "retrieve_tools", Status: storage.ActivityStatusSuccess, DurationMs: 5, Timestamp: ts},
		// A script that ran: the glance shows the calls it MADE, not the
		// wrapper, so this record is a row in neither list nor histogram.
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "code_execution", Status: storage.ActivityStatusSuccess, DurationMs: 400, Timestamp: ts},
		{Type: storage.ActivityTypeToolCall, ServerName: "jira", ToolName: "get_issue", Status: storage.ActivityStatusSuccess, DurationMs: 30, Timestamp: ts, ParentID: "exec-1"},
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "describe_tool", Status: storage.ActivityStatusError, DurationMs: 2, Timestamp: ts},
		// A policy block: a red row in the glance.
		{Type: storage.ActivityTypePolicyDecision, ServerName: "evil", ToolName: "exfil", Status: storage.ActivityStatusBlocked, Timestamp: ts},
	}

	agg := newUsageAggregate()
	for _, rec := range records {
		agg.Apply(rec)
	}

	timeline := agg.Timeline()
	require.Len(t, timeline, 1)
	assert.EqualValues(t, glanceRows(records), timeline[0].Calls,
		"every row the user sees must be a call in the histogram")
	assert.EqualValues(t, 6, timeline[0].Calls,
		"two dispatches, the script's sub-call, retrieve_tools, the failed "+
			"describe_tool and the block — but not the code_execution wrapper")
	assert.EqualValues(t, 3, timeline[0].Errors,
		"the failed dispatch, the failed built-in and the block are all failures")
}

// code_execution is an internal PRIMITIVE, not a call the user made: a script
// that ran is represented by the upstream calls it issued, which are tool_call
// records that count on their own. Counting the wrapper too would put a bar
// under a name no upstream owns and double the script.
//
// A FAILED one is the exception, and the reason is that it has no children: a
// script that died of a syntax error dispatched nothing, so its own record is
// the only trace of the attempt anywhere.
func TestUsageAggregate_CodeExecutionWrapperCountsOnlyWhenItFailed(t *testing.T) {
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	agg := newUsageAggregate()
	agg.Apply(&storage.ActivityRecord{
		Type: storage.ActivityTypeInternalToolCall, ToolName: "code_execution",
		Status: storage.ActivityStatusSuccess, DurationMs: 400, Timestamp: ts,
	})
	assert.Empty(t, agg.Timeline(), "a successful script bars through its sub-calls, not through itself")
	assert.Empty(t, agg.Tools, "and it is not an upstream tool either")

	agg.Apply(&storage.ActivityRecord{
		Type: storage.ActivityTypeInternalToolCall, ToolName: "code_execution",
		Status: storage.ActivityStatusError, DurationMs: 3, Timestamp: ts,
	})
	timeline := agg.Timeline()
	require.Len(t, timeline, 1)
	assert.EqualValues(t, 1, timeline[0].Calls, "a script that never dispatched has only its own row")
	assert.EqualValues(t, 1, timeline[0].Errors)
}

// The direct dispatch path emits BOTH a tool_call record and a call_tool_*
// internal record for one call. The glance hides the internal one; so must the
// histogram, or every direct call is counted twice.
func TestUsageAggregate_InternalCallToolRecordsAreNotDoubleCounted(t *testing.T) {
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	records := []*storage.ActivityRecord{
		{Type: storage.ActivityTypeToolCall, ServerName: "github", ToolName: "search", Status: storage.ActivityStatusSuccess, DurationMs: 12, Timestamp: ts, RequestID: "r1"},
		{Type: storage.ActivityTypeInternalToolCall, ServerName: "github", ToolName: "call_tool_read", Status: storage.ActivityStatusSuccess, DurationMs: 13, Timestamp: ts, RequestID: "r1"},
	}

	agg := newUsageAggregate()
	for _, rec := range records {
		agg.Apply(rec)
	}

	timeline := agg.Timeline()
	require.Len(t, timeline, 1)
	assert.EqualValues(t, 1, timeline[0].Calls, "one dispatch, one bar unit")
	assert.EqualValues(t, glanceRows(records), timeline[0].Calls)

	// And the mirror record must not invent a per-tool row for the variant name.
	assert.Nil(t, agg.Tools[toolKey("github", "call_tool_read")])
}

// The per-tool rollup describes UPSTREAM tools. A built-in has no upstream
// server, and its latency is mcpproxy's, not an upstream's — admitting it would
// invent tool rows and skew percentiles.
func TestUsageAggregate_BuiltinsStayOutOfThePerToolRollup(t *testing.T) {
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	agg := newUsageAggregate()
	agg.Apply(&storage.ActivityRecord{
		Type: storage.ActivityTypeInternalToolCall, ToolName: "retrieve_tools",
		Status: storage.ActivityStatusSuccess, DurationMs: 5, ResponseBytes: 900, Timestamp: ts,
	})

	assert.Empty(t, agg.Tools, "a built-in is not an upstream tool")
	require.Len(t, agg.Timeline(), 1)
	assert.EqualValues(t, 1, agg.Timeline()[0].Calls)
}

// Successful management built-ins (upstream_servers, quarantine_security, …)
// are not glance rows — GlanceSelection rule 3 admits only retrieve_tools and
// describe_tool plus any internal failure — so a config sweep must not paint
// bars over an hour with no visible rows.
func TestUsageAggregate_SuccessfulManagementBuiltinsStayOutOfTheTimeline(t *testing.T) {
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	agg := newUsageAggregate()

	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypeInternalToolCall, ToolName: "upstream_servers", Status: storage.ActivityStatusSuccess, Timestamp: ts})
	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypeInternalToolCall, ToolName: "quarantine_security", Status: storage.ActivityStatusSuccess, Timestamp: ts})
	assert.Empty(t, agg.Timeline(), "successful management calls are hidden rows, so no bars")

	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypeInternalToolCall, ToolName: "search_servers", Status: storage.ActivityStatusError, Timestamp: ts})
	timeline := agg.Timeline()
	require.Len(t, timeline, 1)
	assert.EqualValues(t, 1, timeline[0].Calls, "a non-management internal failure IS a glance row")
	assert.EqualValues(t, 1, timeline[0].Errors)
}

// A blocked tool_call (a policy gate refused a code_execution sub-call before
// dispatch) never executed: like a blocked policy_decision it takes a failed
// timeline bar and the Blocked counter, never Calls/latency/bytes.
func TestUsageAggregate_BlockedToolCallIsARefusalNotAnExecutedCall(t *testing.T) {
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	agg := newUsageAggregate()
	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypeToolCall, ServerName: "github", ToolName: "search", Status: storage.ActivityStatusBlocked, DurationMs: 3, RequestBytes: 100, Timestamp: ts})

	tu := agg.Tools[toolKey("github", "search")]
	require.NotNil(t, tu)
	assert.EqualValues(t, 0, tu.Calls)
	assert.EqualValues(t, 1, tu.Blocked)
	assert.EqualValues(t, 0, tu.ReqBytesSum)

	timeline := agg.Timeline()
	require.Len(t, timeline, 1)
	assert.EqualValues(t, 1, timeline[0].Calls)
	assert.EqualValues(t, 1, timeline[0].Errors)
}

// Management built-ins never row in the glance regardless of status
// (GlanceSelection rule 1), so even their FAILURES must not bar.
func TestUsageAggregate_ManagementBuiltinsNeverEnterTheTimeline(t *testing.T) {
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	agg := newUsageAggregate()
	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypeInternalToolCall, ToolName: "upstream_servers", Status: storage.ActivityStatusError, Timestamp: ts})
	agg.Apply(&storage.ActivityRecord{Type: storage.ActivityTypeInternalToolCall, ToolName: "quarantine_security", Status: storage.ActivityStatusError, Timestamp: ts})
	assert.Empty(t, agg.Timeline(), "rule 1 hides these rows whatever their status, so no bars")
}
