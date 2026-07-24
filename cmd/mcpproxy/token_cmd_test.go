package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

func TestGetTokenCommand(t *testing.T) {
	cmd := GetTokenCommand()
	assert.Equal(t, "token", cmd.Use)
	assert.NotEmpty(t, cmd.Short)

	// Check subcommands
	subCmds := cmd.Commands()
	names := make(map[string]bool)
	for _, sub := range subCmds {
		names[sub.Name()] = true
	}

	assert.True(t, names["create"], "should have 'create' subcommand")
	assert.True(t, names["list"], "should have 'list' subcommand")
	assert.True(t, names["show"], "should have 'show' subcommand")
	assert.True(t, names["revoke"], "should have 'revoke' subcommand")
	assert.True(t, names["delete"], "should have 'delete' subcommand")
	assert.True(t, names["regenerate"], "should have 'regenerate' subcommand")
}

func TestGetTokenCommand_IncludesDelete(t *testing.T) {
	cmd := GetTokenCommand()

	// Find the delete subcommand
	var deleteCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "delete" {
			deleteCmd = sub
			break
		}
	}

	assert.NotNil(t, deleteCmd, "delete subcommand must exist")
	assert.Equal(t, "delete <name>", deleteCmd.Use)
	assert.Contains(t, deleteCmd.Short, "delete")
	assert.NotEmpty(t, deleteCmd.Long)
	assert.Contains(t, deleteCmd.Aliases, "rm", "delete should alias 'rm'")

	// Verify it requires exactly 1 argument
	assert.Error(t, deleteCmd.Args(deleteCmd, []string{}), "should reject zero args")
	assert.NoError(t, deleteCmd.Args(deleteCmd, []string{"my-token"}), "should accept one arg")
	assert.Error(t, deleteCmd.Args(deleteCmd, []string{"a", "b"}), "should reject two args")
}

func TestGetTokenCommand_IncludesRegenerate(t *testing.T) {
	cmd := GetTokenCommand()

	// Find the regenerate subcommand
	var regenCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "regenerate" {
			regenCmd = sub
			break
		}
	}

	assert.NotNil(t, regenCmd, "regenerate subcommand must exist")
	assert.Equal(t, "regenerate <name>", regenCmd.Use)
	assert.Contains(t, regenCmd.Short, "Regenerate")
	assert.NotEmpty(t, regenCmd.Long)

	// Verify it requires exactly 1 argument
	assert.Error(t, regenCmd.Args(regenCmd, []string{}), "should reject zero args")
	assert.NoError(t, regenCmd.Args(regenCmd, []string{"my-token"}), "should accept one arg")
	assert.Error(t, regenCmd.Args(regenCmd, []string{"a", "b"}), "should reject two args")
}

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"github,gitlab", []string{"github", "gitlab"}},
		{" github , gitlab ", []string{"github", "gitlab"}},
		{"read,write,destructive", []string{"read", "write", "destructive"}},
		{"*", []string{"*"}},
		{"", []string{}},
	}

	for _, tt := range tests {
		result := splitAndTrim(tt.input)
		assert.Equal(t, tt.expected, result, "splitAndTrim(%q)", tt.input)
	}
}

func TestGetMapString(t *testing.T) {
	m := map[string]interface{}{
		"name":   "deploy-bot",
		"count":  42,
		"nested": map[string]interface{}{"key": "value"},
	}

	assert.Equal(t, "deploy-bot", getMapString(m, "name"))
	assert.Equal(t, "", getMapString(m, "count"))       // not a string
	assert.Equal(t, "", getMapString(m, "nonexistent")) // missing key
}

func TestJoinInterfaceSlice(t *testing.T) {
	m := map[string]interface{}{
		"servers": []interface{}{"github", "gitlab", "bitbucket"},
	}

	assert.Equal(t, "github,gitlab,bitbucket", joinInterfaceSlice(m, "servers", 0))
	// String is exactly 23 chars, so maxLen=23 doesn't truncate
	assert.Equal(t, "github,gitlab,bitbucket", joinInterfaceSlice(m, "servers", 23))
	// maxLen=20 triggers truncation: result[:17] + "..." = 20 chars
	assert.Equal(t, "github,gitlab,bit...", joinInterfaceSlice(m, "servers", 20))
	assert.Equal(t, "", joinInterfaceSlice(m, "missing", 0))
}

func TestTokenCreateCmd_RequiredFlags(t *testing.T) {
	cmd := newTokenCreateCmd()

	// Verify required flags are defined
	nameFlag := cmd.Flags().Lookup("name")
	assert.NotNil(t, nameFlag, "should have --name flag")

	serversFlag := cmd.Flags().Lookup("servers")
	assert.NotNil(t, serversFlag, "should have --servers flag")

	permsFlag := cmd.Flags().Lookup("permissions")
	assert.NotNil(t, permsFlag, "should have --permissions flag")

	expiresFlag := cmd.Flags().Lookup("expires")
	assert.NotNil(t, expiresFlag, "should have --expires flag")
	assert.Equal(t, "30d", expiresFlag.DefValue, "default expires should be 30d")
}

