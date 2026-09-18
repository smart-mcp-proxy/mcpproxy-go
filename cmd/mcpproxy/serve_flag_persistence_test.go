package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// newServeFlagTestCmd builds a cobra command carrying the `serve` flags that
// loadConfig reads, bound to the same package globals as the real command.
func newServeFlagTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "serve"}
	cmd.Flags().StringVarP(&listen, "listen", "l", "", "")
	cmd.Flags().StringVar(&trayEndpoint, "tray-endpoint", "", "")
	cmd.Flags().BoolVar(&enableSocket, "enable-socket", true, "")
	cmd.Flags().IntVar(&toolResponseLimit, "tool-response-limit", 0, "")
	cmd.Flags().StringVar(&toolResponseMode, "tool-response-mode", "", "")
	cmd.Flags().StringVar(&directToolResponseMode, "direct-tool-response-mode", "", "")
	return cmd
}

// writeServeFlagTestConfig writes a config file whose values differ from every
// flag the test passes, so a leaked override is visible in the file.
func writeServeFlagTestConfig(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mcp_config.json")
	raw := `{
  "listen": "127.0.0.1:8080",
  "data_dir": ` + jsonString(tmp) + `,
  "enable_socket": true,
  "tool_response_limit": 20000,
  "tool_response_mode": "full",
  "direct_tool_response_mode": "full",
  "logging": {"level": "info", "enable_file": true, "enable_console": true, "filename": "main.log"},
  "mcpServers": []
}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
	return path
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func readConfigFileJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// saveServeGlobals snapshots the package globals the serve flags are bound to
// and restores them when the test ends. Tests here mutate globals, so they
// must not use t.Parallel.
func saveServeGlobals(t *testing.T) {
	t.Helper()
	oldConfigFile, oldDataDir := configFile, dataDir
	oldListen, oldTray, oldSocket := listen, trayEndpoint, enableSocket
	oldLimit, oldMode, oldDirect := toolResponseLimit, toolResponseMode, directToolResponseMode
	t.Cleanup(func() {
		configFile, dataDir = oldConfigFile, oldDataDir
		listen, trayEndpoint, enableSocket = oldListen, oldTray, oldSocket
		toolResponseLimit, toolResponseMode, directToolResponseMode = oldLimit, oldMode, oldDirect
	})
}

// A `serve` CLI flag applies to that one process only. The saves runServer
// performs (auto-generated API key, first-run telemetry notice, startup
// outcome) must write the file-loaded values back, not the flag overrides —
// otherwise `serve --listen :0` writes `"listen": ":0"` into the file and the
// next unflagged start (or the tray-launched core) boots in stdio mode.
func TestServeFlagOverridesAreNotPersisted(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		key   string
		want  any
		inMem func(t *testing.T, cfg *config.Config)
	}{
		{
			name: "listen :0",
			args: []string{"--listen", ":0"},
			key:  "listen", want: "127.0.0.1:8080",
			inMem: func(t *testing.T, cfg *config.Config) { assert.Equal(t, ":0", cfg.Listen) },
		},
		{
			name: "listen explicit address",
			args: []string{"--listen", "127.0.0.1:9999"},
			key:  "listen", want: "127.0.0.1:8080",
			inMem: func(t *testing.T, cfg *config.Config) { assert.Equal(t, "127.0.0.1:9999", cfg.Listen) },
		},
		{
			name: "tray-endpoint",
			args: []string{"--tray-endpoint", "unix:///tmp/x.sock"},
			key:  "tray_endpoint", want: nil,
			inMem: func(t *testing.T, cfg *config.Config) { assert.Equal(t, "unix:///tmp/x.sock", cfg.TrayEndpoint) },
		},
		{
			name: "enable-socket=false",
			args: []string{"--enable-socket=false"},
			key:  "enable_socket", want: true,
			inMem: func(t *testing.T, cfg *config.Config) { assert.False(t, cfg.EnableSocket) },
		},
		{
			name: "tool-response-limit",
			args: []string{"--tool-response-limit", "500"},
			key:  "tool_response_limit", want: float64(20000),
			inMem: func(t *testing.T, cfg *config.Config) { assert.Equal(t, 500, cfg.ToolResponseLimit) },
		},
		{
			name: "tool-response-mode",
			args: []string{"--tool-response-mode", "compact"},
			key:  "tool_response_mode", want: "full",
			inMem: func(t *testing.T, cfg *config.Config) { assert.Equal(t, "compact", cfg.ToolResponseMode) },
		},
		{
			name: "direct-tool-response-mode",
			args: []string{"--direct-tool-response-mode", "deferred"},
			key:  "direct_tool_response_mode", want: "full",
			inMem: func(t *testing.T, cfg *config.Config) { assert.Equal(t, "deferred", cfg.DirectToolResponseMode) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saveServeGlobals(t)
			path := writeServeFlagTestConfig(t)
			configFile, dataDir = path, filepath.Dir(path)

			cmd := newServeFlagTestCmd()
			require.NoError(t, cmd.ParseFlags(tc.args))

			cfg, saver, err := loadConfig(cmd)
			require.NoError(t, err)
			tc.inMem(t, cfg)

			// The API-key save site: the generated key must land in the file
			// while the flag override must not.
			cfg.APIKey = "mcp_test_generated_key"
			saver.setGeneratedAPIKey(cfg.APIKey)
			require.NoError(t, saver.save(cfg, path))

			file := readConfigFileJSON(t, path)
			assert.Equal(t, "mcp_test_generated_key", file["api_key"], "runtime-generated api_key must persist")
			assert.Equal(t, tc.want, file[tc.key], "flag override leaked into %s", tc.key)
		})
	}
}

// The startup-outcome and first-run-notice saves reuse the same path: they
// persist only the telemetry fields they own, on top of the file-loaded values.
func TestServeSaverPersistsTelemetryWithoutFlagOverrides(t *testing.T) {
	saveServeGlobals(t)
	path := writeServeFlagTestConfig(t)
	configFile, dataDir = path, filepath.Dir(path)

	cmd := newServeFlagTestCmd()
	require.NoError(t, cmd.ParseFlags([]string{"--listen", ":0"}))
	cfg, saver, err := loadConfig(cmd)
	require.NoError(t, err)
	require.Equal(t, ":0", cfg.Listen)

	// Simulate the in-place mutations runServer makes before its saves.
	cfg.Logging.Level = "debug"
	cfg.ReadOnlyMode = true

	recordStartupOutcome(cfg, path, "success", saver.save)
	cfg.Telemetry.NoticeShown = true
	require.NoError(t, saver.save(cfg, path))

	file := readConfigFileJSON(t, path)
	assert.Equal(t, "127.0.0.1:8080", file["listen"])
	assert.Equal(t, false, file["read_only_mode"], "runServer flag override leaked")
	logging, _ := file["logging"].(map[string]any)
	assert.Equal(t, "info", logging["level"], "log-level override leaked")
	telemetry, _ := file["telemetry"].(map[string]any)
	assert.Equal(t, "success", telemetry["last_startup_outcome"])
	assert.Equal(t, true, telemetry["notice_shown"])
}

// An API key that came from MCPPROXY_API_KEY is an override like any flag: the
// saves must not copy that secret into the file. Only a key `serve` generated
// itself is persisted.
func TestServeSaverDoesNotPersistEnvAPIKey(t *testing.T) {
	saveServeGlobals(t)
	path := writeServeFlagTestConfig(t)
	configFile, dataDir = path, filepath.Dir(path)
	t.Setenv("MCPPROXY_API_KEY", "mcp_env_secret")

	cfg, saver, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	apiKey, wasGenerated, _ := cfg.EnsureAPIKey()
	require.Equal(t, "mcp_env_secret", apiKey)
	require.False(t, wasGenerated)

	recordStartupOutcome(cfg, path, "success", saver.save)

	file := readConfigFileJSON(t, path)
	assert.Nil(t, file["api_key"], "env API key leaked into the config file")
	telemetry, _ := file["telemetry"].(map[string]any)
	assert.Equal(t, "success", telemetry["last_startup_outcome"])
}

// The fatal-serve-error save can fire hours after startup, by which time the
// runtime has persisted its own changes (servers added via the API, quarantine
// decisions). That save must layer the telemetry fields onto the CURRENT file,
// not resurrect the startup-time snapshot.
func TestServeSaverKeepsChangesTheRuntimePersistedLater(t *testing.T) {
	saveServeGlobals(t)
	path := writeServeFlagTestConfig(t)
	configFile, dataDir = path, filepath.Dir(path)

	cmd := newServeFlagTestCmd()
	require.NoError(t, cmd.ParseFlags([]string{"--listen", ":0"}))
	cfg, saver, err := loadConfig(cmd)
	require.NoError(t, err)

	// Simulate a runtime save that landed after startup: a server was added
	// and a top-level setting changed.
	onDisk, err := config.LoadFromFile(path)
	require.NoError(t, err)
	onDisk.Servers = append(onDisk.Servers, &config.ServerConfig{Name: "added-later", URL: "http://127.0.0.1:1/mcp", Protocol: "http", Enabled: true})
	onDisk.ToolsLimit = 7
	require.NoError(t, config.SaveConfig(onDisk, path))

	recordStartupOutcome(cfg, path, "other_error", saver.save)

	file := readConfigFileJSON(t, path)
	servers, _ := file["mcpServers"].([]any)
	require.Len(t, servers, 1, "server added after startup was clobbered")
	assert.Equal(t, float64(7), file["tools_limit"], "setting changed after startup was clobbered")
	assert.Equal(t, "127.0.0.1:8080", file["listen"])
	telemetry, _ := file["telemetry"].(map[string]any)
	assert.Equal(t, "other_error", telemetry["last_startup_outcome"])
}

// If the file vanished after startup, the fallback must recreate it with the
// key the file had — not drop it, and not substitute an env key.
func TestServeSaverFallbackKeepsFileAPIKey(t *testing.T) {
	saveServeGlobals(t)
	path := writeServeFlagTestConfig(t)
	configFile, dataDir = path, filepath.Dir(path)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(`{"api_key":"mcp_file_key",`+string(raw[1:])), 0o600))
	t.Setenv("MCPPROXY_API_KEY", "mcp_env_secret")

	cfg, saver, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))

	recordStartupOutcome(cfg, path, "success", saver.save)

	file := readConfigFileJSON(t, path)
	assert.Equal(t, "mcp_file_key", file["api_key"])
	assert.Equal(t, "127.0.0.1:8080", file["listen"])
}

// A key serve generated at startup fills an EMPTY api_key only. If the key was
// rotated through the API and persisted later, a late save keeps the new one.
func TestServeSaverGeneratedKeyDoesNotOverrideRotatedKey(t *testing.T) {
	saveServeGlobals(t)
	path := writeServeFlagTestConfig(t)
	configFile, dataDir = path, filepath.Dir(path)

	cfg, saver, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	saver.setGeneratedAPIKey("mcp_generated_at_startup")
	require.NoError(t, saver.save(cfg, path))
	require.Equal(t, "mcp_generated_at_startup", readConfigFileJSON(t, path)["api_key"])

	// Simulate a runtime-persisted key rotation.
	onDisk := readConfigFileJSON(t, path)
	onDisk["api_key"] = "mcp_rotated"
	rotated, err := json.Marshal(onDisk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, rotated, 0o600))

	recordStartupOutcome(cfg, path, "other_error", saver.save)
	assert.Equal(t, "mcp_rotated", readConfigFileJSON(t, path)["api_key"])
}

// The merge base is the file as written, not a full load: MCPPROXY_* env
// overrides (applied by config.LoadFromFile) must not be written back either.
func TestServeSaverBaseIgnoresEnvOverrides(t *testing.T) {
	saveServeGlobals(t)
	path := writeServeFlagTestConfig(t)
	configFile, dataDir = path, filepath.Dir(path)
	t.Setenv("MCPPROXY_LISTEN", "127.0.0.1:1")

	cfg, saver, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:1", cfg.Listen, "env override applies to the process")

	recordStartupOutcome(cfg, path, "success", saver.save)
	assert.Equal(t, "127.0.0.1:8080", readConfigFileJSON(t, path)["listen"], "env override leaked into the file")
}

// The merge base must apply the loader's read-time normalizations (legacy
// "teams" → "server_edition", MCP-1086) or a serve-time save erases them.
func TestServeSaverKeepsLegacyTeamsBlock(t *testing.T) {
	saveServeGlobals(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mcp_config.json")
	raw := `{"listen":"127.0.0.1:8080","data_dir":` + jsonString(tmp) + `,"teams":{},"mcpServers":[]}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
	configFile, dataDir = path, tmp

	cfg, saver, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	require.NotNil(t, cfg.ServerEdition, "loader normalizes teams → server_edition")

	recordStartupOutcome(cfg, path, "success", saver.save)

	file := readConfigFileJSON(t, path)
	assert.NotNil(t, file["server_edition"], "legacy teams block was erased by the save")
}
