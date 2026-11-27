package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 124 PR-2 (T101): `mcpproxy activity session <id>`.

const timelineSessionJSON = `{"id": "ws-aaa", "token_name": "ci", "client_id": "cursor", "client_name": "Cursor",
  "project_name": "shop", "surface": "mcp", "status": "ended",
  "started_at": "2026-11-26T22:00:00Z", "last_activity_at": "2026-11-27T00:13:20Z", "ended_at": "2026-11-27T00:43:20Z",
  "duration_ms": 8000000, "attempts": 120, "failures": 5, "policy_blocks": 1, "stored_failures": 3,
  "truncated_start_at": "2026-11-26T21:00:00Z"}`

// timelineRow builds one stored row of a canned timeline.
func timelineRow(index int, kind, at, extra, activity string) string {
	row := fmt.Sprintf(`{"index": %d, "kind": %q, "at": %q`, index, kind, at)
	if extra != "" {
		row += ", " + extra
	}
	if activity != "" {
		row += `, "activity": ` + activity
	}
	return row + "}"
}

func callActivity(id, at, server, tool, status, rest string) string {
	a := fmt.Sprintf(`{"id": %q, "type": "tool_call", "server_name": %q, "tool_name": %q, "status": %q, "timestamp": %q, "has_sensitive_data": false`,
		id, server, tool, status, at)
	if rest != "" {
		a += ", " + rest
	}
	return a + "}"
}

var timelineBody = `{
  "session": ` + timelineSessionJSON + `,
  "rows": [` + strings.Join([]string{
	timelineRow(0, "session_start", "2026-11-26T22:00:00Z", "", ""),
	timelineRow(1, "call", "2026-11-26T22:00:01.25Z", "",
		callActivity("01CALL1", "2026-11-26T22:00:01.25Z", "github", "list_issues", "success", `"duration_ms": 120`)),
	timelineRow(2, "upstream_event", "2026-11-26T22:00:02.5Z", "",
		`{"id": "01EVENT", "type": "upstream_event", "server_name": "github", "status": "error",
		  "error_message": "The connection to the server was lost", "timestamp": "2026-11-26T22:00:02.5Z",
		  "metadata": {"kind": "connection_lost", "code": "MCPX_UPSTREAM_CONNECTION_LOST", "count": 3},
		  "has_sensitive_data": false}`),
	timelineRow(3, "call", "2026-11-26T22:00:03.007Z", `"failed": true`,
		callActivity("01CALL2", "2026-11-26T22:00:03.007Z", "github", "create_issue", "error",
			`"error_message": "dial tcp: connection refused", "duration_ms": 30012, "error_class": "network", "fault_domain": "upstream"`)),
	timelineRow(4, "policy_decision", "2026-11-26T22:00:04Z", `"failed": true, "policy_block": true`,
		`{"id": "01BLOCK", "type": "policy_decision", "server_name": "github", "tool_name": "delete_repo", "status": "blocked",
		  "timestamp": "2026-11-26T22:00:04Z", "metadata": {"decision": "blocked", "reason": "TOOL_BLOCKED: Tool is disabled"},
		  "block_reason": "tier_cap", "error_class": "proxy_policy", "has_sensitive_data": false}`),
	timelineRow(5, "session_end", "2026-11-27T00:43:20Z", "", ""),
}, ",\n") + `],
  "position": 3, "total_rows": 6, "failure_index": 1, "failure_total": 3,
  "lifetime_failures": 5, "earlier_failures_not_stored": true, "has_before": false, "has_after": false}`

