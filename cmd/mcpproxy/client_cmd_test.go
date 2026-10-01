package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestClientCommandGoldens is T126's daemon-backed acceptance seam. The
// fixture makes the request path part of the assertion so `show` cannot
// accidentally fall back to the lightweight list endpoint.
func TestClientCommandGoldens(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/status" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/clients":
			_, _ = w.Write([]byte(`{"success":true,"data":{"clients":[{"id":"claude-code","display_name":"Claude Code","state":"connected_seen","last_seen":"2026-09-25T06:10:00Z","active_sessions":1,"calls_24h":38,"display_path":"~/.claude.json"}],"routing":{"routing_mode":"retrieve_tools"}}}`))
		case "/api/v1/clients/claude-code":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"claude-code","display_name":"Claude Code","state":"connected_seen","config_path":"/Users/test/.claude.json","reload_hint":"Run /mcp in Claude Code","sessions":[{"id":"S1","work_session_id":"ws-W1","started_at":"2026-09-25T06:00:00Z","last_activity":"2026-09-25T06:10:00Z"}]}}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(daemon.Close)
	withClientDaemon(t, daemon.URL)

	for _, tc := range []struct {
		name   string
		args   []string
		golden string
	}{
		{name: "list", args: []string{"list"}, golden: "client-list.golden"},
		{name: "show", args: []string{"show", "claude-code"}, golden: "client-show.golden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setOutputGlobals(t, "table", false)
			out := captureOutput(func() {
				cmd := GetClientCommand()
				cmd.SetArgs(tc.args)
				require.NoError(t, cmd.Execute())
			})
			assertClientGolden(t, tc.golden, out)
		})
	}

	t.Run("json-is-the-rest-data-object", func(t *testing.T) {
		setOutputGlobals(t, "json", false)
		out := captureOutput(func() { require.NoError(t, runClientList(nil, nil)) })
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		require.Equal(t, "retrieve_tools", got["routing"].(map[string]any)["routing_mode"])
		require.Equal(t, "Claude Code", got["clients"].([]any)[0].(map[string]any)["display_name"])
	})
}

// TestClientHelpJSONGoldens runs the actual help hook in a helper process. The
// production hook intentionally exits after writing JSON, so calling it in the
// test process would terminate the package's test run.
func TestClientHelpJSONGoldens(t *testing.T) {
	if os.Getenv("MCPPROXY_CLIENT_HELP_JSON_HELPER") == "1" {
		root := &cobra.Command{Use: "mcpproxy"}
		root.AddCommand(GetClientCommand())
		clioutput.SetupHelpJSON(root)
		root.SetArgs([]string{"client", os.Getenv("MCPPROXY_CLIENT_HELP_TARGET"), "--help-json"})
		require.NoError(t, root.Execute())
		return
	}
	for _, target := range []string{"list", "show"} {
		t.Run(target, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestClientHelpJSONGoldens$")
			cmd.Env = append(os.Environ(), "MCPPROXY_CLIENT_HELP_JSON_HELPER=1", "MCPPROXY_CLIENT_HELP_TARGET="+target)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			var payload map[string]any
			require.NoError(t, json.Unmarshal(out, &payload), string(out))
			require.Equal(t, target, payload["name"])
			assertClientGolden(t, "client-"+target+"-help-json.golden", string(out))
		})
	}
}

func withClientDaemon(t *testing.T, endpoint string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "mcp_config.json")
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.APIKey = "client-test-key"
	require.NoError(t, config.SaveConfig(cfg, configPath))
	previous := configFile
	configFile = configPath
	t.Cleanup(func() { configFile = previous })
	t.Setenv("MCPPROXY_TRAY_ENDPOINT", endpoint)
	t.Setenv("MCPPROXY_API_KEY", "client-test-key")
}

func assertClientGolden(t *testing.T, name, got string) {
	t.Helper()
	if os.Getenv("MCPPROXY_UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(filepath.Join("testdata", "cli109", name), []byte(got), 0o644))
	}
	want, err := os.ReadFile(filepath.Join("testdata", "cli109", name))
	if err != nil {
		t.Fatalf("read golden %s: %v\nactual:\n%s", name, err, got)
	}
	normalize := func(value string) string { return strings.ReplaceAll(value, "\r\n", "\n") }
	require.Equal(t, normalize(string(want)), normalize(got))
}
