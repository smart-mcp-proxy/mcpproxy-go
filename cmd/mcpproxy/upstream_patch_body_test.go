package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #2008: `mcpproxy upstream patch` could not change a stdio server's
// command or arguments, although PATCH /api/v1/servers/{id} accepts both.

func patchBodyJSON(t *testing.T, in upstreamPatchInput) string {
	t.Helper()
	body, _, err := buildUpstreamPatchBody(in)
	require.NoError(t, err)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return string(raw)
}

func TestBuildUpstreamPatchBody_Command(t *testing.T) {
	got := patchBodyJSON(t, upstreamPatchInput{Command: "uvx", CommandSet: true})
	assert.JSONEq(t, `{"command":"uvx"}`, got, "args must be omitted so the stored vector is kept")
}

func TestBuildUpstreamPatchBody_CommandIsStoredAsGiven(t *testing.T) {
	// A path may legitimately end in a space; only a blank value is refused.
	got := patchBodyJSON(t, upstreamPatchInput{Command: "/opt/mcp/server ", CommandSet: true})
	assert.JSONEq(t, `{"command":"/opt/mcp/server "}`, got)
	got = patchBodyJSON(t, upstreamPatchInput{CommandLine: []string{"/opt/mcp/server ", "a"}})
	assert.JSONEq(t, `{"command":"/opt/mcp/server ","args":["a"]}`, got)
}

func TestBuildUpstreamPatchBody_Args(t *testing.T) {
	got := patchBodyJSON(t, upstreamPatchInput{Args: []string{"mcp-server-sqlite", "--db", "a,b.db"}})
	assert.JSONEq(t, `{"args":["mcp-server-sqlite","--db","a,b.db"]}`, got)
}

func TestBuildUpstreamPatchBody_ClearArgs(t *testing.T) {
	got := patchBodyJSON(t, upstreamPatchInput{ClearArgs: true})
	assert.JSONEq(t, `{"args":[]}`, got, "an empty array, not null: null means 'leave unchanged'")
}

func TestBuildUpstreamPatchBody_CommandLine(t *testing.T) {
	got := patchBodyJSON(t, upstreamPatchInput{CommandLine: []string{"npx", "-y", "some-mcp"}})
	assert.JSONEq(t, `{"command":"npx","args":["-y","some-mcp"]}`, got)

	// A bare command after `--` replaces the whole command line, so the old
	// arguments must go too.
	got = patchBodyJSON(t, upstreamPatchInput{CommandLine: []string{"my-server"}})
	assert.JSONEq(t, `{"command":"my-server","args":[]}`, got)
}

func TestBuildUpstreamPatchBody_CombinesWithEnvAndHeaders(t *testing.T) {
	got := patchBodyJSON(t, upstreamPatchInput{
		Command:      "uvx",
		CommandSet:   true,
		Args:         []string{"x"},
		Envs:         []string{"LOG=debug"},
		EnvRemove:    []string{"OLD"},
		Headers:      []string{"X-A: 1"},
		HeaderRemove: []string{"X-B"},
		InitTimeout:  "120s",
	})
	assert.JSONEq(t, `{
		"command":"uvx","args":["x"],
		"env":{"LOG":"debug","OLD":null},
		"headers":{"X-A":"1","X-B":null},
		"init_timeout":"120s"}`, got)
}

func TestBuildUpstreamPatchBody_Summary(t *testing.T) {
	_, summary, err := buildUpstreamPatchBody(upstreamPatchInput{CommandLine: []string{"npx", "-y", "some-mcp"}})
	require.NoError(t, err)
	joined := strings.Join(summary, ", ")
	assert.Contains(t, joined, "command set")
	assert.Contains(t, joined, "2 arg(s) set")
	// Argument values can carry credentials; the summary must not echo them.
	assert.NotContains(t, joined, "some-mcp")

	_, summary, err = buildUpstreamPatchBody(upstreamPatchInput{ClearArgs: true})
	require.NoError(t, err)
	assert.Contains(t, strings.Join(summary, ", "), "args cleared")
}

func TestBuildUpstreamPatchBody_Errors(t *testing.T) {
	cases := []struct {
		name string
		in   upstreamPatchInput
		want string
	}{
		{"nothing to change", upstreamPatchInput{}, "at least one of"},
		{"empty command", upstreamPatchInput{Command: "  ", CommandSet: true}, "--command must not be empty"},
		{"args and clear", upstreamPatchInput{Args: []string{"a"}, ClearArgs: true}, "--arg and --clear-args conflict"},
		{"command line and --command", upstreamPatchInput{CommandLine: []string{"npx"}, Command: "uvx", CommandSet: true}, "cannot be combined"},
		{"command line and --arg", upstreamPatchInput{CommandLine: []string{"npx"}, Args: []string{"a"}}, "cannot be combined"},
		{"command line and --clear-args", upstreamPatchInput{CommandLine: []string{"npx"}, ClearArgs: true}, "cannot be combined"},
		{"empty command line head", upstreamPatchInput{CommandLine: []string{" ", "a"}}, "command after -- must not be empty"},
		{"bad header", upstreamPatchInput{Headers: []string{"nocolon"}}, "invalid --header format"},
		{"bad env", upstreamPatchInput{Envs: []string{"noequals"}}, "invalid --env format"},
		{"bad timeout", upstreamPatchInput{InitTimeout: "soon"}, "invalid --init-timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildUpstreamPatchBody(tc.in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestSplitUpstreamPatchArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		dash     int
		wantName string
		wantLine []string
		wantErr  string
	}{
		{"name only", []string{"sqlite"}, -1, "sqlite", nil, ""},
		{"name and command line", []string{"sqlite", "uvx", "pkg"}, 1, "sqlite", []string{"uvx", "pkg"}, ""},
		{"no name", []string{}, -1, "", nil, "exactly one server name"},
		{"two names", []string{"a", "b"}, -1, "", nil, "exactly one server name"},
		{"dash before name and command", []string{"uvx", "pkg"}, 0, "", nil, "exactly one server name"},
		// A name that starts with a dash can only be passed behind `--`; that
		// form must keep working for --env / --header patches.
		{"dash-leading name behind --", []string{"-sqlite"}, 0, "-sqlite", nil, ""},
		{"dash with nothing after", []string{"sqlite"}, 1, "", nil, "a command is required after --"},
		{"blank name", []string{"  "}, -1, "", nil, "server name is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, line, err := splitUpstreamPatchArgs(tc.args, tc.dash)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantName, name)
			assert.Equal(t, tc.wantLine, line)
		})
	}
}