func TestActivitySession_Table(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions/ws-aaa/timeline": okResp(timelineBody),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws-aaa")
	require.NoError(t, err)

	require.Contains(t, out, "Session ws-aaa")
	require.Contains(t, out, "token ci")
	require.Contains(t, out, "client cursor")
	require.Contains(t, out, "project shop")
	require.Contains(t, out, "120 calls")
	require.Contains(t, out, "5 failures (1 blocked)")
	// FR-061 and FR-043: what is no longer stored is said, not hidden.
	require.Contains(t, out, "the start of this session is no longer stored")
	require.Contains(t, out, "5 failures in its lifetime, 3 are still stored")
	require.Contains(t, out, "Failure 1 of 3")
	require.Contains(t, out, "rows 0-5 of 6")

	rowOf := func(needle string) string {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, needle) {
				return line
			}
		}
		t.Fatalf("no row with %q in:\n%s", needle, out)
		return ""
	}
	// Millisecond times, and rows that are not calls are marked apart.
	require.Contains(t, rowOf("SESSION START"), "2026-11-26 22:00:00.000Z")
	require.Contains(t, rowOf("SESSION END"), "2026-11-27 00:43:20.000Z")
	ok := rowOf("01CALL1")
	require.Contains(t, ok, "2026-11-26 22:00:01.250Z")
	require.Contains(t, ok, "github:list_issues")
	require.Contains(t, ok, "success")
	require.Contains(t, ok, "120ms")
	require.False(t, strings.HasPrefix(ok, "FAIL"))

	event := rowOf("UPSTREAM EVENT")
	require.Contains(t, event, "github")
	require.Contains(t, event, "connection_lost")
	require.Contains(t, event, "MCPX_UPSTREAM_CONNECTION_LOST")
	require.Contains(t, event, "The connection to the server was lost")
	require.Contains(t, event, "x3")

	failed := rowOf("01CALL2")
	require.True(t, strings.HasPrefix(failed, "FAIL"), failed)
	require.Contains(t, failed, "2026-11-26 22:00:03.007Z")
	require.Contains(t, failed, "github:create_issue")
	require.Contains(t, failed, "network")
	require.Contains(t, failed, "dial tcp: connection refused")
	require.Contains(t, failed, "30.0s")

	blocked := rowOf("01BLOCK")
	require.True(t, strings.HasPrefix(blocked, "BLOCK"), blocked)
	require.Contains(t, blocked, "tier_cap")
	require.Contains(t, blocked, "Tool is disabled", "the reason of a refusal is in the row's metadata")

	got := rec.only(t)
	require.Equal(t, "/api/v1/work-sessions/ws-aaa/timeline", got.Path)
	q, err := url.ParseQuery(got.RawQuery)
	require.NoError(t, err)
	require.Equal(t, url.Values{"before": {"20"}, "after": {"20"}}, q, "the default position is the door's: the first failure")
}

