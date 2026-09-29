package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestFormatReviewResponseTableHonorsFull(t *testing.T) {
	raw := []byte(`{"data":{"server":{"name":"github"},"tools":[{"name":"create_issue","tier":"write","approval_status":"pending","scan_verdict":"warnings","description":"Create an issue\nwith labels","input_schema":{"type":"object"},"output_schema":{"type":"string"},"diff":"@@ -1 +1 @@\n-old\n+new"}]}}`)

	compact := captureReviewOutput(t, func() error { return formatReviewResponse("table", raw, false) })
	require.Contains(t, compact, "TOOL")
	require.Contains(t, compact, "create_issue")
	require.Contains(t, compact, "Create an issue")
	require.NotContains(t, compact, "with labels")
	require.NotContains(t, compact, "from the server, not verified")

	full := captureReviewOutput(t, func() error { return formatReviewResponse("table", raw, true) })
	require.Contains(t, full, "with labels")
	require.Contains(t, full, "from the server, not verified")
	require.Contains(t, full, "Input schema")
	require.Contains(t, full, "Output schema")
	require.Contains(t, full, "@@ -1 +1 @@")
}

func TestFormatReviewResponseTableWritesActionResult(t *testing.T) {
	for _, test := range []struct {
		raw  []byte
		want string
	}{
		{[]byte(`{"data":{"status":"approved","server_name":"github"}}`), "Approved server github"},
		{[]byte(`{"data":{"message":"Blocked 2 tools for server github"}}`), "Blocked 2 tools for server github"},
	} {
		output := captureReviewOutput(t, func() error { return formatReviewResponse("table", test.raw, false) })
		require.NotContains(t, output, "Server: <nil>")
		require.NotContains(t, output, "No results found")
		require.Contains(t, output, test.want)
	}
}

func TestFormatReviewResponseTableQueueColumns(t *testing.T) {
	raw := []byte(`{"data":{"servers":[{"server":"github","kind":"server_review","quarantined":true,"pending":2,"changed":1,"tier_counts":{"write":2},"scan":{"verdict":"warnings"}}]}}`)

	output := captureReviewOutput(t, func() error { return formatReviewResponse("table", raw, false) })
	for _, want := range []string{"SERVER", "QUARANTINED", "PENDING", "CHANGED", "TIERS", "SCAN", "github"} {
		require.Contains(t, output, want)
	}
}

func TestReviewAliasHelpPointsToReviewWorkflow(t *testing.T) {
	for _, command := range []*cobra.Command{
		newToolsApproveCmd(),
		newToolsRejectCmd(),
		newSecurityApproveCmd(),
		newSecurityRejectCmd(),
		upstreamApproveCmd,
	} {
		require.Contains(t, command.Long, "mcpproxy review", command.Use)
	}
}

// The CLI's JSON mode is a deliberately thin wrapper over the review route.
// Keep that boundary in the regression: a future formatter must not re-read a
// configured server or otherwise turn a redacted response back into a secret.
func TestReviewShowJSONNeverRevealsComposerRedaction(t *testing.T) {
	const apiKey = "review-cli-key"
	const secret = "secret123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, apiKey, r.Header.Get("X-API-Key"))
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/servers/alpha/review":
			_, _ = w.Write([]byte(`{"success":true,"data":{"server":{"name":"alpha","command":"server --token ••••23 (9 chars)","url":"https://example.test/mcp?api_key=%E2%80%A2%E2%80%A2%E2%80%A2%E2%80%A223%20%289%20chars%29"},"tools":[]}}`))
		default:
			t.Fatalf("unexpected CLI request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "mcp_config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"listen":"127.0.0.1:0","api_key":"`+apiKey+`"}`), 0o600))
	previousConfigFile := configFile
	configFile = configPath
	t.Cleanup(func() { configFile = previousConfigFile })
	t.Setenv("MCPPROXY_TRAY_ENDPOINT", server.URL)
	t.Setenv("MCPPROXY_OUTPUT", "json")

	output := captureReviewOutput(t, func() error { return runReviewRead("/api/v1/servers/alpha/review") })
	require.NotContains(t, output, secret)
	require.Contains(t, output, "••••23")
}

func captureReviewOutput(t *testing.T, fn func() error) string {
	t.Helper()
	previous := os.Stdout
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = previous })
	require.NoError(t, fn())
	require.NoError(t, writer.Close())
	output, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	return strings.TrimSpace(string(output))
}
