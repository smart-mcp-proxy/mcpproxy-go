package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Audit finding F1 (#1046): the Activity Log and the Usage tab reported
// different totals for the same 24-hour window on the same instance, within
// seconds of each other. Both surfaces are fed from this package, so both are
// pinned here against ONE record set.
//
// The three numbers observed in the repro were 51 (Activity header), 25 (the
// "Activity over time" chart on the Usage tab) and 19 (the "Tool calls" tile
// directly above that chart). Only the middle one is "calls the user made";
// the other two were an every-record-type event count and a client-side sum of
// a lifetime-cumulative, top-N-truncated per-tool rollup.

// callParityController answers both halves of the comparison from one record
// set: the summary streams the records, and the usage snapshot is the same
// records replayed through the real Apply path.
type callParityController struct {
	baseController
	activities []*storage.ActivityRecord
	snap       *internalRuntime.UsageAggregate
}

func (m *callParityController) GetCurrentConfig() any {
	return &config.Config{APIKey: "test-key", Observability: config.DefaultObservabilityConfig()}
}

func (m *callParityController) StreamActivities(filter storage.ActivityFilter) <-chan *storage.ActivityRecord {
	ch := make(chan *storage.ActivityRecord)
	go func() {
		defer close(ch)
		for _, a := range m.activities {
			if filter.Matches(a) {
				ch <- a
			}
		}
	}()
	return ch
}

func (m *callParityController) UsageSnapshot() *internalRuntime.UsageAggregate { return m.snap }

func (m *callParityController) GetTokenSavings() (*contracts.ServerTokenMetrics, error) {
	return &contracts.ServerTokenMetrics{}, nil
}

// parityRecords is the repro's traffic in miniature: upstream dispatches,
// mcpproxy's own built-ins, management chatter, a policy block, a shed call and
// the non-call bookkeeping (quarantine auto-approvals, system start) that made
// the Activity header read more than twice the Usage tile.
func parityRecords(ts time.Time) []*storage.ActivityRecord {
	return []*storage.ActivityRecord{
		// 10 upstream successes + 3 upstream failures = 13 calls, 3 of them errors.
		{Type: storage.ActivityTypeToolCall, ServerName: "everything", ToolName: "echo", Status: storage.ActivityStatusSuccess, DurationMs: 3, Timestamp: ts},
		{Type: storage.ActivityTypeToolCall, ServerName: "everything", ToolName: "echo", Status: storage.ActivityStatusSuccess, DurationMs: 3, Timestamp: ts},
		{Type: storage.ActivityTypeToolCall, ServerName: "everything", ToolName: "echo", Status: storage.ActivityStatusSuccess, DurationMs: 4, Timestamp: ts},
		{Type: storage.ActivityTypeToolCall, ServerName: "memory", ToolName: "create_entities", Status: storage.ActivityStatusSuccess, DurationMs: 5, Timestamp: ts},
		{Type: storage.ActivityTypeToolCall, ServerName: "broken-remote", ToolName: "whatever", Status: storage.ActivityStatusError, DurationMs: 40, Timestamp: ts},
		{Type: storage.ActivityTypeToolCall, ServerName: "everything", ToolName: "doesnotexist", Status: storage.ActivityStatusError, DurationMs: 2, Timestamp: ts},
		{Type: storage.ActivityTypeToolCall, ServerName: "everything", ToolName: "doesnotexist", Status: storage.ActivityStatusError, DurationMs: 2, Timestamp: ts},
		// Discovery built-ins: rows in the log, and calls.
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "retrieve_tools", Status: storage.ActivityStatusSuccess, DurationMs: 5, Timestamp: ts},
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "retrieve_tools", Status: storage.ActivityStatusSuccess, DurationMs: 6, Timestamp: ts},
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "describe_tool", Status: storage.ActivityStatusSuccess, DurationMs: 1, Timestamp: ts},
		// Management chatter: never a call on either surface.
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "upstream_servers", Status: storage.ActivityStatusSuccess, DurationMs: 1, Timestamp: ts},
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "quarantine_security", Status: storage.ActivityStatusSuccess, DurationMs: 1, Timestamp: ts},
		// A script that ran: its sub-call counts, the wrapper does not.
		{Type: storage.ActivityTypeInternalToolCall, ToolName: "code_execution", Status: storage.ActivityStatusSuccess, DurationMs: 300, Timestamp: ts},
		{Type: storage.ActivityTypeToolCall, ServerName: "memory", ToolName: "read_graph", Status: storage.ActivityStatusSuccess, DurationMs: 7, Timestamp: ts, ParentID: "exec-1"},
		// A policy block: a red row, and a call the user made and did not get.
		{Type: storage.ActivityTypePolicyDecision, ServerName: "evil", ToolName: "exfil", Status: storage.ActivityStatusBlocked, Timestamp: ts},
		// Shed by the concurrency limiter: never executed, never a call.
		{Type: storage.ActivityTypeToolCall, ServerName: "everything", ToolName: "echo", Status: storage.ActivityStatusRejected, Timestamp: ts},
		// Bookkeeping the Activity Log shows and nobody would call a "call".
		{Type: storage.ActivityTypeSystemStart, ToolName: "", Status: storage.ActivityStatusSuccess, Timestamp: ts},
		{Type: storage.ActivityTypeToolQuarantineChange, ServerName: "everything", ToolName: "echo", Status: "tool_auto_approved", Timestamp: ts},
		{Type: storage.ActivityTypeToolQuarantineChange, ServerName: "memory", ToolName: "read_graph", Status: "tool_auto_approved", Timestamp: ts},
		{Type: storage.ActivityTypeSecurityScan, ServerName: "everything", ToolName: "echo", Status: storage.ActivityStatusSuccess, Timestamp: ts},
	}
}