func TestActivitySession_FlagsMapToTheTimelineDoor(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want url.Values
	}{
		{"at end", []string{"--at", "end"}, url.Values{"at": {"end"}, "before": {"20"}, "after": {"20"}}},
		{"at nth failure", []string{"--at", "failure:2"}, url.Values{"at": {"failure:2"}, "before": {"20"}, "after": {"20"}}},
		{"at row", []string{"--at", "row:01CALL2"}, url.Values{"at": {"row:01CALL2"}, "before": {"20"}, "after": {"20"}}},
		{"at first failure", []string{"--at", "first_failure"}, url.Values{"at": {"first_failure"}, "before": {"20"}, "after": {"20"}}},
		{"context", []string{"--before", "0", "--after", "250"}, url.Values{"before": {"0"}, "after": {"250"}}},
		{"bodies", []string{"--include-bodies"}, url.Values{"include_bodies": {"true"}, "before": {"20"}, "after": {"20"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRESTRecorder(t, map[string]cannedResponse{
				"GET /api/v1/work-sessions/ws-aaa/timeline": okResp(timelineBody),
			})
			_, _, err := runCLI(t, workSessionsTestRoot, "table", append([]string{"session", "ws-aaa"}, tc.args...)...)
			require.NoError(t, err)
			got, err := url.ParseQuery(rec.only(t).RawQuery)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestActivitySession_InvalidFlagsSendNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"no id":                 {},
		"two ids":               {"ws-1", "ws-2"},
		"empty id":              {""},
		"before too large":      {"ws-1", "--before", "251"},
		"negative after":        {"ws-1", "--after", "-1"},
		"unknown position":      {"ws-1", "--at", "sideways"},
		"failure zero":          {"ws-1", "--at", "failure:0"},
		"failure not a number":  {"ws-1", "--at", "failure:x"},
		"row without an id":     {"ws-1", "--at", "row:"},
		"failures with at":      {"ws-1", "--failures", "--at", "end"},
		"failures with at same": {"ws-1", "--failures", "--at", "failure:2"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := newRESTRecorder(t, map[string]cannedResponse{})
			_, _, err := runCLI(t, workSessionsTestRoot, "table", append([]string{"session"}, args...)...)
			require.Error(t, err)
			require.Equal(t, ExitCodeGeneralError, classifyError(err))
			require.Empty(t, rec.all())
		})
	}
}

// Acceptance scenario 4: an id that matches no session exits non-zero, says
// so, and names the listing command.
func TestActivitySession_NotFound(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			newRESTRecorder(t, map[string]cannedResponse{
				"GET /api/v1/work-sessions/ws-nope/timeline": refuse(http.StatusNotFound, `{"success":false,"error":"Work session not found","request_id":"r-9"}`),
			})
			out, _, err := runCLI(t, workSessionsTestRoot, format, "session", "ws-nope")
			require.Error(t, err)
			require.Equal(t, ExitCodeGeneralError, classifyError(err))
			require.Contains(t, err.Error(), "ws-nope")
			require.Contains(t, err.Error(), "not found")
			require.Contains(t, err.Error(), "mcpproxy activity sessions")
			require.Empty(t, strings.TrimSpace(out), "nothing that reads as a result is printed")
		})
	}
}

// A refusal is one line. Cobra's usage block belongs to a command line that
// did not parse, and that one still gets it.
func TestActivitySession_UsageOnlyForAnUnparsedCommandLine(t *testing.T) {
	execute := func(t *testing.T, args ...string) (string, error) {
		setOutputGlobals(t, "table", false)
		var buf strings.Builder
		root := workSessionsTestRoot()
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs(args)
		var err error
		stdout, stderr := captureStd(func() { err = root.Execute() })
		return buf.String() + stdout + stderr, err
	}
	t.Run("refused by the daemon", func(t *testing.T) {
		newRESTRecorder(t, map[string]cannedResponse{
			"GET /api/v1/work-sessions/ws-nope/timeline": refuse(http.StatusNotFound, `{"success":false,"error":"Work session not found"}`),
		})
		out, err := execute(t, "session", "ws-nope")
		require.Error(t, err)
		require.NotContains(t, out, "Usage:")
	})
	t.Run("refused value", func(t *testing.T) {
		newRESTRecorder(t, map[string]cannedResponse{})
		out, err := execute(t, "sessions", "--limit", "500")
		require.Error(t, err)
		require.NotContains(t, out, "Usage:")
	})
	t.Run("unknown flag", func(t *testing.T) {
		newRESTRecorder(t, map[string]cannedResponse{})
		out, err := execute(t, "session", "ws-1", "--sideways")
		require.Error(t, err)
		require.Contains(t, out, "Usage:")
	})
}

// The id is the caller's text and is path-escaped, never spliced into the URL.
func TestActivitySession_IDIsEscaped(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions/ws a?b/timeline": okResp(timelineBody),
	})
	_, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws a?b")
	require.NoError(t, err)
	require.Equal(t, "/api/v1/work-sessions/ws a?b/timeline", rec.only(t).Path)
}

// Acceptance scenario 3: -o json carries the fields of the REST response.
func TestActivitySession_JSONCarriesTheRESTFields(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions/ws-aaa/timeline": okResp(timelineBody),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "json", "session", "ws-aaa")
	require.NoError(t, err)
	var got, want any
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	require.NoError(t, json.Unmarshal([]byte(timelineBody), &want))
	require.Equal(t, want, got)
}

