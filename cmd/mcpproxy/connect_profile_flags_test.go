package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

// connectTestEnv isolates HOME (so the client config paths are under a temp
// dir), points the CLI at a config file with the given settings, and makes sure
// no real daemon can be reached.
func connectTestEnv(t *testing.T, requireAuth bool) (home string, cfg *config.Config) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("MCPPROXY_API_KEY", "")
	t.Setenv("MCPPROXY_TRAY_ENDPOINT", "")

	cfg = config.DefaultConfig()
	cfg.DataDir = filepath.Join(home, "data")
	require.NoError(t, os.MkdirAll(cfg.DataDir, 0o755))
	cfg.Listen = "127.0.0.1:1" // nothing listens here: no daemon
	cfg.APIKey = "cli-test-admin-key"
	cfg.RequireMCPAuth = requireAuth
	cfg.Profiles = []config.ProfileConfig{
		{Name: "ro", Servers: []string{"a"}},
		{Name: "work", Servers: []string{"a", "b"}},
	}
	configPath := filepath.Join(home, "mcp_config.json")
	require.NoError(t, config.SaveConfig(cfg, configPath))
	prev := configFile
	configFile = configPath
	t.Cleanup(func() { configFile = prev })
	setOutputGlobals(t, "table", false)
	resetConnectFlags(t)
	return home, cfg
}

// resetConnectFlags restores the package-level flag variables the command
// binds, so one test's flags never leak into the next.
func resetConnectFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		connectProfile, connectLock, connectSwitchable, connectKeyless = "all", false, false, false
		connectList, connectAll, connectForce, connectServerName = false, false, false, ""
	})
}

func seedCursor(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".cursor", "mcp.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o644))
	return path
}

func runConnectArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var runErr error
	out := captureStdout(t, func() {
		cmd := GetConnectCommand()
		cmd.SetArgs(args)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		runErr = cmd.Execute()
	})
	return out, runErr
}

var maskedCredentialLine = regexp.MustCompile(`(?m)^Credential: mcp_cli_•••• \(token client-cursor, profile ro, locked\)$`)

// T041: the four flags exist and --help-json lists them.
func TestConnectProfileFlags_HelpJSONListsTheFlags(t *testing.T) {
	if os.Getenv("MCPPROXY_CONNECT_HELP_JSON_HELPER") == "1" {
		root := &cobra.Command{Use: "mcpproxy"}
		root.AddCommand(GetConnectCommand())
		clioutput.SetupHelpJSON(root)
		root.SetArgs([]string{"connect", "--help-json"})
		require.NoError(t, root.Execute())
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestConnectProfileFlags_HelpJSONListsTheFlags$")
	cmd.Env = append(os.Environ(), "MCPPROXY_CONNECT_HELP_JSON_HELPER=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	var payload struct {
		Flags []struct {
			Name string `json:"name"`
		} `json:"flags"`
	}
	require.NoError(t, json.Unmarshal(out, &payload), string(out))
	names := map[string]bool{}
	for _, f := range payload.Flags {
		names[f.Name] = true
	}
	for _, want := range []string{"profile", "lock", "switchable", "keyless"} {
		require.True(t, names[want], "--help-json must list --%s, got %v", want, names)
	}
}

func TestConnectProfileFlags_UsageErrors(t *testing.T) {
	connectTestEnv(t, true)
	_, err := runConnectArgs(t, "cursor", "--lock", "--switchable")
	require.Error(t, err, "--lock and --switchable are mutually exclusive")
	require.Contains(t, err.Error(), "none of the others can be")

	_, err = runConnectArgs(t, "cursor", "--keyless", "--profile", "ro")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--keyless cannot be combined")
	require.Equal(t, ExitCodeGeneralError, classifyError(err))

	_, err = runConnectArgs(t, "cursor", "--lock", "--profile", "all")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--lock needs a profile")
}

// Offline (no daemon), auth on: the write carries a client credential, never
// the admin key, and the masked credential line is printed between the result
// line and the 109-b Config:/Next: lines.
func TestConnectProfileFlags_OfflineWritesAMaskedClientCredential(t *testing.T) {
	home, _ := connectTestEnv(t, true)
	path := seedCursor(t, home)

	out, err := runConnectArgs(t, "cursor", "--profile", "ro", "--lock")
	require.NoError(t, err, out)

	require.Regexp(t, maskedCredentialLine, out)
	credAt := strings.Index(out, "Credential:")
	require.Greater(t, credAt, strings.Index(out, "\n"), "the credential line follows the result line")
	require.Greater(t, strings.Index(out, "Config: "), credAt, "and precedes Config:")
	require.Contains(t, out, "Next: ", "109-b's reload hint line is kept")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "cli-test-admin-key", "the admin API key is never written")
	require.Regexp(t, `mcp_cli_[0-9a-f]{64}`, string(raw))
	require.NotContains(t, out, string(regexp.MustCompile(`mcp_cli_[0-9a-f]{64}`).Find(raw)), "the secret is never printed")
}

