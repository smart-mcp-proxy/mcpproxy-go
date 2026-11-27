package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
)

// Spec 124 PR-2 (T100): `mcpproxy activity sessions`.

// workSessionsTestRoot builds a fresh `activity` group holding only the two
// work-session commands. Both are built by constructors with their own flag
// variables, so one test's flags never reach the next.
func workSessionsTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "activity"}
	root.AddCommand(newActivitySessionsCmd(), newActivitySessionCmd())
	return root
}

const workSessionsListBody = `{
  "sessions": [
    {"id": "ws-aaa", "token_name": "ci", "client_id": "cursor", "client_name": "Cursor", "project_name": "shop",
     "surface": "mcp", "profile": "work", "profile_source": "pin", "status": "ended",
     "started_at": "2026-11-26T22:00:00Z", "last_activity_at": "2026-11-27T00:13:20Z", "ended_at": "2026-11-27T00:43:20Z",
     "duration_ms": 8000000, "attempts": 120, "failures": 3, "policy_blocks": 1, "stored_failures": 3,
     "first_failure_at": "2026-11-26T23:00:00Z",
     "last_failure": {"at": "2026-11-27T00:13:20.5Z", "error_class": "network", "error_code": "MCPX_NET",
                      "server": "github", "tool": "create_issue", "reason": "network: connection refused"},
     "connection_count": 4, "truncated_start_at": "2026-11-26T22:00:00Z"},
    {"id": "ws-bbb", "surface": "rest", "run_name": "nightly-7", "status": "active",
     "started_at": "2026-11-27T01:00:00Z", "last_activity_at": "2026-11-27T01:00:45Z",
     "duration_ms": 45000, "attempts": 4, "failures": 0, "policy_blocks": 0, "stored_failures": 0}
  ],
  "total": 2, "limit": 25, "offset": 0}`

func TestActivitySessions_Table(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": okResp(workSessionsListBody),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "sessions")
	require.NoError(t, err)

	// The columns of User Story 4, and the id the timeline command takes.
	for _, header := range []string{"ID", "TOKEN", "CLIENT", "PROJECT", "RUN", "STARTED", "DURATION", "CALLS", "FAILURES", "LAST FAILURE", "REASON"} {
		require.Contains(t, out, header)
	}
	lines := strings.Split(out, "\n")
	var first, second string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "ws-aaa"):
			first = line
		case strings.HasPrefix(line, "ws-bbb"):
			second = line
		}
	}
	for _, want := range []string{"ci", "cursor", "shop", "2026-11-26 22:00:00Z*", "2h13m", "120", "3 (1 blocked)", "2026-11-27 00:13:20Z", "network: connection refused"} {
		require.Contains(t, first, want, out)
	}
	// A REST run has no token, client or project: its surface and run name say
	// what it was. A session still running says so.
	for _, want := range []string{"rest", "nightly-7", "2026-11-27 01:00:00Z", "45s (active)", "4", "0"} {
		require.Contains(t, second, want, out)
	}
	require.NotContains(t, second, "Z*", "only a session whose start was trimmed is starred")
	require.Contains(t, out, "* the start of this session is no longer stored")
	require.Contains(t, out, "Showing 2 of 2 sessions")
	require.Contains(t, out, "mcpproxy activity session <id>")

	require.Equal(t, "/api/v1/work-sessions", rec.only(t).Path)
	require.Equal(t, "limit=25", rec.only(t).RawQuery, "no filter is sent unless asked for")
}

// The RUN column appears only when some session carries a run name.
func TestActivitySessions_TableWithoutRunColumn(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": okResp(`{"sessions":[{"id":"ws-1","token_name":"ci","status":"ended","started_at":"2026-11-26T22:00:00Z","last_activity_at":"2026-11-26T22:00:01Z","duration_ms":1000,"attempts":1,"failures":0,"policy_blocks":0,"stored_failures":0}],"total":40,"limit":1,"offset":3}`),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "sessions", "--limit", "1", "--offset", "3")
	require.NoError(t, err)
	require.NotContains(t, out, "RUN")
	require.Contains(t, out, "Showing 1 of 40 sessions (from 4)")
}

func TestActivitySessions_Empty(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": okResp(`{"sessions":[],"total":0,"limit":25,"offset":0}`),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "sessions", "--failed")
	require.NoError(t, err)
	require.Contains(t, out, "No work sessions found")
}