// Bodies are asked for and shown only on request.
func TestActivitySession_IncludeBodies(t *testing.T) {
	body := `{"session": ` + timelineSessionJSON + `, "rows": [` +
		timelineRow(7, "call", "2026-11-26T22:00:03Z", `"failed": true`,
			callActivity("01CALL2", "2026-11-26T22:00:03Z", "github", "create_issue", "error",
				`"arguments": {"title": "broken\u001b[31m build", "path": "reports/April  2026 <a&b>.csv"}, "response": "line one\nline two `+strings.Repeat("word ", 120)+`the-end", "error_class": "network"`)) + `,` +
		timelineRow(8, "call", "2026-11-26T22:00:04Z", `"bodies_removed": true`,
			callActivity("01CALL3", "2026-11-26T22:00:04Z", "github", "list_issues", "success", "")) + `],
	  "position": 7, "total_rows": 9, "failure_index": 1, "failure_total": 1, "lifetime_failures": 1, "has_before": true, "has_after": false}`
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions/ws-aaa/timeline": okResp(body),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws-aaa", "--include-bodies")
	require.NoError(t, err)
	require.Contains(t, out, `"title"`)
	require.Contains(t, out, "broken")
	// A body is printed as one line of JSON, so no character of it is lost: a
	// line break is \n, and a run of spaces in a value stays a run of spaces.
	require.Contains(t, out, `"line one\nline two word`)
	require.Contains(t, out, `"path":"reports/April  2026 <a&b>.csv"`)
	require.Contains(t, out, "the-end", "a body is capped at its own length, not at a table cell's")
	require.NotContains(t, out, "\x1b", "a body is upstream text and is escaped like every other cell")
	require.Contains(t, out, "bodies removed by retention")
	// More rows exist before this page: the operator is told how to reach them.
	require.Contains(t, out, "--before")
}