// The expected call population, counted by hand from parityRecords:
//
//	7 upstream dispatches + 1 code_execution sub-call + 2 retrieve_tools
//	+ 1 describe_tool + 1 policy block = 12 calls, of which
//	3 upstream errors + the block = 4 errors.
const (
	parityCalls  = 12
	parityErrors = 4
)

func newCallParityServer(t *testing.T, ts time.Time) *Server {
	t.Helper()
	records := parityRecords(ts)
	return NewServer(&callParityController{
		activities: records,
		snap:       buildUsageSnapshot(records...),
	}, zap.NewNop().Sugar(), nil)
}

func getSummary(t *testing.T, srv *Server) contracts.ActivitySummaryResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/summary?period=24h", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Success bool                              `json:"success"`
		Data    contracts.ActivitySummaryResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.True(t, resp.Success)
	return resp.Data
}

// F1: the number the Activity Log prints as "N calls" and the number the Usage
// tab prints as "Tool calls" must be the same number over the same window.
func TestActivityAndUsageAgreeOnTheCallCount(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Minute)
	srv := newCallParityServer(t, ts)

	summary := getSummary(t, srv)
	_, usage := doUsageRequest(t, srv, "?window=24h")
	require.NotNil(t, usage)

	assert.Equal(t, parityCalls, summary.CallCount,
		"the Activity header counts the calls the user made")
	assert.EqualValues(t, parityCalls, usage.TotalCalls,
		"and the Usage tile counts the same population")
	assert.EqualValues(t, summary.CallCount, usage.TotalCalls,
		"F1: two surfaces, one 24h window, one number")

	assert.Equal(t, parityErrors, summary.CallErrorCount)
	assert.EqualValues(t, parityErrors, usage.TotalErrors)
	assert.EqualValues(t, summary.CallErrorCount, usage.TotalErrors,
		"F1: the error counts have to agree too, or the error RATE cannot")
}

// The Usage tiles have to agree with the chart printed directly beneath them.
// They used to be a client-side sum over `tools`, which is a lifetime-cumulative
// rollup of UPSTREAM tools only, truncated to top-N — so the tile disagreed with
// its own histogram on the same screen.
func TestUsageTotalsMatchTheTimelineItRenders(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Minute)
	srv := newCallParityServer(t, ts)

	_, usage := doUsageRequest(t, srv, "?window=24h")
	require.NotNil(t, usage)

	var calls, errors int64
	for _, b := range usage.Timeline {
		calls += b.Calls
		errors += b.Errors
	}
	assert.Equal(t, calls, usage.TotalCalls, "the tile is the sum of the bars")
	assert.Equal(t, errors, usage.TotalErrors)

	// And the per-tool rows are a DIFFERENT, narrower population — which is
	// exactly why summing them client-side was wrong.
	var toolCalls int64
	for _, row := range usage.Tools {
		toolCalls += row.Calls
	}
	assert.Less(t, toolCalls, usage.TotalCalls,
		"upstream-only rows undercount the calls the user made")
}

// The event total is still reported — the Activity Log genuinely has that many
// rows — but it is a different question from "how many calls", and the two must
// not be printed under the same word.
func TestSummaryKeepsEventTotalSeparateFromCallCount(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Minute)
	srv := newCallParityServer(t, ts)

	summary := getSummary(t, srv)

	assert.Equal(t, len(parityRecords(ts)), summary.TotalCount,
		"every record in the window is still an event")
	assert.Greater(t, summary.TotalCount, summary.CallCount,
		"quarantine bookkeeping, system start and management chatter are events, not calls")
}
