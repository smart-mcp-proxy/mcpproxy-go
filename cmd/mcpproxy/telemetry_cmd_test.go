package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// writeMinimalConfig writes a small valid config JSON to path.
func writeMinimalConfig(t *testing.T, path string, dataDir string) {
	t.Helper()
	cfg := map[string]interface{}{
		"listen":   "127.0.0.1:0",
		"data_dir": dataDir,
		"telemetry": map[string]interface{}{
			"enabled": true,
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func readTelemetryEnabled(t *testing.T, path string) *bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out struct {
		Telemetry *struct {
			Enabled *bool `json:"enabled"`
		} `json:"telemetry"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	if out.Telemetry == nil {
		return nil
	}
	return out.Telemetry.Enabled
}

// sandboxHome redirects HOME (and XDG-style vars) to a tempdir so that
// fallback paths to "~/.mcpproxy/mcp_config.json" land in a sandbox instead
// of clobbering the developer's real config.
func sandboxHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	// On macOS config.Load() might consult USERPROFILE on some paths; harmless on unix.
	t.Setenv("USERPROFILE", home)
	return home
}

// TestRunTelemetryDisable_HonorsConfigFlag is the regression test for the bug
// where `mcpproxy telemetry disable --config /tmp/custom.json` wrote to
// ~/.mcpproxy/mcp_config.json instead of the custom path.
func TestRunTelemetryDisable_HonorsConfigFlag(t *testing.T) {
	home := sandboxHome(t)

	// Target config file at an explicit path unrelated to the fake home.
	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "custom_mcp_config.json")
	dataDir := filepath.Join(tmpDir, "data")
	writeMinimalConfig(t, customPath, dataDir)

	// Simulate `--config customPath` by setting the package-level flag var.
	prev := configFile
	configFile = customPath
	t.Cleanup(func() { configFile = prev })

	if err := runTelemetryDisable(nil, nil); err != nil {
		t.Fatalf("runTelemetryDisable: %v", err)
	}

	// The custom config file must now have telemetry.enabled == false.
	enabled := readTelemetryEnabled(t, customPath)
	if enabled == nil {
		t.Fatal("custom config: telemetry.enabled missing after disable")
	}
	if *enabled {
		t.Errorf("custom config: telemetry.enabled = true, want false")
	}

	// The sandboxed default path must NOT have been created or modified.
	defaultPath := filepath.Join(home, ".mcpproxy", "mcp_config.json")
	if _, err := os.Stat(defaultPath); !os.IsNotExist(err) {
		t.Errorf("default config path should not exist in sandbox, got err=%v", err)
	}
}

// TestRunTelemetryEnable_HonorsConfigFlag mirrors the disable test for enable.
func TestRunTelemetryEnable_HonorsConfigFlag(t *testing.T) {
	sandboxHome(t)

	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "custom_mcp_config.json")
	dataDir := filepath.Join(tmpDir, "data")

	// Start with telemetry disabled so enable is observable.
	cfg := map[string]interface{}{
		"listen":   "127.0.0.1:0",
		"data_dir": dataDir,
		"telemetry": map[string]interface{}{
			"enabled": false,
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(customPath, data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	prev := configFile
	configFile = customPath
	t.Cleanup(func() { configFile = prev })

	if err := runTelemetryEnable(nil, nil); err != nil {
		t.Fatalf("runTelemetryEnable: %v", err)
	}

	enabled := readTelemetryEnabled(t, customPath)
	if enabled == nil {
		t.Fatal("custom config: telemetry.enabled missing after enable")
	}
	if !*enabled {
		t.Errorf("custom config: telemetry.enabled = false, want true")
	}
}

// TestTelemetryConfigSavePath_PrefersConfigFileFlag is a small unit test for
// the helper itself: when configFile is set, it wins over the DataDir-derived
// default; when unset, it falls back to config.GetConfigPath(DataDir).
func TestTelemetryConfigSavePath_PrefersConfigFileFlag(t *testing.T) {
	cfg := &config.Config{DataDir: "/tmp/fake-data-dir"}

	prev := configFile
	t.Cleanup(func() { configFile = prev })

	configFile = ""
	if got := telemetryConfigSavePath(cfg); got != config.GetConfigPath(cfg.DataDir) {
		t.Errorf("with empty configFile: got %q, want default %q", got, config.GetConfigPath(cfg.DataDir))
	}

	configFile = "/elsewhere/custom.json"
	if got := telemetryConfigSavePath(cfg); got != "/elsewhere/custom.json" {
		t.Errorf("with configFile set: got %q, want %q", got, "/elsewhere/custom.json")
	}
}

// writeBeaconTestConfig writes a config with telemetry enabled, a real
// anonymous_id and the opt-out beacon endpoint pointed at a test server, so
// runTelemetryDisable's beacon send (if any) is observable and hermetic.
func writeBeaconTestConfig(t *testing.T, path, dataDir, endpoint string) {
	t.Helper()
	cfg := map[string]interface{}{
		"listen":   "127.0.0.1:0",
		"data_dir": dataDir,
		"telemetry": map[string]interface{}{
			"enabled":      true,
			"anonymous_id": "0f5b62e0-1111-4222-8333-444455556666",
			"endpoint":     endpoint,
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// beaconTestSetup neutralizes the telemetry env kill-switches (CI is set on
// runners and would suppress the beacon entirely), sandboxes HOME, points the
// beacon endpoint at a counting test server, and wires --config. Returns the
// request counter.
func beaconTestSetup(t *testing.T) *int32 {
	t.Helper()
	sandboxHome(t)
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "custom_mcp_config.json")
	writeBeaconTestConfig(t, customPath, tmpDir, srv.URL)

	prev := configFile
	configFile = customPath
	t.Cleanup(func() { configFile = prev })

	return &hits
}

// TestRunTelemetryDisable_SendsBeacon: the CLI always sends the opt-out
// beacon (exactly once) after persisting the disable — it does NOT gate the
// send on daemon detection. Accepted trade-off (PR #857): a running daemon
// may also emit the beacon on config hot-reload, and the backend dedupes by
// anon_id; a detection false positive dropping the only send would be worse.
func TestRunTelemetryDisable_SendsBeacon(t *testing.T) {
	hits := beaconTestSetup(t)

	if err := runTelemetryDisable(nil, nil); err != nil {
		t.Fatalf("runTelemetryDisable: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("beacon sends = %d, want 1", got)
	}
	// The disable itself must be persisted.
	enabled := readTelemetryEnabled(t, configFile)
	if enabled == nil || *enabled {
		t.Errorf("telemetry.enabled must be persisted false")
	}
}
