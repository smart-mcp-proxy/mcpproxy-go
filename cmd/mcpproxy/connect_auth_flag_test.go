//go:build !server

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

// Spec 132 PR-3: `mcpproxy connect <client> --auth key|oauth`.

func seedClaudeCode(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".claude.json")
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o644))
	return path
}

// connectAuthEnv is connectTestEnv with browser login switched on or off in the
// CLI's config file.
func connectAuthEnv(t *testing.T, oauthOn bool) (home string) {
	t.Helper()
	home, cfg := connectTestEnv(t, true)
	cfg.MCPOAuthEnabled = oauthOn
	require.NoError(t, config.SaveConfig(cfg, configFile))
	return home
}

func TestConnectAuthFlag_HelpJSONListsIt(t *testing.T) {
	if os.Getenv("MCPPROXY_CONNECT_AUTH_HELP_JSON_HELPER") == "1" {
		root := &cobra.Command{Use: "mcpproxy"}
		root.AddCommand(GetConnectCommand())
		clioutput.SetupHelpJSON(root)
		root.SetArgs([]string{"connect", "--help-json"})
		require.NoError(t, root.Execute())
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestConnectAuthFlag_HelpJSONListsIt$")
	cmd.Env = append(os.Environ(), "MCPPROXY_CONNECT_AUTH_HELP_JSON_HELPER=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	var payload struct {
		Flags []struct {
			Name    string `json:"name"`
			Default string `json:"default"`
		} `json:"flags"`
	}
	require.NoError(t, json.Unmarshal(out, &payload), string(out))
	found := false
	for _, f := range payload.Flags {
		if f.Name == "auth" {
			found = true
			require.Equal(t, "key", f.Default)
		}
	}
	require.True(t, found, "--help-json must list --auth: %s", out)
}

func TestConnectAuthFlag_OAuthWritesOnlyTheAddress(t *testing.T) {
	home := connectAuthEnv(t, true)
	path := seedClaudeCode(t, home)

	out, err := runConnectArgs(t, "claude-code", "--auth", "oauth")
	require.NoError(t, err, out)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(raw)
	require.Contains(t, body, "/mcp/oauth/claude-code")
	for _, bad := range []string{"headers", "X-API-Key", "mcp_cli_", "mcp_oat_", "cli-test-admin-key"} {
		require.NotContains(t, body, bad)
	}
	require.NotContains(t, out, "Credential:", "no credential line for a browser login")
	require.Contains(t, out, "Sign-in: browser")
	require.Contains(t, out, "does not revoke")
	require.Contains(t, out, "Next: ")
}

func TestConnectAuthFlag_KeyIsTheDefault(t *testing.T) {
	home := connectAuthEnv(t, true)
	path := seedClaudeCode(t, home)
	out, err := runConnectArgs(t, "claude-code", "--auth", "key")
	require.NoError(t, err, out)
	require.Contains(t, out, "Credential: mcp_cli_••••")
	require.NotContains(t, out, "Sign-in:")
	raw, _ := os.ReadFile(path)
	require.NotContains(t, string(raw), "/mcp/oauth")
}

func TestConnectAuthFlag_Refusals(t *testing.T) {
	t.Run("feature off", func(t *testing.T) {
		home := connectAuthEnv(t, false)
		path := seedClaudeCode(t, home)
		out, err := runConnectArgs(t, "claude-code", "--auth", "oauth")
		require.Error(t, err, out)
		require.Contains(t, err.Error(), "mcp_oauth_enabled")
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
		raw, _ := os.ReadFile(path)
		require.Equal(t, "{}\n", string(raw))
	})
	t.Run("unsupported client", func(t *testing.T) {
		home := connectAuthEnv(t, true)
		path := seedCursor(t, home)
		out, err := runConnectArgs(t, "cursor", "--auth", "oauth")
		require.Error(t, err, out)
		require.Contains(t, err.Error(), "does not support browser login")
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
		raw, _ := os.ReadFile(path)
		require.Equal(t, "{}\n", string(raw))
	})
	t.Run("unknown value", func(t *testing.T) {
		connectAuthEnv(t, true)
		_, err := runConnectArgs(t, "claude-code", "--auth", "magic")
		require.Error(t, err)
		require.Contains(t, err.Error(), "--auth must be key or oauth")
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
	})
	t.Run("combined with a binding", func(t *testing.T) {
		connectAuthEnv(t, true)
		for _, extra := range [][]string{{"--keyless"}, {"--profile", "ro"}, {"--lock", "--profile", "ro"}, {"--switchable"}} {
			_, err := runConnectArgs(t, append([]string{"claude-code", "--auth", "oauth"}, extra...)...)
			require.Error(t, err, strings.Join(extra, " "))
			require.Contains(t, err.Error(), "--auth oauth cannot be combined")
		}
	})
}

// A daemon refusal (HTTP 400) of an oauth write exits 1 and prints its text.
func TestConnectAuthFlag_DaemonSendsAuthAndPrintsRefusal(t *testing.T) {
	connectAuthEnv(t, true)
	var body map[string]any
	fakeConnectDaemon(t, 400, `{"success":false,"error":"browser login is off: set mcp_oauth_enabled to true first"}`, &body)
	_, err := runConnectArgs(t, "cursor", "--auth", "oauth")
	require.Error(t, err)
	require.Equal(t, "oauth", body["auth"])
	require.Contains(t, err.Error(), "mcp_oauth_enabled")
	require.Equal(t, ExitCodeGeneralError, classifyError(err))

	// The default sends no auth at all.
	body = nil
	fakeConnectDaemon(t, 200, `{"success":true,"data":{"success":true,"client":"cursor","action":"created","message":"ok","credential":"mcp_cli_••••","token_name":"client-cursor","mode":"switchable"}}`, &body)
	_, err = runConnectArgs(t, "cursor")
	require.NoError(t, err)
	require.NotContains(t, body, "auth")
}

func TestConnectAuthLine(t *testing.T) {
	require.Equal(t, "", connectAuthLine(&connect.ConnectResult{}))
	require.Contains(t, connectAuthLine(&connect.ConnectResult{Auth: "oauth"}), "Sign-in: browser")
}