func TestConnectProfileFlags_AllServersAndKeyless(t *testing.T) {
	home, _ := connectTestEnv(t, true)
	seedCursor(t, home)
	out, err := runConnectArgs(t, "cursor")
	require.NoError(t, err, out)
	require.Contains(t, out, "Credential: mcp_cli_•••• (token client-cursor, all servers, switchable)")

	// keyless is refused while auth is on
	_, err = runConnectArgs(t, "cursor", "--force", "--keyless")
	require.Error(t, err)
	require.Contains(t, err.Error(), "keyless connect is only possible while require_mcp_auth is off")
	require.Equal(t, ExitCodeGeneralError, classifyError(err))

	home2, _ := connectTestEnv(t, false)
	seedCursor(t, home2)
	out, err = runConnectArgs(t, "cursor", "--keyless")
	require.NoError(t, err, out)
	require.Contains(t, out, "Credential: none (keyless)")
}

// FR-008a offline: a named binding while auth is off is refused with the 409
// text and the fix; the config is untouched and nothing is minted.
func TestConnectProfileFlags_OfflineRefusesABypassableBinding(t *testing.T) {
	home, _ := connectTestEnv(t, false)
	path := seedCursor(t, home)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	out, err := runConnectArgs(t, "cursor", "--profile", "ro", "--lock")
	require.Error(t, err, out)
	require.Contains(t, err.Error(), "a client bound to profile ro could escape it by omitting its credential while require_mcp_auth is off")
	require.Contains(t, err.Error(), "Fixes:")
	require.Contains(t, err.Error(), "set require_mcp_auth: true")
	require.Equal(t, ExitCodeGeneralError, classifyError(err), "the refusal exits 1")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	entries, _ := filepath.Glob(path + ".bak*")
	require.Empty(t, entries, "no backup for a refused connect")
}

// fakeConnectDaemon answers the status probe and POST /api/v1/connect/cursor.
func fakeConnectDaemon(t *testing.T, status int, response string, gotBody *map[string]any) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
		case r.URL.Path == "/api/v1/connect/cursor" && r.Method == http.MethodPost:
			if gotBody != nil {
				_ = json.NewDecoder(r.Body).Decode(gotBody)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(response))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MCPPROXY_TRAY_ENDPOINT", srv.URL)
	t.Setenv("MCPPROXY_API_KEY", "cli-test-admin-key")
}

func TestConnectProfileFlags_DaemonPathSendsTheIntentAndPrintsTheResult(t *testing.T) {
	connectTestEnv(t, true)
	var body map[string]any
	fakeConnectDaemon(t, http.StatusOK, `{"success":true,"data":{"success":true,"client":"cursor","config_path":"/h/.cursor/mcp.json","display_path":"~/.cursor/mcp.json","server_name":"mcpproxy","action":"created","message":"MCPProxy registered in Cursor as \"mcpproxy\"","reload_hint":"Restart Cursor","credential":"mcp_cli_••••","token_name":"client-cursor","profile":"ro","mode":"locked"}}`, &body)

	out, err := runConnectArgs(t, "cursor", "--profile", "ro", "--lock")
	require.NoError(t, err, out)
	require.Equal(t, "ro", body["profile"])
	require.Equal(t, "locked", body["mode"])
	require.Regexp(t, maskedCredentialLine, out)
	require.Contains(t, out, "Config: ~/.cursor/mcp.json")
	require.Contains(t, out, "Next: Restart Cursor")

	// json output carries the result fields
	setOutputGlobals(t, "json", false)
	out, err = runConnectArgs(t, "cursor", "--profile", "ro", "--lock")
	require.NoError(t, err, out)
	var res map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &res), out)
	require.Equal(t, "mcp_cli_••••", res["credential"])
	require.Equal(t, "client-cursor", res["token_name"])
}

