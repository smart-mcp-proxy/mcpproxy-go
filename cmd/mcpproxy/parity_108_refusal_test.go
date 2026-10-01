package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 108-l (L14, T121): the CLI half of the shared-refusal parity. A fake
// daemon answers with the REST body built from the shared golden
// (internal/profile/testdata/contract/refusals.json, also read by the REST and
// MCP halves); the command must print exactly the golden message as the first
// line of its error, with the same exit code, so REST, MCP and the CLI cannot
// word a refusal differently.

type p108CLIRefusal struct {
	Name   string `json:"name"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Field  string `json:"field"`
	Error  string `json:"error"`
}

func p108CLIRefusals(t *testing.T) map[string]p108CLIRefusal {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "profile", "testdata", "contract", "refusals.json"))
	require.NoError(t, err)
	var doc struct {
		Refusals []p108CLIRefusal `json:"refusals"`
	}
	require.NoError(t, json.Unmarshal(b, &doc))
	out := map[string]p108CLIRefusal{}
	for _, r := range doc.Refusals {
		out[r.Name] = r
	}
	require.Len(t, out, 4)
	return out
}

// restBody is the REST error body the daemon would send for the refusal.
func (r p108CLIRefusal) restBody(t *testing.T, p, id string) string {
	t.Helper()
	body := map[string]any{"success": false, "error": strings.NewReplacer("<p>", p, "<id>", id).Replace(r.Error)}
	if r.Code != "" {
		body["code"] = r.Code
	}
	if r.Field != "" {
		body["field"] = r.Field
	}
	b, err := json.Marshal(body)
	require.NoError(t, err)
	return string(b)
}

// want is the first line the CLI prints: the golden message, plus the REST
// `field` the CLI appends as " (field: <name>)" (contracts/cli.md) when there is one.
func (r p108CLIRefusal) want(p, id string) string {
	text := strings.NewReplacer("<p>", p, "<id>", id).Replace(r.Error)
	if r.Field != "" {
		text += " (field: " + r.Field + ")"
	}
	return text
}

func TestProfilesV3RefusalParity_CLI(t *testing.T) {
	golden := p108CLIRefusals(t)

	// firstLine is what the operator reads first: the error up to the first newline.
	firstLine := func(err error) string { return strings.SplitN(err.Error(), "\n", 2)[0] }

	t.Run("profile_in_use", func(t *testing.T) {
		g := golden["profile_in_use"]
		newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/profiles/work-ro2": refuse(g.Status, g.restBody(t, "", ""))})
		_, _, err := runCLI(t, GetProfileCommand, "table", "delete", "work-ro2")
		require.Error(t, err)
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
		require.Equal(t, g.want("", ""), firstLine(err))
	})

	t.Run("no_client_credential", func(t *testing.T) {
		g := golden["no_client_credential"]
		newRESTRecorder(t, map[string]cannedResponse{"PUT /api/v1/clients/codex/binding": refuse(g.Status, g.restBody(t, "", "codex"))})
		_, _, err := runCLI(t, GetClientCommand, "table", "set-profile", "codex", "work-ro2")
		require.Error(t, err)
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
		require.Equal(t, g.want("", "codex"), firstLine(err))
	})

	t.Run("unknown_profile", func(t *testing.T) {
		g := golden["unknown_profile"]
		newRESTRecorder(t, map[string]cannedResponse{"PUT /api/v1/clients/cursor/binding": refuse(g.Status, g.restBody(t, "nope", ""))})
		_, _, err := runCLI(t, GetClientCommand, "table", "set-profile", "cursor", "nope")
		require.Error(t, err)
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
		require.Equal(t, g.want("nope", ""), firstLine(err))
	})

	t.Run("binding_bypassable_without_auth", func(t *testing.T) {
		g := golden["binding_bypassable_without_auth"]
		body := strings.TrimSuffix(g.restBody(t, "work-ro2", ""), "}") +
			`,"bindings":[{"client_id":"cursor","token_name":"client-cursor","profile":"work-ro2","mode":"locked"}],"fixes":[{"kind":"require_mcp_auth"},{"kind":"set_anonymous_profile","target":"work-ro2"}]}`
		newRESTRecorder(t, map[string]cannedResponse{"PUT /api/v1/clients/cursor/binding": refuse(g.Status, body)})
		_, _, err := runCLI(t, GetClientCommand, "table", "set-profile", "cursor", "work-ro2", "--lock")
		require.Error(t, err)
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
		require.Equal(t, g.want("work-ro2", ""), firstLine(err))
	})

	t.Run("mutation self-check: a differently worded refusal is caught", func(t *testing.T) {
		g := golden["no_client_credential"]
		newRESTRecorder(t, map[string]cannedResponse{"PUT /api/v1/clients/codex/binding": refuse(http.StatusConflict,
			`{"success":false,"error":"codex is not connected","code":"no_client_credential"}`)})
		_, _, err := runCLI(t, GetClientCommand, "table", "set-profile", "codex", "work-ro2")
		require.Error(t, err)
		require.NotEqual(t, g.want("", "codex"), firstLine(err))
	})
}