func TestActivitySessions_FlagsMapToTheListDoor(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want url.Values
	}{
		// User Story 4, scenario 1: the failed-only list is newest failure first.
		{"failed", []string{"--failed"}, url.Values{"failed": {"true"}, "sort": {"last_failure"}}},
		{"failed, sorted as asked", []string{"--failed", "--sort", "last_activity"}, url.Values{"failed": {"true"}, "sort": {"last_activity"}}},
		{"failed without blocks", []string{"--failed", "--no-blocked"}, url.Values{"failed": {"true"}, "include_blocked": {"false"}, "sort": {"last_failure"}}},
		{"token", []string{"--token", "ci bot"}, url.Values{"token": {"ci bot"}}},
		{"client", []string{"--client", "cursor"}, url.Values{"client": {"cursor"}}},
		{"profile", []string{"--profile", "work"}, url.Values{"profile": {"work"}}},
		{"run", []string{"--run", "nightly-7"}, url.Values{"run": {"nightly-7"}}},
		{"error class", []string{"--error-class", "timeout"}, url.Values{"error_class": {"timeout"}}},
		{"absolute range", []string{"--since", "2026-11-26T00:00:00Z", "--until", "2026-11-27T00:00:00+02:00"},
			url.Values{"since": {"2026-11-26T00:00:00Z"}, "until": {"2026-11-27T00:00:00+02:00"}}},
		{"sort", []string{"--sort", "last_failure"}, url.Values{"sort": {"last_failure"}}},
		{"paging", []string{"--limit", "100", "--offset", "200"}, url.Values{"limit": {"100"}, "offset": {"200"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRESTRecorder(t, map[string]cannedResponse{
				"GET /api/v1/work-sessions": okResp(`{"sessions":[],"total":0,"limit":25,"offset":0}`),
			})
			_, _, err := runCLI(t, workSessionsTestRoot, "table", append([]string{"sessions"}, tc.args...)...)
			require.NoError(t, err)
			got, err := url.ParseQuery(rec.only(t).RawQuery)
			require.NoError(t, err)
			if _, set := tc.want["limit"]; !set {
				tc.want["limit"] = []string{"25"}
			}
			require.Equal(t, tc.want, got)
		})
	}
}

// A relative --since is resolved by the CLI: the door takes RFC3339 only.
func TestActivitySessions_RelativeSince(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": okResp(`{"sessions":[],"total":0,"limit":25,"offset":0}`),
	})
	_, _, err := runCLI(t, workSessionsTestRoot, "table", "sessions", "--since", "-24h")
	require.NoError(t, err)
	got, err := url.ParseQuery(rec.only(t).RawQuery)
	require.NoError(t, err)
	require.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`, got.Get("since"))
}

// A bad flag fails before any request is made, with exit code 1.
func TestActivitySessions_InvalidFlagsSendNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"limit too small":         {"--limit", "0"},
		"limit too large":         {"--limit", "101"},
		"negative offset":         {"--offset", "-1"},
		"unknown sort":            {"--sort", "name"},
		"unknown error class":     {"--error-class", "bogus"},
		"unparsable since":        {"--since", "yesterday"},
		"unparsable until":        {"--until", "2026-11-27"},
		"no-blocked needs failed": {"--no-blocked"},
		"positional argument":     {"ws-1"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := newRESTRecorder(t, map[string]cannedResponse{})
			_, _, err := runCLI(t, workSessionsTestRoot, "table", append([]string{"sessions"}, args...)...)
			require.Error(t, err)
			require.Equal(t, ExitCodeGeneralError, classifyError(err))
			require.Empty(t, rec.all())
		})
	}
}

// Acceptance scenario 3: -o json carries the fields of the REST response.
func TestActivitySessions_JSONCarriesTheRESTFields(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": okResp(workSessionsListBody),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "json", "sessions")
	require.NoError(t, err)
	var got, want any
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	require.NoError(t, json.Unmarshal([]byte(workSessionsListBody), &want))
	require.Equal(t, want, got)
}

func TestActivitySessions_YAML(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": okResp(workSessionsListBody),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "yaml", "sessions")
	require.NoError(t, err)
	var got struct {
		Sessions []struct {
			ID          string `yaml:"id"`
			LastFailure struct {
				Reason string `yaml:"reason"`
			} `yaml:"last_failure"`
		} `yaml:"sessions"`
		Total int `yaml:"total"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(out), &got), out)
	require.Equal(t, 2, got.Total)
	require.Equal(t, "ws-aaa", got.Sessions[0].ID)
	require.Equal(t, "network: connection refused", got.Sessions[0].LastFailure.Reason)
}

// Every string in the list comes from a client or an upstream; none may reach
// the terminal unescaped.
func TestActivitySessions_TableEscapesTerminalControls(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": okResp(`{"sessions":[{"id":"ws-1","token_name":"ci\u001b[31m","client_name":"evil\u202e","project_name":"p\u001b]0;x\u0007","run_name":"r\u001b[2J","status":"ended","started_at":"2026-11-26T22:00:00Z","last_activity_at":"2026-11-26T22:00:01Z","duration_ms":1000,"attempts":1,"failures":1,"policy_blocks":0,"stored_failures":1,"last_failure":{"at":"2026-11-26T22:00:01Z","reason":"boom\u001b[1m"}}],"total":1,"limit":25,"offset":0}`),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "sessions")
	require.NoError(t, err)
	require.NotContains(t, out, "\x1b")
	require.NotContains(t, out, "\u202e")
	require.NotContains(t, out, "\x07")
}

// The Unicode line separators are neither control nor format characters, and
// a terminal that honours them would break a row in two.
func TestActivitySessions_TableEscapesLineSeparators(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": okResp(`{"sessions":[{"id":"ws-1","client_name":"cursor\u2028other","project_name":"a\u2029b","status":"ended","started_at":"2026-11-26T22:00:00Z","last_activity_at":"2026-11-26T22:00:01Z","duration_ms":1000,"attempts":1,"failures":1,"policy_blocks":0,"stored_failures":1,"last_failure":{"at":"2026-11-26T22:00:01Z","reason":"one\u2028two"}}],"total":1,"limit":25,"offset":0}`),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "sessions")
	require.NoError(t, err)
	require.NotContains(t, out, "\u2028")
	require.NotContains(t, out, "\u2029")
	require.Contains(t, out, `cursor\u2028other`)
}

// An offset past the last session is not "nothing matches".
func TestActivitySessions_OffsetPastTheEnd(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		// The door clamps the offset it reports to the number of matches.
		"GET /api/v1/work-sessions": okResp(`{"sessions":[],"total":10,"limit":25,"offset":10}`),
	})
	out, _, err := runCLI(t, workSessionsTestRoot, "table", "sessions", "--offset", "100")
	require.NoError(t, err)
	require.NotContains(t, out, "No work sessions found")
	require.Contains(t, out, "No sessions at offset 100: 10 sessions match", "the offset that was asked for is the one named")
}

