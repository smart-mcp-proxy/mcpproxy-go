package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{view: "system", want: "policy_decision,quarantine_change,server_change,system_start,system_stop,config_change,preflight,prompt_get"},
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
		{name: "explicit server kept when it disagrees", server: "notes", tool: "github:create_issue", wantServer: "notes", wantTool: "create_issue"},
		{name: "explicit server kept when it agrees", server: "github", tool: "github:create_issue", wantServer: "github", wantTool: "create_issue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotServer, gotTool := splitActivityTool(tt.server, tt.tool)
			assert.Equal(t, tt.wantServer, gotServer)
			assert.Equal(t, tt.wantTool, gotTool)
		})
	}
}

func TestActivityWatchInRange(t *testing.T) {
	from := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 26, 14, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ts   string
		from time.Time
		to   time.Time
		want bool
	}{
		{name: "no bounds always in range", ts: "2020-01-01T00:00:00Z", want: true},
		{name: "within range", ts: "2026-09-26T12:00:00Z", from: from, to: to, want: true},
		{name: "before from", ts: "2026-09-26T09:00:00Z", from: from, to: to, want: false},
		{name: "after to", ts: "2026-09-26T15:00:00Z", from: from, to: to, want: false},
		{name: "malformed timestamp passes through", ts: "not-a-time", from: from, to: to, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, activityWatchInRange(tt.ts, tt.from, tt.to))
		})
	}
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
func TestActivityFilter_ToQueryParams_ToolSplit(t *testing.T) {
	server, tool := splitActivityTool("", "github:create_issue")
	filter := &ActivityFilter{Server: server, Tool: tool}
	q := filter.ToQueryParams()
	assert.Equal(t, "github", q.Get("server"))
	assert.Equal(t, "create_issue", q.Get("tool"))
}
