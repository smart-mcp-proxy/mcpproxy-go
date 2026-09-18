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
