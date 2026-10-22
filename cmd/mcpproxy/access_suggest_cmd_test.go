package main

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// `mcpproxy access suggest` (#1700): the query it sends, what it prints, and
// that it never applies anything.

const suggestResponse = `{
 "since":"2026-10-19T12:00:00Z","until":"2026-10-20T12:00:00Z","window":"24h0m0s",
 "blocked_calls":9,"already_fixed":1,"profile_removed":0,"non_profile_blocked":2,"unattributed":0,
 "suggestions":[
  {"profile":"research","tool":"github:create_issue","server":"github","block_reason":"profile_tier","count":5,
   "tokens":["ci-bot","dev-bot"],"clients":[],"first_seen":"2026-10-20T08:00:00Z","last_seen":"2026-10-20T11:00:00Z",
   "suggestion":"allow","fixes":[{"step":"tier_cap","action":"allow_in_profile","target":"research","label":"Allow github:create_issue in research"}]},
  {"profile":"research","tool":"github:search_code","server":"github","block_reason":"profile_unannotated","count":2,
   "tokens":["ci-bot"],"clients":[],"first_seen":"2026-10-20T08:00:00Z","last_seen":"2026-10-20T09:00:00Z",
   "suggestion":"classify","needs_input":["tier"],"note":"The tool declares no tier. Choose read, write or destructive; no tier is assumed.",
   "fixes":[{"step":"tier_cap","action":"classify_in_profile","target":"research","label":"Classify github:search_code in research"}]},
  {"profile":"research","tool":"github:delete_repo","server":"github","block_reason":"profile_rule","count":1,
   "tokens":["ci-bot"],"clients":[],"first_seen":"2026-10-20T08:00:00Z","last_seen":"2026-10-20T08:00:00Z",
   "suggestion":"none","note":"Denied by an explicit rule in the profile. Edit tools.deny if this access is intended.","fixes":[]},
  {"profile":"research","tool":"code_execution","block_reason":"profile_code_execution","count":1,
   "tokens":["ci-bot"],"clients":[],"first_seen":"2026-10-20T08:00:00Z","last_seen":"2026-10-20T08:00:00Z",
   "suggestion":"none","warning":"Setting code_execution on profile research lets scripts call every upstream tool the profile can reach.","fixes":[]}
 ],
 "patches":[{"profile":"research","changes":["tools.allow += github:create_issue"],
   "proposed":{"name":"research","servers":["github"],"max_tier":"read","tools":{"allow":["github:create_issue"]}}}]
}`

func TestAccessSuggest_DefaultQuery(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/suggest": okResp(suggestResponse)})
	_, _, err := runCLI(t, GetAccessCommand, "table", "suggest")
	require.NoError(t, err)
	q, _ := url.ParseQuery(rec.only(t).RawQuery)
	require.Equal(t, url.Values{"since": {"24h"}}, q, "the default window is 24h and no patch is asked for")
}

func TestAccessSuggest_FlagsMapToTheQuery(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/suggest": okResp(suggestResponse)})
	_, _, err := runCLI(t, GetAccessCommand, "table", "suggest", "--since", "7d", "--profile", "research", "--token", "ci-bot", "--patch")
	require.NoError(t, err)
	q, _ := url.ParseQuery(rec.only(t).RawQuery)
	require.Equal(t, url.Values{"since": {"7d"}, "profile": {"research"}, "token": {"ci-bot"}, "patch": {"true"}}, q)
}

func TestAccessSuggest_TableShowsCommandsNotesAndWarnings(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/suggest": okResp(suggestResponse)})
	out, _, err := runCLI(t, GetAccessCommand, "table", "suggest")
	require.NoError(t, err)

	last := time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC).Local().Format("2006-01-02 15:04")
	require.Contains(t, out, "Profile refusals in the last 24h0m0s: 9")
	require.Contains(t, out, "github:create_issue")
	require.Contains(t, out, "ci-bot,dev-bot")
	require.Contains(t, out, last)
	require.Contains(t, out, "mcpproxy profile update research --add-allow github:create_issue")
	// A tier is never filled in for the operator.
	require.Contains(t, out, "mcpproxy profile classify research github:search_code <read|write|destructive>")
	require.Contains(t, out, "Denied by an explicit rule")
	require.NotContains(t, out, "--add-allow github:delete_repo", "a deny rule is never offered for widening")
	require.Contains(t, out, "WARNING: Setting code_execution on profile research")
	require.Contains(t, out, "Not listed: 1 group(s) already reachable; 2 blocked call(s) not caused by a profile")
	require.NotContains(t, out, "Proposed profile", "the patch is opt-in")
}

func TestAccessSuggest_PatchPrintsTheProposedProfileDocument(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/suggest": okResp(suggestResponse)})
	out, _, err := runCLI(t, GetAccessCommand, "table", "suggest", "--patch")
	require.NoError(t, err)
	require.Contains(t, out, "Proposed profile research (tools.allow += github:create_issue):")
	require.Contains(t, out, `"allow": [`)
	require.Contains(t, out, `"max_tier": "read"`)
}

func TestAccessSuggest_JSONIsData(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/suggest": okResp(suggestResponse)})
	out, _, err := runCLI(t, GetAccessCommand, "json", "suggest")
	require.NoError(t, err)
	require.Equal(t, indented(t, suggestResponse), out)
}

func TestAccessSuggest_NothingToSuggest(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/suggest": okResp(`{"window":"24h0m0s","suggestions":[],"blocked_calls":0}`)})
	out, _, err := runCLI(t, GetAccessCommand, "table", "suggest")
	require.NoError(t, err)
	require.Contains(t, out, "Nothing to suggest.")
}

func TestAccessSuggest_RefusalIsExit1(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/suggest": refuse(400, `{"success":false,"error":"invalid since \"soon\": use a duration such as 90m, 24h or 7d"}`)})
	_, _, err := runCLI(t, GetAccessCommand, "table", "suggest", "--since", "soon")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), "invalid since")
}

func TestAccessSuggest_OnlyReadsFromTheDaemon(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/suggest": okResp(suggestResponse)})
	_, _, err := runCLI(t, GetAccessCommand, "table", "suggest", "--patch")
	require.NoError(t, err)
	for _, r := range rec.all() {
		require.Equal(t, "GET", r.Method, "suggest never writes a profile")
	}
}
