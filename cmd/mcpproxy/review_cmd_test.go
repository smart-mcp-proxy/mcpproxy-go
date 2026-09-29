package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestReviewCommandGoldens exercises the CLI's complete review workflow against
// the daemon HTTP seam.  The fixture deliberately records requests: table text
// alone cannot prove --except is translated to security/approve's block field
// or that trusted-tool review uses tools/approve instead.
func TestReviewCommandGoldens(t *testing.T) {
	var requests []reviewRequest
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/status" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, reviewRequest{method: r.Method, path: r.URL.Path, body: string(body)})
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/review":
			_, _ = w.Write([]byte(`{"success":true,"data":{"count":1,"servers":[{"server":"filesystem","kind":"server_review","quarantined":true,"pending":0,"changed":0,"tier_counts":{"destructive":1},"scan":{"verdict":"clean"}}]}}`))
		case "/api/v1/servers/filesystem/review":
			_, _ = w.Write([]byte(`{"success":true,"data":{"server":{"name":"filesystem","quarantined":true},"tools":[{"name":"delete_0","tier":"destructive","approval_status":"pending","scan_verdict":"clean","description":"Delete a file\nThis cannot be undone","input_schema":{"type":"object"}}]}}`))
		case "/api/v1/servers/trusted/review":
			_, _ = w.Write([]byte(`{"success":true,"data":{"server":{"name":"trusted","quarantined":false},"tools":[]}}`))
		case "/api/v1/servers/filesystem/security/approve":
			_, _ = w.Write([]byte(`{"success":true,"data":{"status":"approved","server_name":"filesystem"}}`))
		case "/api/v1/servers/trusted/tools/approve":
			_, _ = w.Write([]byte(`{"success":true,"data":{"message":"Approved 1 tool for server trusted"}}`))
		case "/api/v1/servers/filesystem/security/reject":
			_, _ = w.Write([]byte(`{"success":true,"data":{"status":"rejected","server_name":"filesystem"}}`))
		case "/api/v1/servers/trusted/tools/block":
			_, _ = w.Write([]byte(`{"success":true,"data":{"message":"Blocked 1 tool for server trusted"}}`))
		default:
			t.Errorf("unexpected review request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer daemon.Close()

	withReviewDaemon(t, daemon.URL)
	setOutputGlobals(t, "table", false)

	for _, tc := range []struct {
		name   string
		args   []string
		golden string
	}{
		{"list", []string{"list"}, "review-list.golden"},
		{"show-full", []string{"show", "filesystem", "--full"}, "review-show-full.golden"},
		{"approve-except-force", []string{"approve", "filesystem", "--except", "delete_0", "--force", "--yes"}, "review-approve.golden"},
		{"approve-tools", []string{"approve", "trusted", "--tools", "write_0", "--yes"}, "review-approve-tools.golden"},
		{"reject-server", []string{"reject", "filesystem", "--yes"}, "review-reject.golden"},
		{"reject-tools", []string{"reject", "trusted", "--tools", "write_0", "--yes"}, "review-reject-tools.golden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := captureReviewOutput(t, func() error {
				cmd := GetReviewCommand()
				cmd.SetArgs(tc.args)
				return cmd.Execute()
			})
			assertReviewGolden(t, tc.golden, out+"\n")
		})
	}

	require.Len(t, requests, 8, "approve commands read review before writing")
	assertReviewRequest(t, requests, "POST", "/api/v1/servers/filesystem/security/approve", map[string]any{"force": true, "block": []any{"delete_0"}})
	assertReviewRequest(t, requests, "POST", "/api/v1/servers/trusted/tools/approve", map[string]any{"tools": []any{"write_0"}})
	assertReviewRequest(t, requests, "POST", "/api/v1/servers/trusted/tools/block", map[string]any{"tools": []any{"write_0"}})
}

func TestReviewCommandPromptsAndHonorsDecline(t *testing.T) {
	for _, action := range []string{"approve", "reject"} {
		t.Run(action, func(t *testing.T) {
			var prompt string
			cmd := newReviewCommand(func(message string) (bool, error) {
				prompt = message
				return false, nil
			})
			cmd.SetArgs([]string{action, "filesystem"})
			require.NoError(t, cmd.Execute())
			require.Contains(t, prompt, "filesystem")
			require.Contains(t, strings.ToLower(prompt), action)
		})
	}
}

type reviewRequest struct{ method, path, body string }

func withReviewDaemon(t *testing.T, endpoint string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "mcp_config.json")
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.APIKey = "review-test-key"
	require.NoError(t, config.SaveConfig(cfg, configPath))
	previous := configFile
	configFile = configPath
	t.Cleanup(func() { configFile = previous })
	t.Setenv("MCPPROXY_TRAY_ENDPOINT", endpoint)
	t.Setenv("MCPPROXY_API_KEY", "review-test-key")
}

func assertReviewGolden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", "cli109", name))
	if err != nil {
		t.Fatalf("read golden %s: %v\nactual:\n%s", name, err, got)
	}
	require.Equal(t, string(want), got)
}

func assertReviewRequest(t *testing.T, requests []reviewRequest, method, path string, want map[string]any) {
	t.Helper()
	for _, request := range requests {
		if request.method != method || request.path != path {
			continue
		}
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(request.body), &got))
		require.Equal(t, want, got)
		return
	}
	t.Fatalf("missing request %s %s in %#v", method, path, requests)
}

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