// A reconnect without --profile sends no profile: the daemon keeps the
// client's existing binding instead of silently widening it.
func TestConnectProfileFlags_UnspecifiedProfileIsNotSent(t *testing.T) {
	connectTestEnv(t, true)
	var body map[string]any
	fakeConnectDaemon(t, http.StatusOK, `{"success":true,"data":{"success":true,"client":"cursor","action":"updated","message":"ok","credential":"mcp_cli_••••","token_name":"client-cursor","profile":"ro","mode":"locked","rotation":"finalized"}}`, &body)
	_, err := runConnectArgs(t, "cursor", "--force")
	require.NoError(t, err)
	require.NotContains(t, body, "profile")
	require.NotContains(t, body, "mode")
	require.Equal(t, true, body["force"])
}

func TestConnectProfileFlags_DaemonGuardRefusalPrintsBothFixesAndExits1(t *testing.T) {
	connectTestEnv(t, false)
	fakeConnectDaemon(t, http.StatusConflict, `{"success":false,"error":"a client bound to profile ro could escape it by omitting its credential while require_mcp_auth is off","code":"binding_bypassable_without_auth","bindings":[{"client_id":"cursor","token_name":"client-cursor","profile":"ro","mode":"locked"}],"fixes":[{"kind":"require_mcp_auth"},{"kind":"set_anonymous_profile","target":"ro"}]}`, nil)

	_, err := runConnectArgs(t, "cursor", "--profile", "ro", "--lock")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	want := "a client bound to profile ro could escape it by omitting its credential while require_mcp_auth is off\n" +
		"Fixes:\n" +
		"  - set require_mcp_auth: true (config or Settings → Security)\n" +
		"  - mcpproxy profile anonymous ro"
	require.Equal(t, want, err.Error())
}

func TestConnectProfileFlags_DaemonNameConflictPrintsRemediation(t *testing.T) {
	connectTestEnv(t, true)
	fakeConnectDaemon(t, http.StatusConflict, `{"success":false,"error":"token name client-cursor is held by a regular agent token","conflicting_token":"client-cursor","remediation":"revoke or delete token client-cursor, then connect again"}`, nil)
	_, err := runConnectArgs(t, "cursor")
	require.Error(t, err)
	require.Equal(t, "token name client-cursor is held by a regular agent token\nrevoke or delete token client-cursor, then connect again", err.Error())
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
}

func TestConnectProfileFlags_DaemonAlreadyExistsIsAResultNotAnError(t *testing.T) {
	connectTestEnv(t, true)
	fakeConnectDaemon(t, http.StatusConflict, `{"success":false,"action":"already_exists","error":"Cursor already has an entry","data":{"success":false,"client":"cursor","action":"already_exists","message":"Cursor already has an entry named \"mcpproxy\"; use force=true to overwrite"}}`, nil)
	out, err := runConnectArgs(t, "cursor")
	require.NoError(t, err)
	require.Contains(t, out, "Failed: Cursor already has an entry")
}

func TestConnectCredentialLine(t *testing.T) {
	require.Equal(t, "", connectCredentialLine(&connect.ConnectResult{}))
	require.Equal(t, "Credential: none (keyless)", connectCredentialLine(&connect.ConnectResult{Keyless: true}))
	require.Equal(t, "Credential: mcp_cli_•••• (token client-cursor, profile ro, locked)",
		connectCredentialLine(&connect.ConnectResult{Credential: "mcp_cli_••••", TokenName: "client-cursor", Profile: "ro", Mode: "locked"}))
	require.Equal(t, "Credential: mcp_cli_•••• (token client-cursor, all servers, switchable)",
		connectCredentialLine(&connect.ConnectResult{Credential: "mcp_cli_••••", TokenName: "client-cursor", Mode: "switchable"}))
}
