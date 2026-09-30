package main

import (
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 108 FR-031 (T065): --profile / --client / --token / --client-name on
// activity list, watch, summary and export, with --agent kept as a deprecated
// alias of --token.

func resetActivityScopeFlags(t *testing.T) {
	t.Helper()
	prev := [5]string{activityProfile, activityClient, activityToken, activityClientName, activityAgent}
	activityProfile, activityClient, activityToken, activityClientName, activityAgent = "", "", "", "", ""
	t.Cleanup(func() {
		activityProfile, activityClient, activityToken, activityClientName, activityAgent = prev[0], prev[1], prev[2], prev[3], prev[4]
	})
}

func TestActivityScopeFlags_RegisteredOnAllFourCommands(t *testing.T) {
	// Building/registering on every command in one process is also the guard
	// against a duplicate --from/--to style registration panic.
	for name, cmd := range map[string]*cobra.Command{
		"list": activityListCmd, "watch": activityWatchCmd, "summary": activitySummaryCmd, "export": activityExportCmd,
	} {
		for _, flag := range []string{"profile", "client", "token", "client-name", "agent"} {
			assert.NotNil(t, cmd.Flags().Lookup(flag), "activity %s missing --%s", name, flag)
		}
		assert.True(t, cmd.Flags().Lookup("agent").Hidden, "--agent is a hidden deprecated alias (activity %s)", name)
		assert.False(t, cmd.Flags().Lookup("token").Hidden)
	}
	// Spec 109-k's flags are still registered exactly once (no double
	// registration by this PR).
	for _, name := range []string{"from", "to"} {
		assert.NotNil(t, activityListCmd.Flags().Lookup(name))
		assert.NotNil(t, activitySummaryCmd.Flags().Lookup(name))
	}
}

func TestActivityScopeFlags_ListQueryMapping(t *testing.T) {
	f := &ActivityFilter{Profile: "work-readonly", Client: "cursor", Token: "client-cursor", ClientName: "Cursor"}
	q := f.ToQueryParams()
	assert.Equal(t, "work-readonly", q.Get("profile"))
	assert.Equal(t, "cursor", q.Get("client"))
	assert.Equal(t, "client-cursor", q.Get("token"))
	assert.Equal(t, "Cursor", q.Get("client_name"))
	assert.Empty(t, q.Get("agent"), "the alias is resolved before the query is built")

	empty := (&ActivityFilter{}).ToQueryParams()
	for _, k := range []string{"profile", "client", "token", "client_name"} {
		assert.False(t, empty.Has(k), k)
	}
	assert.Equal(t, "-", (&ActivityFilter{Client: "-"}).ToQueryParams().Get("client"), "the unattributed sentinel passes through")
}

func TestActivityScopeFlags_AgentIsDeprecatedAliasOfToken(t *testing.T) {
	resetActivityScopeFlags(t)

	activityAgent = "ci-bot"
	sc, err := resolveActivityScope()
	require.NoError(t, err)
	assert.Equal(t, "ci-bot", sc.Token)
	assert.Equal(t, "--agent is deprecated; use --token", sc.Deprecation)

	activityToken = "ci-bot"
	sc, err = resolveActivityScope()
	require.NoError(t, err, "the same name twice is fine")
	assert.Equal(t, "ci-bot", sc.Token)

	activityToken = "other"
	_, err = resolveActivityScope()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must name the same token")

	activityAgent = ""
	sc, err = resolveActivityScope()
	require.NoError(t, err)
	assert.Empty(t, sc.Deprecation)
	assert.Equal(t, "other", sc.Token)
}

func TestActivityScopeFlags_ExportQueryMapping(t *testing.T) {
	resetActivityScopeFlags(t)
	prevFmt := activityExportFormat
	activityExportFormat = "csv"
	t.Cleanup(func() { activityExportFormat = prevFmt })

	activityProfile, activityClient, activityAgent, activityClientName = "p", "c", "a", "N"
	q, err := activityExportQueryParams()
	require.NoError(t, err)
	assert.Equal(t, "p", q.Get("profile"))
	assert.Equal(t, "c", q.Get("client"))
	assert.Equal(t, "a", q.Get("token"), "--agent maps to token")
	assert.Equal(t, "N", q.Get("client_name"))
	assert.Empty(t, q.Get("agent"))
}

func TestActivityScopeFlags_SummaryScopeQueryAndClientNameRejected(t *testing.T) {
	resetActivityScopeFlags(t)
	sc := activityScope{Profile: "p", Client: "c", Token: "t", ClientName: "ignored"}
	q := activityScopeQuery(sc)
	assert.Equal(t, "p", q.Get("profile"))
	assert.Equal(t, "c", q.Get("client"))
	assert.Equal(t, "t", q.Get("token"))
	assert.False(t, q.Has("client_name"), "summary never sends client_name")

	// --client-name is registered on summary and always rejected, before any
	// daemon connection, with the REST text.
	require.NoError(t, activitySummaryCmd.Flags().Set("client-name", "Cursor"))
	t.Cleanup(func() {
		_ = activitySummaryCmd.Flags().Set("client-name", "")
		activitySummaryCmd.Flags().Lookup("client-name").Changed = false
	})
	err := runActivitySummary(activitySummaryCmd, nil)
	require.Error(t, err)
	assert.Equal(t, "client_name is not supported on this endpoint; filter by client", err.Error())
}

func TestActivityScopeFlags_WatchFiltersOnAttribution(t *testing.T) {
	resetActivityScopeFlags(t)
	event := func(attr map[string]interface{}) map[string]interface{} {
		ev := map[string]interface{}{"server_name": "github", "tool_name": "list_issues", "status": "success"}
		if attr != nil {
			ev["attribution"] = attr
		}
		return ev
	}
	cursor := event(map[string]interface{}{"profile": "work-readonly", "profile_source": "pin", "client_id": "cursor", "client_name": "Cursor", "token_name": "client-cursor"})
	unattributed := event(nil)

	// No filter: everything prints.
	assert.True(t, activityWatchScopeMatches(cursor))
	assert.True(t, activityWatchScopeMatches(unattributed))

	activityClient = "cursor"
	assert.True(t, activityWatchScopeMatches(cursor))
	assert.False(t, activityWatchScopeMatches(unattributed))
	assert.False(t, activityWatchScopeMatches(event(map[string]interface{}{"client_id": "zed"})))

	activityClient = "-"
	assert.False(t, activityWatchScopeMatches(cursor), "- never matches an attributed event")
	assert.True(t, activityWatchScopeMatches(unattributed), "- matches an event with no attribution")

	activityClient = ""
	activityProfile = "work-readonly"
	assert.True(t, activityWatchScopeMatches(cursor))
	activityProfile = "work-full"
	assert.False(t, activityWatchScopeMatches(cursor))

	activityProfile = ""
	activityAgent = "client-cursor" // the alias
	assert.True(t, activityWatchScopeMatches(cursor))
	activityAgent = ""
	activityClientName = "Cursor"
	assert.True(t, activityWatchScopeMatches(cursor))
	activityClientName = "Zed"
	assert.False(t, activityWatchScopeMatches(cursor))
}

func TestActivityScopeFlags_ListTableGolden(t *testing.T) {
	acts := []map[string]interface{}{
		{
			"id": "01A", "source": "mcp", "type": "tool_call", "server_name": "github", "tool_name": "list_issues",
			"status": "success", "duration_ms": float64(12), "timestamp": "2026-09-30T10:00:00Z",
			"client_id": "cursor", "client_name": "Cursor", "profile": "work-readonly", "profile_source": "pin", "token_name": "client-cursor",
		},
		{
			"id": "01B", "source": "mcp", "type": "tool_call", "server_name": "github", "tool_name": "search_code",
			"status": "success", "duration_ms": float64(3), "timestamp": "2026-09-30T10:00:00Z",
			"client_name": "Claude Code", // self-reported only: advisory, marked with ~
		},
		{
			"id": "01C", "source": "api", "type": "tool_call", "server_name": "github", "tool_name": "x",
			"status": "success", "duration_ms": float64(1), "timestamp": "2026-09-30T10:00:00Z",
		},
	}
	headers, rows := activityListRows(acts)
	assert.Equal(t, []string{"ID", "SRC", "TYPE", "SERVER", "TOOL", "CLIENT", "PROFILE", "INTENT", "SENSITIVE", "STATUS", "DURATION", "TIME"}, headers)
	require.Len(t, rows, 3)
	assert.Equal(t, []string{"cursor", "work-readonly (pin)"}, []string{rows[0][5], rows[0][6]})
	assert.Equal(t, []string{"~Claude Code", "-"}, []string{rows[1][5], rows[1][6]})
	assert.Equal(t, []string{"-", "-"}, []string{rows[2][5], rows[2][6]})
	for _, row := range rows {
		assert.Len(t, row, len(headers))
	}
}

func TestActivityScopeFlags_HelpJSONListsTheFlags(t *testing.T) {
	for _, cmd := range []*cobra.Command{activityListCmd, activityWatchCmd, activitySummaryCmd, activityExportCmd} {
		usage := cmd.Flags().FlagUsages()
		for _, want := range []string{"--profile", "--client ", "--token", "--client-name"} {
			assert.Contains(t, usage, want, cmd.Name())
		}
		assert.NotContains(t, usage, "--agent", "the deprecated alias is hidden from help (%s)", cmd.Name())
	}
	// The activity type help names profile_change.
	assert.Contains(t, activityListCmd.Flags().Lookup("type").Usage, "profile_change")
	_, err := json.Marshal(activityListCmd.Flags().Lookup("token").Usage)
	require.NoError(t, err)
}
