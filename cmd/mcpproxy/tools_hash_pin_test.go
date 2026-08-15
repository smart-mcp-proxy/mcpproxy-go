package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T020 (Spec 098 FR-011): `mcpproxy tools list -o json` is the CLI half of the
// hash-pin authoring surface — the value it prints under "hash" is pasted
// straight into `mcpproxy tools preflight --pin <id>=<hash>`. The renderers
// pass the daemon payload through untouched, so these tests are the guard
// against a future typed-struct refactor silently dropping the field.

func captureToolsOutput(t *testing.T, format string, run func() error) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	oldFormat, oldJSON := globalOutputFormat, globalJSONOutput
	globalOutputFormat, globalJSONOutput = format, false
	defer func() { globalOutputFormat, globalJSONOutput = oldFormat, oldJSON }()

	runErr := run()

	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	require.NoError(t, runErr)
	return buf.String()
}

func toolsWithPin() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":            "create_issue",
			"server_name":     "github",
			"description":     "Create a new GitHub issue",
			"approval_status": "approved",
			"hash":            "sha256/v3:abc123",
		},
		{
			// No stored hash: the field is simply absent, never a placeholder.
			"name":        "no_record",
			"server_name": "github",
			"description": "Never approved",
		},
	}
}

func TestToolsList_JSONCarriesHashPin(t *testing.T) {
	for name, run := range map[string]func() error{
		"global":     func() error { return outputGlobalTools(toolsWithPin()) },
		"per-server": func() error { return outputTools(toolsWithPin(), nil) },
	} {
		t.Run(name, func(t *testing.T) {
			out := captureToolsOutput(t, "json", run)

			var parsed []map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(out), &parsed))
			require.Len(t, parsed, 2)
			assert.Equal(t, "sha256/v3:abc123", parsed[0]["hash"],
				"the pin an operator copies into --pin must survive JSON rendering")
			assert.NotContains(t, parsed[1], "hash")
		})
	}
}

// The pin is a long opaque string; the human table stays as it was (#938
// columns) so it is not pushed off screen. JSON is the authoring surface.
func TestToolsList_TableOmitsHashPin(t *testing.T) {
	out := captureToolsOutput(t, "table", func() error { return outputGlobalTools(toolsWithPin()) })
	assert.NotContains(t, out, "sha256/v3:abc123")
	assert.Contains(t, out, "create_issue")
}
