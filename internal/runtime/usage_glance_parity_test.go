package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// glanceRows applies the SAME admission rule the activity list uses by default
// (storage.DefaultActivityFilter) so the two populations can be compared
// directly rather than by eye.
func glanceRows(records []*storage.ActivityRecord) int {
	filter := storage.DefaultActivityFilter()
	n := 0
	for _, rec := range records {
		if filter.Matches(rec) {
			n++
		}
	}
	return n
}

// The bars under the Recent list have to count the rows in it. They used to
// count tool_calls only, so an agent whose traffic was mostly retrieve_tools
// and code_execution — which is most agents — looked at a list of work under an
// almost empty histogram.
func TestUsageAggregate_TimelineMatchesTheGlancePopulation(t *testing.T) {
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	records := []*storage.ActivityRecord{
		// Upstream dispatches.
		{Type: storage.ActivityTypeToolCall, ServerName: "github", ToolName: "search", Status: storage.ActivityStatusSuccess, DurationMs: 12, Timestamp: ts},
		{Type: storage.ActivityTypeToolCall, ServerName: "github", ToolName: "search", Status: storage.ActivityStatusError, DurationMs: 8, Timestamp: ts},
		// mcpproxy's own built-ins: visible in the glance, and until now
		// invisible in the histogram.
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "retrieve_tools", Status: storage.ActivityStatusSuccess, DurationMs: 5, Timestamp: ts},
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "code_execution", Status: storage.ActivityStatusSuccess, DurationMs: 400, Timestamp: ts},
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
	assert.EqualValues(t, 3, timeline[0].Errors,
		"the failed dispatch, the failed built-in and the block are all failures")
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
