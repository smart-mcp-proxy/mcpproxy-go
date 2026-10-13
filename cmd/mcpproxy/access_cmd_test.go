package main

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

const explainBlocked = `{"subject":{"kind":"client","name":"cursor"},"tool":"github:create_issue","profile":{"name":"work-readonly","source":"pin"},
"steps":[{"step":"credential","status":"pass","detail":""},{"step":"profile","status":"pass","detail":""},{"step":"server_in_scope","status":"pass","detail":""},{"step":"tool_rule","status":"pass","detail":""},
{"step":"tier_cap","status":"fail","detail":"above_tier_cap"},{"step":"token_permission","status":"skip","detail":""},{"step":"global_gate","status":"skip","detail":""},{"step":"server_state","status":"skip","detail":""},{"step":"tool_approval","status":"skip","detail":""}],
"verdict":"hidden","first_failure":"tier_cap",
"fixes":[{"step":"tier_cap","action":"allow_in_profile","target":"work-readonly","label":"Allow github:create_issue in Work Read-only"},{"step":"tier_cap","action":"move_client","target":"cursor","label":"Move Cursor to Work Full"}]}`

func TestAccessExplain_TableGolden(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/explain": okResp(explainBlocked)})
	out, _, err := runCLI(t, GetAccessCommand, "table", "explain", "--tool", "github:create_issue", "--client", "cursor")
	require.NoError(t, err)
	assertGolden108(t, "access-explain.golden", out)
	q, _ := url.ParseQuery(rec.only(t).RawQuery)
	require.Equal(t, url.Values{"tool": {"github:create_issue"}, "client": {"cursor"}}, q)
	require.Contains(t, out, "VERDICT: hidden at tier_cap")
	require.Contains(t, out, "mcpproxy profile update work-readonly --add-allow github:create_issue")
}

func TestAccessExplain_JSONIsData(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/explain": okResp(explainBlocked)})
	out, _, err := runCLI(t, GetAccessCommand, "json", "explain", "--tool", "github:create_issue", "--profile", "work-readonly")
	require.NoError(t, err)
	require.Equal(t, indented(t, explainBlocked), out)
}

func TestAccessExplain_SubjectQueryMapping(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		query url.Values
	}{
		{[]string{"--token", "ci"}, url.Values{"tool": {"github:list_issues"}, "token": {"ci"}}},
		{[]string{"--profile", "p"}, url.Values{"tool": {"github:list_issues"}, "profile": {"p"}}},
		{[]string{"--anonymous"}, url.Values{"tool": {"github:list_issues"}, "anonymous": {"true"}}},
	} {
		rec := newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/explain": okResp(`{"subject":{"kind":"token","name":"ci"},"tool":"github:list_issues","profile":{"name":"p","source":"pin"},"steps":[],"verdict":"allowed","first_failure":"","fixes":[]}`)})
		out, _, err := runCLI(t, GetAccessCommand, "table", append([]string{"explain", "--tool", "github:list_issues"}, tc.args...)...)
		require.NoError(t, err)
		q, _ := url.ParseQuery(rec.only(t).RawQuery)
		require.Equal(t, tc.query, q)
		require.Contains(t, out, "VERDICT: allowed\n")
	}
}

func TestAccessExplain_ExactlyOneSubject(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{})
	for _, args := range [][]string{
		{"explain", "--tool", "github:x"},
		{"explain", "--tool", "github:x", "--client", "cursor", "--token", "ci"},
		{"explain", "--tool", "github:x", "--profile", "p", "--anonymous"},
		{"explain", "--client", "cursor"},
	} {
		_, _, err := runCLI(t, GetAccessCommand, "table", args...)
		require.Error(t, err, args)
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
	}
	require.Empty(t, rec.all(), "a bad subject list never reaches the daemon")
}

func TestAccessExplain_Exit0OnBlockedVerdict(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/explain": okResp(explainBlocked)})
	_, _, err := runCLI(t, GetAccessCommand, "table", "explain", "--tool", "github:create_issue", "--client", "cursor")
	require.NoError(t, err, "the verdict is data, not an error")
}

func TestAccessExplain_404Exit1(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/access/explain": refuse(http.StatusNotFound, `{"success":false,"error":"client not found"}`)})
	_, _, err := runCLI(t, GetAccessCommand, "table", "explain", "--tool", "github:create_issue", "--client", "nope")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), "client not found")
}
