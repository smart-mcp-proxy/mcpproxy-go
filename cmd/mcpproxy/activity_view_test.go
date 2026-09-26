package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// =============================================================================
// Spec 109-k (activity-scope-filters): --view, --from/--to on the CLI.
// url-filter-contract.md owns the parameter table this file exercises.
// =============================================================================

func TestActivityViewTypeFilter(t *testing.T) {
	tests := []struct {
		view    string
		want    string
		wantErr bool
	}{
		{view: "", want: ""},
		{view: "all", want: ""},
		{view: "calls", want: "tool_call,internal_tool_call"},
		{view: "system", want: "policy_decision,quarantine_change,server_change,system_start,system_stop,config_change,tool_quarantine_change,security_scan,credential_broker,preflight,prompt_get"},
		{view: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.view, func(t *testing.T) {
			got, err := activityViewTypeFilter(tt.view)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestActivitySystemTypes_MatchesStorageValidTypesMinusCalls is a live-QA
// regression (109-k-activity-scope-filters): activitySystemTypes used to be a
// hand-maintained literal that drifted from storage.ValidActivityTypes as new
// activity types were added, silently dropping `mp activity list --view
// system` rows (tool_quarantine_change, security_scan, credential_broker were
// all missing) despite the doc comment claiming it covered "every other known
// activity type". Assert it by construction against the canonical list
// instead of pinning another hand-copied literal that can drift the same way.
func TestActivitySystemTypes_MatchesStorageValidTypesMinusCalls(t *testing.T) {
	calls := make(map[string]bool, len(activityCallTypes))
	for _, typ := range activityCallTypes {
		calls[typ] = true
	}

	var wantSystem []string
	for _, typ := range storage.ValidActivityTypes {
		if !calls[typ] {
			wantSystem = append(wantSystem, typ)
		}
	}

	assert.ElementsMatch(t, wantSystem, activitySystemTypes,
		"activitySystemTypes must be exactly storage.ValidActivityTypes minus activityCallTypes")

	// Pin the specific three the live QA found missing, so a future revert to
	// a hand-copied literal fails loudly on these names, not just on count.
	for _, typ := range []string{"tool_quarantine_change", "security_scan", "credential_broker"} {
		assert.Contains(t, activitySystemTypes, typ)
	}
}

func TestResolveActivityTime(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "empty passes through", value: "", want: ""},
		{name: "absolute RFC3339 passes through unchanged", value: "2025-01-01T00:00:00Z", want: "2025-01-01T00:00:00Z"},
		{name: "-1h", value: "-1h", want: "2026-09-26T11:00:00Z"},
		{name: "-24h", value: "-24h", want: "2026-09-25T12:00:00Z"},
		{name: "-7d", value: "-7d", want: "2026-09-19T12:00:00Z"},
		{name: "-30d", value: "-30d", want: "2026-08-27T12:00:00Z"},
		{name: "-30m", value: "-30m", want: "2026-09-26T11:30:00Z"},
		{name: "invalid shorthand", value: "-1y", wantErr: true},
		{name: "garbage", value: "yesterday", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveActivityTime(tt.value, now)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestActivityPeriodFromRelative(t *testing.T) {
	tests := []struct {
		from    string
		want    string
		wantErr bool
	}{
		{from: "-1h", want: "1h"},
		{from: "-24h", want: "24h"},
		{from: "-7d", want: "7d"},
		{from: "-30d", want: "30d"},
		{from: "-3d", wantErr: true},
		{from: "2025-01-01T00:00:00Z", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.from, func(t *testing.T) {
			got, err := activityPeriodFromRelative(tt.from)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSplitActivityTool(t *testing.T) {
	tests := []struct {
		name       string
		server     string
		tool       string
		wantServer string
		wantTool   string
	}{
		{name: "bare tool unchanged", server: "", tool: "search", wantServer: "", wantTool: "search"},
		{name: "server:tool splits", server: "", tool: "github:create_issue", wantServer: "github", wantTool: "create_issue"},
		{name: "explicit server kept when it agrees", server: "github", tool: "github:create_issue", wantServer: "github", wantTool: "create_issue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotServer, gotTool, err := splitActivityTool(tt.server, tt.tool)
			require.NoError(t, err)
			assert.Equal(t, tt.wantServer, gotServer)
			assert.Equal(t, tt.wantTool, gotTool)
		})
	}
}

// TestSplitActivityTool_ConflictingServer pins url-filter-contract.md rule 8:
// an explicit --server that disagrees with the --tool "server:tool" prefix is
// a contradiction (their intersection is empty) — CLI must exit 1 with a
// conflict error before any request, never silently keep one value and drop
// the other.
func TestSplitActivityTool_ConflictingServer(t *testing.T) {
	server, tool, err := splitActivityTool("notes", "github:create_issue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--server notes conflicts with the server in --tool github:create_issue")
	assert.Empty(t, server)
	assert.Empty(t, tool)
}

func TestActivityWatchInRange(t *testing.T) {
	from := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 26, 14, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ts   time.Time
		from time.Time
		to   time.Time
		want bool
	}{
		{name: "no bounds always in range", ts: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), want: true},
		{name: "within range", ts: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC), from: from, to: to, want: true},
		{name: "before from", ts: time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC), from: from, to: to, want: false},
		{name: "after to", ts: time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC), from: from, to: to, want: false},
		{name: "unknown (zero) timestamp passes through", ts: time.Time{}, from: from, to: to, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, activityWatchInRange(tt.ts, tt.from, tt.to))
		})
	}
}

// TestEventTimestampFromWrapper pins the zcode round-1 fix (F1): the SSE
// envelope's "timestamp" is a Unix-seconds JSON number
// (internal/httpapi/server.go: time.Now().Unix()/evt.Timestamp.Unix()), never
// an RFC3339 string. Reading it with the wrong shape used to make the
// --from/--to filter a silent no-op against a real daemon.
func TestEventTimestampFromWrapper(t *testing.T) {
	got := eventTimestampFromWrapper(map[string]interface{}{"timestamp": float64(1_790_000_000)})
	assert.Equal(t, time.Unix(1_790_000_000, 0).UTC(), got)

	assert.True(t, eventTimestampFromWrapper(map[string]interface{}{}).IsZero(), "missing timestamp")
	assert.True(t, eventTimestampFromWrapper(map[string]interface{}{"timestamp": "2026-01-01T00:00:00Z"}).IsZero(),
		"a string timestamp (wrong shape) must not be misread as valid")
	assert.True(t, eventTimestampFromWrapper(map[string]interface{}{"timestamp": float64(0)}).IsZero())
}

// TestResolveActivityTime_RejectsAbsurdRelativeAmount pins the zcode round-1
// fix (F4): an unbounded relative amount could overflow the int64 duration
// multiplication and resolve to a bogus (even future) timestamp instead of
// failing loudly.
func TestResolveActivityTime_RejectsAbsurdRelativeAmount(t *testing.T) {
	_, err := resolveActivityTime("-999999999d", time.Now())
	require.Error(t, err)

	_, err = resolveActivityTime("-0h", time.Now())
	require.Error(t, err, "zero is not a meaningful relative amount")
}

func TestActivityWatchShouldExit(t *testing.T) {
	to := time.Date(2026, 9, 26, 14, 0, 0, 0, time.UTC)

	assert.False(t, activityWatchShouldExit(time.Time{}, to), "no --to means never exit on time")
	assert.False(t, activityWatchShouldExit(to, to.Add(-time.Minute)), "before --to: keep watching")
	assert.True(t, activityWatchShouldExit(to, to.Add(time.Minute)), "after --to: stop")
}

// TestActivityListCmd_ViewFromToFlags asserts the --view/--from/--to flags
// exist on 'activity list' (Spec 109-k T121).
func TestActivityListCmd_ViewFromToFlags(t *testing.T) {
	flags := []string{"view", "from", "to"}
	for _, name := range flags {
		f := activityListCmd.Flags().Lookup(name)
		require.NotNilf(t, f, "activity list missing --%s flag", name)
	}
}

// TestActivityWatchCmd_ViewFromToFlags asserts the --view/--from/--to flags
// exist on 'activity watch' (Spec 109-k T121).
func TestActivityWatchCmd_ViewFromToFlags(t *testing.T) {
	flags := []string{"view", "from", "to"}
	for _, name := range flags {
		f := activityWatchCmd.Flags().Lookup(name)
		require.NotNilf(t, f, "activity watch missing --%s flag", name)
	}
}

// TestActivitySummaryCmd_FromToFlags asserts --from/--to exist on
// 'activity summary' (Spec 109-k T121, FR-075); summary has no --view.
func TestActivitySummaryCmd_FromToFlags(t *testing.T) {
	for _, name := range []string{"from", "to"} {
		f := activitySummaryCmd.Flags().Lookup(name)
		require.NotNilf(t, f, "activity summary missing --%s flag", name)
	}
	assert.Nil(t, activitySummaryCmd.Flags().Lookup("view"), "activity summary has no --view (FR-075)")
}

// TestActivityExportCmd_ViewFromToFlags asserts --view/--from/--to exist on
// 'activity export' (Spec 109-k T121).
func TestActivityExportCmd_ViewFromToFlags(t *testing.T) {
	for _, name := range []string{"view", "from", "to"} {
		f := activityExportCmd.Flags().Lookup(name)
		require.NotNilf(t, f, "activity export missing --%s flag", name)
	}
}

// TestActivityFilter_ToQueryParams_ToolSplit asserts the tool-split rule
// (url-filter-contract.md) reaches the actual REST query when a caller sets
// Tool to a "server:tool" value directly on the filter (covers callers that
// bypass the CLI flag-splitting helper, e.g. --tool passed with no --server).
// TestActivityExportQueryParams_ConflictingServerTool asserts the export
// command surfaces the same --server/--tool conflict error (rule 8) as
// 'activity list', and does so before building any request URL.
func TestActivityExportQueryParams_ConflictingServerTool(t *testing.T) {
	prevServer, prevTool, prevFormat := activityServer, activityTool, activityExportFormat
	t.Cleanup(func() {
		activityServer, activityTool, activityExportFormat = prevServer, prevTool, prevFormat
	})

	activityExportFormat = "json"
	activityServer = "notes"
	activityTool = "github:create_issue"

	_, err := activityExportQueryParams()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--server notes conflicts with the server in --tool github:create_issue")
}

func TestActivityFilter_ToQueryParams_ToolSplit(t *testing.T) {
	server, tool, err := splitActivityTool("", "github:create_issue")
	require.NoError(t, err)
	filter := &ActivityFilter{Server: server, Tool: tool}
	q := filter.ToQueryParams()
	assert.Equal(t, "github", q.Get("server"))
	assert.Equal(t, "create_issue", q.Get("tool"))
}
