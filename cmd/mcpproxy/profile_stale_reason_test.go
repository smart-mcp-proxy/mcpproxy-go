package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A filtered response lists only the filtered rows, but the server computed
// every stale classification over the whole tool set: the note must come from
// its stale_classification_reasons, not from the rows the CLI happens to see.
func TestProfileShow_EffectiveStaleNoteWithServerFilter(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles/work-ro2/effective-tools": okResp(`{"profile":"work-ro2",
"tools":[{"server":"notion","tool":"update_page","intrinsic_tier":"write","profile_tier":"write","access":{"visible":false,"callable":false,"reason":"above_tier_cap"},"classification_stale":false}],
"counts":{"visible":0,"hidden":1,"callable":0,"by_reason":{"above_tier_cap":1}},
"stale_classifications":["github:gone_tool","github:list_issues"],
"stale_classification_reasons":{"github:gone_tool":"missing","github:list_issues":"annotated"}}`)})
	out, _, err := runCLI(t, GetProfileCommand, "table", "show", "work-ro2", "--effective", "--server", "notion")
	require.NoError(t, err)
	require.Contains(t, out, "github:list_issues: classification ignored — tool is now annotated")
	require.Contains(t, out, "github:gone_tool: classification ignored — tool not found")
	require.NotContains(t, out, "github:list_issues: classification ignored — tool not found")
}

// An older daemon sends no reasons map. Unfiltered, the listed rows are the
// whole tool set and the old heuristic is exact; filtered, the reason cannot
// be told, so the note stays neutral instead of claiming "not found".
func TestProfileShow_EffectiveStaleNoteOldDaemonFilteredFallsBackToNeutralNote(t *testing.T) {
	const body = `{"profile":"work-ro2",
"tools":[{"server":"notion","tool":"update_page","intrinsic_tier":"write","profile_tier":"write","access":{"visible":false,"callable":false,"reason":"above_tier_cap"},"classification_stale":false}],
"counts":{"visible":0,"hidden":1,"callable":0},
"stale_classifications":["github:list_issues"]}`
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles/work-ro2/effective-tools": okResp(body)})

	out, _, err := runCLI(t, GetProfileCommand, "table", "show", "work-ro2", "--effective", "--server", "notion")
	require.NoError(t, err)
	require.Contains(t, out, "github:list_issues: classification ignored\n")
	require.NotContains(t, out, "tool not found")

	out, _, err = runCLI(t, GetProfileCommand, "table", "show", "work-ro2", "--effective")
	require.NoError(t, err)
	require.Contains(t, out, "github:list_issues: classification ignored — tool not found",
		"unfiltered, the missing-row heuristic is exact")
}