// A line break in the daemon's text must not read as a second message of the
// CLI.
func TestActivitySessions_RefusalIsOneLine(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": refuse(http.StatusBadRequest, `{"success":false,"error":"Request denied\nShowing 0 of 0 sessions\tok","request_id":"r\n1"}`),
	})
	_, _, err := runCLI(t, workSessionsTestRoot, "table", "sessions")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Request denied")
	require.NotContains(t, err.Error(), "denied\nShowing")
	require.NotContains(t, err.Error(), "\t")
	require.NotContains(t, err.Error(), "r\n1")
}

// A body that is not the daemon's JSON (a proxy's error page) is quoted as
// text: nothing in it reaches the terminal as a control sequence.
func TestActivitySessions_ForeignErrorBodyIsEscaped(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": refuse(http.StatusBadGateway, "bad gateway \x1b[2J\x1b[H"),
	})
	_, _, err := runCLI(t, workSessionsTestRoot, "table", "sessions")
	require.Error(t, err)
	require.Contains(t, err.Error(), "502")
	require.NotContains(t, err.Error(), "\x1b")
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
}

// A refusal keeps the daemon's words and exits 1.
func TestActivitySessions_RefusalIsReported(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/work-sessions": refuse(http.StatusBadRequest, `{"success":false,"error":"Invalid since or until. Use RFC3339 time","request_id":"r-1"}`),
	})
	_, _, err := runCLI(t, workSessionsTestRoot, "table", "sessions")
	require.ErrorContains(t, err, "Invalid since or until")
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
}

// Both commands are registered on the real `activity` group.
func TestActivitySessionCommandsAreRegistered(t *testing.T) {
	names := map[string]*cobra.Command{}
	for _, sub := range GetActivityCommand().Commands() {
		names[sub.Name()] = sub
	}
	require.Contains(t, names, "sessions")
	require.Contains(t, names, "session")
	for _, flag := range []string{"failed", "no-blocked", "token", "client", "profile", "run", "error-class", "since", "until", "sort", "limit", "offset"} {
		require.NotNil(t, names["sessions"].Flags().Lookup(flag), flag)
	}
	for _, flag := range []string{"failures", "before", "after", "at", "include-bodies"} {
		require.NotNil(t, names["session"].Flags().Lookup(flag), flag)
	}
}

// TestActivitySessionsHelpJSON runs the real --help-json hook in a helper
// process (the production hook exits after writing JSON). `session` takes a
// positional argument, which must not block machine-readable discovery.
func TestActivitySessionsHelpJSON(t *testing.T) {
	if target := os.Getenv("MCPPROXY_124_HELP_TARGET"); target != "" {
		root := &cobra.Command{Use: "mcpproxy"}
		root.AddCommand(workSessionsTestRoot())
		clioutput.SetupHelpJSON(root)
		root.SetArgs(append(strings.Fields(target), "--help-json"))
		require.NoError(t, root.Execute())
		return
	}
	for target, flags := range map[string][]string{
		"activity sessions": {"failed", "no-blocked", "token", "client", "profile", "run", "error-class", "since", "until", "sort", "limit", "offset"},
		"activity session":  {"failures", "before", "after", "at", "include-bodies"},
	} {
		t.Run(target, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestActivitySessionsHelpJSON$")
			cmd.Env = append(os.Environ(), "MCPPROXY_124_HELP_TARGET="+target)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			var payload struct {
				Name  string `json:"name"`
				Flags []struct {
					Name string `json:"name"`
				} `json:"flags"`
			}
			require.NoError(t, json.Unmarshal(out, &payload), string(out))
			require.Equal(t, target[strings.LastIndex(target, " ")+1:], payload.Name)
			have := map[string]bool{}
			for _, f := range payload.Flags {
				have[f.Name] = true
			}
			for _, flag := range flags {
				require.True(t, have[flag], "flag --%s missing from --help-json of %q", flag, target)
			}
		})
	}
}
