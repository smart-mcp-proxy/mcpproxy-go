package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