// GH #897 verification follow-up: the REST API wraps token responses in the
// standard envelope {"success":true,"data":{...}}, but the CLI table paths
// read top-level keys — so `token list` always printed "No agent tokens
// configured" and `token create` never displayed the minted token.
// parseTokenAPIResponse must unwrap the envelope (and tolerate the bare
// legacy shape).
func TestParseTokenAPIResponse(t *testing.T) {
	t.Run("unwraps success envelope", func(t *testing.T) {
		body := []byte(`{"success":true,"data":{"tokens":[{"name":"a"}],"token":"mcp_agt_x"}}`)
		result, err := parseTokenAPIResponse(body)
		assert.NoError(t, err)
		assert.Equal(t, "mcp_agt_x", result["token"])
		tokens, ok := result["tokens"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, tokens, 1)
	})

	t.Run("passes through bare legacy shape", func(t *testing.T) {
		body := []byte(`{"tokens":[],"token":"mcp_agt_y"}`)
		result, err := parseTokenAPIResponse(body)
		assert.NoError(t, err)
		assert.Equal(t, "mcp_agt_y", result["token"])
	})

	t.Run("invalid json errors", func(t *testing.T) {
		_, err := parseTokenAPIResponse([]byte("not json"))
		assert.Error(t, err)
	})
}

// --- GH #897 / PR #907 handler-level regression tests ---
//
// These drive the real run* handlers against an httptest daemon (via the
// MCPPROXY_TRAY_ENDPOINT seam in daemonEndpoint) with responses marshaled
// through contracts.NewSuccessResponse — the exact envelope the httpapi
// handlers emit — so CLI/handler drift like the pre-#907 "No agent tokens
// configured" bug cannot regress silently. They mutate package globals, so
// no t.Parallel.

func newTokenTestDaemon(t *testing.T, tokensHandler http.HandlerFunc) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"running": true}})
	})
	mux.HandleFunc("/api/v1/tokens", tokensHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "cfg.json")
	cfgJSON := fmt.Sprintf(`{"listen":"127.0.0.1:0","data_dir":%q,"api_key":"test-key","mcpServers":[]}`, filepath.Join(tmp, "data"))
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MCPPROXY_TRAY_ENDPOINT", server.URL)
	t.Setenv("MCPPROXY_OUTPUT", "")
	oldCfgPath := tokenConfigPath
	tokenConfigPath = cfgPath
	t.Cleanup(func() { tokenConfigPath = oldCfgPath })
	return server.URL
}

func captureTokenStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fnErr := fn()
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if fnErr != nil {
		t.Fatalf("handler returned error: %v\noutput so far:\n%s", fnErr, out)
	}
	return string(out)
}

func TestRunTokenList_TableOutput(t *testing.T) {
	newTokenTestDaemon(t, func(w http.ResponseWriter, _ *http.Request) {
		resp := contracts.NewSuccessResponse(map[string]interface{}{
			"tokens": []map[string]interface{}{{
				"name": "qa-list-token", "token_prefix": "mcp_agt_ab12",
				"allowed_servers": []string{"*"}, "permissions": []string{"read"},
				"revoked": false, "expires_at": "2026-08-23T00:00:00Z", "created_at": "2026-07-24T00:00:00Z",
			}},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	out := captureTokenStdout(t, func() error { return runTokenList(nil, nil) })
	assert.Contains(t, out, "qa-list-token", "table must list the token")
	assert.Contains(t, out, "mcp_agt_ab12")
	assert.NotContains(t, out, "No agent tokens configured", "pre-#907 envelope bug must not regress")
}

func TestRunTokenCreate_DisplaysMintedToken(t *testing.T) {
	const minted = "mcp_agt_deadbeefcafe0123456789"
	newTokenTestDaemon(t, func(w http.ResponseWriter, _ *http.Request) {
		resp := contracts.NewSuccessResponse(map[string]interface{}{
			"name": "qa-create", "token": minted,
			"allowed_servers": []string{"*"}, "permissions": []string{"read"},
			"expires_at": "2026-08-23T00:00:00Z",
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	})

	oldName, oldServers, oldPerms, oldExpires := tokenName, tokenServers, tokenPermissions, tokenExpires
	tokenName, tokenServers, tokenPermissions, tokenExpires = "qa-create", "*", "read", "30d"
	defer func() {
		tokenName, tokenServers, tokenPermissions, tokenExpires = oldName, oldServers, oldPerms, oldExpires
	}()

	out := captureTokenStdout(t, func() error { return runTokenCreate(nil, nil) })
	assert.Contains(t, out, minted, "create must display the minted token — it is shown only once")
}

// GH #897: the config.Load() branch of loadTokenConfig (no --config flag) is
// the exact reported user flow. Sandbox HOME so config.Load touches only a
// temp dir, then assert the global --data-dir flag overrides the default.
func TestLoadTokenConfig_LoadBranchHonorsDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows os.UserHomeDir
	t.Setenv("MCPPROXY_API_KEY", "")

	oldCfgPath, oldDataDir := tokenConfigPath, dataDir
	tokenConfigPath = ""
	flagDir := filepath.Join(home, "flag-data")
	dataDir = flagDir
	defer func() { tokenConfigPath, dataDir = oldCfgPath, oldDataDir }()

	cfg, err := loadTokenConfig()
	if err != nil {
		t.Fatalf("loadTokenConfig via config.Load(): %v", err)
	}
	if cfg.DataDir != flagDir {
		t.Errorf("config.Load() branch DataDir = %q, want --data-dir %q (GH #897 regression)", cfg.DataDir, flagDir)
	}
}

func TestTokenCommand_ConfigFlagRegistered(t *testing.T) {
	cmd := GetTokenCommand()
	flag := cmd.PersistentFlags().Lookup("config")
	if assert.NotNil(t, flag, "token command must have persistent --config flag") {
		assert.Equal(t, "c", flag.Shorthand)
	}
}