// --failures walks every stored failure and prints overlapping context once.
// The first page reaches two failures whose contexts overlap; the third is far
// away and takes a second request.
func TestActivitySession_FailuresPrintOverlappingContextOnce(t *testing.T) {
	call := func(index int, failed bool) string {
		id := fmt.Sprintf("01ROW%03d", index)
		at := fmt.Sprintf("2026-11-26T22:%02d:%02dZ", index/60, index%60)
		status, extra, rest := "success", "", ""
		if failed {
			status, extra, rest = "error", `"failed": true`, `"error_class": "timeout"`
		}
		return timelineRow(index, "call", at, extra, callActivity(id, at, "github", "t", status, rest))
	}
	page := func(rows []string, position, failureIndex int, hasBefore, hasAfter bool) string {
		return fmt.Sprintf(`{"session": %s, "rows": [%s], "position": %d, "total_rows": 300,
		  "failure_index": %d, "failure_total": 3, "lifetime_failures": 3, "has_before": %t, "has_after": %t}`,
			timelineSessionJSON, strings.Join(rows, ","), position, failureIndex, hasBefore, hasAfter)
	}
	first := page([]string{call(8, false), call(9, false), call(10, true), call(11, false), call(12, true), call(13, false), call(14, false)}, 10, 1, true, true)
	second := page([]string{call(198, false), call(199, false), call(200, true), call(201, false)}, 200, 3, true, true)

	run := func(t *testing.T, format string) (string, []recordedRequest) {
		rec := newSequencedRecorder(t, "GET /api/v1/work-sessions/ws-aaa/timeline", []cannedResponse{okResp(first), okResp(second)})
		out, _, err := runCLI(t, workSessionsTestRoot, format, "session", "ws-aaa", "--failures", "--before", "2")
		require.NoError(t, err)
		return out, rec.all()
	}

	t.Run("table", func(t *testing.T) {
		out, requests := run(t, "table")
		require.Len(t, requests, 2, "the second failure was on the first page: no request for it")
		q1, _ := url.ParseQuery(requests[0].RawQuery)
		q2, _ := url.ParseQuery(requests[1].RawQuery)
		require.Equal(t, "first_failure", q1.Get("at"))
		require.Equal(t, "2", q1.Get("before"))
		require.Equal(t, "failure:3", q2.Get("at"))
		require.Equal(t, "2", q2.Get("before"))

		require.Contains(t, out, "Failures 1-2 of 3")
		require.Contains(t, out, "Failure 3 of 3")
		// Rows 8..12 are the context of failures 1 and 2; row 10 is in both and
		// is printed once. Rows 13 and 14 are after the last failure of the
		// group and are not context of anything.
		for _, index := range []int{8, 9, 10, 11, 12, 198, 199, 200} {
			require.Equal(t, 1, strings.Count(out, fmt.Sprintf("01ROW%03d", index)), "row %d", index)
		}
		for _, index := range []int{13, 14, 201} {
			require.NotContains(t, out, fmt.Sprintf("01ROW%03d", index))
		}
	})

	t.Run("json", func(t *testing.T) {
		out, _ := run(t, "json")
		var got struct {
			Session struct {
				ID string `json:"id"`
			} `json:"session"`
			Rows []struct {
				Index  int  `json:"index"`
				Failed bool `json:"failed"`
			} `json:"rows"`
			Failures []struct {
				Number int `json:"number"`
				Index  int `json:"index"`
			} `json:"failures"`
			Before                   int  `json:"before"`
			After                    int  `json:"after"`
			TotalRows                int  `json:"total_rows"`
			FailureTotal             int  `json:"failure_total"`
			LifetimeFailures         int  `json:"lifetime_failures"`
			EarlierFailuresNotStored bool `json:"earlier_failures_not_stored"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got), out)
		require.Equal(t, "ws-aaa", got.Session.ID)
		var indexes []int
		for _, r := range got.Rows {
			indexes = append(indexes, r.Index)
		}
		require.Equal(t, []int{8, 9, 10, 11, 12, 198, 199, 200}, indexes, "one object, rows in order, each once")
		require.Len(t, got.Failures, 3)
		require.Equal(t, 1, got.Failures[0].Number)
		require.Equal(t, 10, got.Failures[0].Index)
		require.Equal(t, 2, got.Failures[1].Number)
		require.Equal(t, 12, got.Failures[1].Index)
		require.Equal(t, 3, got.Failures[2].Number)
		require.Equal(t, 200, got.Failures[2].Index)
		require.Equal(t, 2, got.Before)
		require.Equal(t, 0, got.After)
		require.Equal(t, 300, got.TotalRows)
		require.Equal(t, 3, got.FailureTotal)
	})
}

// With --after the context of a failure also runs forward. A failure whose
// forward context is cut by the end of the page is asked for again, so its
// context is whole.
func TestActivitySession_FailuresWithAfterContext(t *testing.T) {
	call := func(index int, failed bool) string {
		id := fmt.Sprintf("01ROW%03d", index)
		extra, status := "", "success"
		if failed {
			extra, status = `"failed": true`, "error"
		}
		return timelineRow(index, "call", "2026-11-26T22:00:00Z", extra, callActivity(id, "2026-11-26T22:00:00Z", "github", "t", status, ""))
	}
	page := func(rows []string, position, failureIndex int, hasAfter bool) string {
		return fmt.Sprintf(`{"session": %s, "rows": [%s], "position": %d, "total_rows": 9,
		  "failure_index": %d, "failure_total": 2, "lifetime_failures": 2, "has_before": false, "has_after": %t}`,
			timelineSessionJSON, strings.Join(rows, ","), position, failureIndex, hasAfter)
	}
	// Failure 2 is the last row of the first page, and more rows follow: its
	// one row of forward context is not on this page.
	first := page([]string{call(3, false), call(4, true), call(5, false), call(6, true)}, 4, 1, true)
	second := page([]string{call(5, false), call(6, true), call(7, false), call(8, false)}, 6, 2, false)
	rec := newSequencedRecorder(t, "GET /api/v1/work-sessions/ws-aaa/timeline", []cannedResponse{okResp(first), okResp(second)})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws-aaa", "--failures", "--before", "1", "--after", "1")
	require.NoError(t, err)
	require.Len(t, rec.all(), 2)
	for _, index := range []int{3, 4, 5, 6, 7} {
		require.Equal(t, 1, strings.Count(out, fmt.Sprintf("01ROW%03d", index)), "row %d\n%s", index, out)
	}
	require.NotContains(t, out, "01ROW008")
	require.Contains(t, out, "Failures 1-2 of 2")
}

// A session with no stored failure has nothing to walk, and says how many
// failures it had.
func TestActivitySession_FailuresNoneStored(t *testing.T) {
	body := `{"session": ` + timelineSessionJSON + `, "rows": [` + timelineRow(0, "session_start", "2026-11-26T22:00:00Z", "", "") + `],
	  "position": 0, "total_rows": 40, "failure_index": 0, "failure_total": 0, "lifetime_failures": 5,
	  "earlier_failures_not_stored": true, "has_before": false, "has_after": true}`
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions/ws-aaa/timeline": okResp(body),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws-aaa", "--failures")
	require.NoError(t, err)
	require.Len(t, rec.all(), 1)
	require.Contains(t, out, "No stored failures")
	require.Contains(t, out, "5 failures in its lifetime, 0 are still stored")
	require.NotContains(t, out, "SESSION START")
}

// The walk ends when the door stops making progress, whatever it answers.
func TestActivitySession_FailuresWalkAlwaysAdvances(t *testing.T) {
	// The door keeps answering "failure 1 of 4" with the same page.
	stuck := `{"session": ` + timelineSessionJSON + `, "rows": [` +
		timelineRow(4, "call", "2026-11-26T22:00:00Z", `"failed": true`, callActivity("01ROW004", "2026-11-26T22:00:00Z", "github", "t", "error", "")) + `],
	  "position": 4, "total_rows": 90, "failure_index": 1, "failure_total": 4, "lifetime_failures": 4, "has_before": true, "has_after": true}`
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions/ws-aaa/timeline": okResp(stuck),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws-aaa", "--failures")
	require.NoError(t, err)
	require.Len(t, rec.all(), 4, "one request per failure at most")
	require.Equal(t, 1, strings.Count(out, "01ROW004"))
}

// A 404 that is not the door's own answer is not "this session does not
// exist": the daemon's (or the proxy's) words are reported as they came.
func TestActivitySession_Foreign404IsNotASessionNotFound(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions/ws-1/timeline": refuse(http.StatusNotFound, `{"success":false,"error":"Route not found","request_id":"gw-42"}`),
	})
	_, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws-1")
	require.ErrorContains(t, err, "Route not found")
	require.ErrorContains(t, err, "gw-42")
	require.NotContains(t, err.Error(), "mcpproxy activity sessions")
}

// One page holds 500 rows. The door cuts the forward context of a larger
// request without saying so, so the CLI refuses it.
func TestActivitySession_ContextMustFitOnePage(t *testing.T) {
	for name, args := range map[string][]string{
		"position":               {"ws-1", "--before", "250", "--after", "250"},
		"failures with an after": {"ws-1", "--failures", "--before", "250", "--after", "250"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := newRESTRecorder(t, map[string]cannedResponse{})
			_, _, err := runCLI(t, workSessionsTestRoot, "table", append([]string{"session"}, args...)...)
			require.ErrorContains(t, err, "at most 499")
			require.Empty(t, rec.all())
		})
	}
	// --failures has no forward context unless asked, so the default --after
	// does not count against the page; the look-ahead is what is left of it.
	t.Run("failures without an after", func(t *testing.T) {
		rec := newRESTRecorder(t, map[string]cannedResponse{
			"GET /api/v1/work-sessions/ws-1/timeline": okResp(`{"session": ` + timelineSessionJSON + `, "rows": [], "position": 0, "total_rows": 0,
			  "failure_index": 0, "failure_total": 0, "lifetime_failures": 0, "has_before": false, "has_after": false}`),
		})
		_, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws-1", "--failures", "--before", "250")
		require.NoError(t, err)
		q, _ := url.ParseQuery(rec.only(t).RawQuery)
		require.Equal(t, "250", q.Get("before"))
		require.Equal(t, "249", q.Get("after"))
	})
}

// With bodies the walk asks for no row it will not print.
func TestActivitySession_FailuresWithBodiesAskForNoLookAhead(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions/ws-1/timeline": okResp(`{"session": ` + timelineSessionJSON + `, "rows": [], "position": 0, "total_rows": 0,
		  "failure_index": 0, "failure_total": 0, "lifetime_failures": 0, "has_before": false, "has_after": false}`),
	})
	_, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws-1", "--failures", "--include-bodies")
	require.NoError(t, err)
	q, _ := url.ParseQuery(rec.only(t).RawQuery)
	require.Equal(t, "0", q.Get("after"))
	require.Equal(t, "true", q.Get("include_bodies"))
}

// failuresPage builds a page of the --failures walk.
func failuresPage(rows []string, position, failureIndex, failureTotal, lifetime int, hasAfter bool) string {
	return fmt.Sprintf(`{"session": %s, "rows": [%s], "position": %d, "total_rows": 900,
	  "failure_index": %d, "failure_total": %d, "lifetime_failures": %d, "earlier_failures_not_stored": %t,
	  "has_before": true, "has_after": %t}`,
		timelineSessionJSON, strings.Join(rows, ","), position, failureIndex, failureTotal, lifetime, lifetime > failureTotal, hasAfter)
}

func failuresRow(index int, failed bool) string {
	id := fmt.Sprintf("01ROW%03d", index)
	extra, status := "", "success"
	if failed {
		extra, status = `"failed": true`, "error"
	}
	return timelineRow(index, "call", "2026-11-26T22:00:00Z", extra, callActivity(id, "2026-11-26T22:00:00Z", "github", "t", status, ""))
}

// Failures are asked for by their number among the stored ones. When
// retention removes an older failure in the middle of the walk, every number
// moves, and "failure 2" is no longer the failure after the first one read.
// The walk sees the stored count fall behind the lifetime count and starts
// over, so no stored failure is skipped.
func TestActivitySession_FailuresWalkRestartsWhenRetentionMovesTheNumbers(t *testing.T) {
	rec := newSequencedRecorder(t, "GET /api/v1/work-sessions/ws-aaa/timeline", []cannedResponse{
		// First pass: three stored failures, A at row 10.
		okResp(failuresPage([]string{failuresRow(10, true)}, 10, 1, 3, 3, true)),
		// A was removed: "failure 2" is now C, and one failure is gone.
		okResp(failuresPage([]string{failuresRow(600, true)}, 600, 2, 2, 3, true)),
		// Second pass: B and C, numbered 1 and 2.
		okResp(failuresPage([]string{failuresRow(300, true)}, 300, 1, 2, 3, true)),
		okResp(failuresPage([]string{failuresRow(600, true)}, 600, 2, 2, 3, true)),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "json", "session", "ws-aaa", "--failures", "--before", "0")
	require.NoError(t, err)
	requests := rec.all()
	require.Len(t, requests, 4)
	q, _ := url.ParseQuery(requests[2].RawQuery)
	require.Equal(t, "first_failure", q.Get("at"), "the walk starts over")

	var got struct {
		Rows []struct {
			Index int `json:"index"`
		} `json:"rows"`
		Failures []struct {
			Number int `json:"number"`
			Index  int `json:"index"`
		} `json:"failures"`
		FailureTotal int `json:"failure_total"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	require.Len(t, got.Failures, 2)
	require.Equal(t, 300, got.Failures[0].Index, "B is not skipped")
	require.Equal(t, 1, got.Failures[0].Number)
	require.Equal(t, 600, got.Failures[1].Index)
	require.Len(t, got.Rows, 2)
	require.Equal(t, 2, got.FailureTotal)
}

// A session that keeps changing is not walked forever, and a mixed result is
// never printed as if it were whole.
func TestActivitySession_FailuresWalkGivesUpWhenTheSessionKeepsChanging(t *testing.T) {
	rec := newSequencedRecorder(t, "GET /api/v1/work-sessions/ws-aaa/timeline", []cannedResponse{
		okResp(failuresPage([]string{failuresRow(10, true)}, 10, 1, 3, 3, true)),
		okResp(failuresPage([]string{failuresRow(600, true)}, 600, 2, 2, 3, true)),
		okResp(failuresPage([]string{failuresRow(10, true)}, 10, 1, 3, 3, true)),
		okResp(failuresPage([]string{failuresRow(600, true)}, 600, 2, 2, 3, true)),
		okResp(failuresPage([]string{failuresRow(10, true)}, 10, 1, 3, 3, true)),
		okResp(failuresPage([]string{failuresRow(600, true)}, 600, 2, 2, 3, true)),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws-aaa", "--failures", "--before", "0")
	require.ErrorContains(t, err, "changed while they were being read")
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Len(t, rec.all(), 6, "three passes")
	require.NotContains(t, out, "01ROW")
}

// The door can name a failure and return no row for it: the row was removed
// after the door found it, or is not readable with this credential. Nothing
// is shown for such a failure, and the count that was not returned is said.
func TestActivitySession_FailuresWithoutARowAreCountedNotShown(t *testing.T) {
	pages := []cannedResponse{
		// Failure 1 is at row 10; the page holds its neighbours only.
		okResp(failuresPage([]string{failuresRow(9, false), failuresRow(11, false)}, 10, 1, 2, 2, true)),
		okResp(failuresPage([]string{failuresRow(399, false), failuresRow(400, true)}, 400, 2, 2, 2, false)),
	}
	t.Run("table", func(t *testing.T) {
		newSequencedRecorder(t, "GET /api/v1/work-sessions/ws-aaa/timeline", pages)
		out, _, err := runCLI(t, workSessionsTestRoot, "table", "session", "ws-aaa", "--failures", "--before", "1")
		require.NoError(t, err)
		require.Contains(t, out, "1 of the 2 stored failures were not returned")
		require.NotContains(t, out, "Failure 1 of 2")
		require.NotContains(t, out, "No results found")
		require.NotContains(t, out, "01ROW009", "a row is context only of a failure that is shown")
		require.Contains(t, out, "Failure 2 of 2")
		require.Contains(t, out, "01ROW399")
		require.Contains(t, out, "01ROW400")
	})
	t.Run("json", func(t *testing.T) {
		newSequencedRecorder(t, "GET /api/v1/work-sessions/ws-aaa/timeline", pages)
		out, _, err := runCLI(t, workSessionsTestRoot, "json", "session", "ws-aaa", "--failures", "--before", "1")
		require.NoError(t, err)
		var got struct {
			Failures []struct {
				Number int `json:"number"`
				Index  int `json:"index"`
			} `json:"failures"`
			FailuresNotReturned int `json:"failures_not_returned"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got), out)
		require.Len(t, got.Failures, 1)
		require.Equal(t, 2, got.Failures[0].Number)
		require.Equal(t, 400, got.Failures[0].Index)
		require.Equal(t, 1, got.FailuresNotReturned)
	})
}
