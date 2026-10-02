package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// loaderCase describes one per-command config loader and how to set/clear its
// local --config variable.
type loaderCase struct {
	name    string
	setPath func(string)
	load    func() (*config.Config, error)
}

func managementLoaderCases() []loaderCase {
	return []loaderCase{
		{"doctor", func(p string) { doctorConfigPath = p }, loadDoctorConfig},
		{"auth", func(p string) { authConfigPath = p }, loadAuthConfig},
		{"call", func(p string) { callConfigPath = p }, loadCallConfig},
		{"code", func(p string) { codeConfigPath = p }, loadCodeConfig},
		{"tools", func(p string) { configPath = p }, loadToolsConfig},
		{"token", func(p string) { tokenConfigPath = p }, loadTokenConfig},
		{"registry", func(p string) { registryConfigPath = p }, loadRegistryConfig},
		{"upstream", func(p string) { upstreamConfigPath = p }, loadUpstreamConfig},
		{"status", func(string) {}, loadStatusConfig},
	}
}

func writeListenConfig(t *testing.T, path, listen string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"listen":%q,"mcpServers":[]}`, listen)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// resetLoaderGlobals clears every path variable the loaders read and restores
// them after the test.
func resetLoaderGlobals(t *testing.T) {
	t.Helper()
	oldCfg, oldDir := configFile, dataDir
	t.Cleanup(func() {
		configFile, dataDir = oldCfg, oldDir
		for _, tc := range managementLoaderCases() {
			tc.setPath("")
		}
	})
	configFile, dataDir = "", ""
	for _, tc := range managementLoaderCases() {
		tc.setPath("")
	}
}

// F-06: the global -c applies to every management command even though the
// command registers (and leaves empty) its own local --config.
func TestLoadersHonorGlobalConfigFlag(t *testing.T) {
	for _, tc := range managementLoaderCases() {
		t.Run(tc.name, func(t *testing.T) {
			resetLoaderGlobals(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			cfgPath := filepath.Join(t.TempDir(), "global.json")
			writeListenConfig(t, cfgPath, "127.0.0.1:18999")
			configFile = cfgPath

			cfg, err := tc.load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.Listen != "127.0.0.1:18999" {
				t.Errorf("listen = %q, want the global -c file's 127.0.0.1:18999", cfg.Listen)
			}
		})
	}
}

// F-06: with only -d, management commands read <data-dir>/mcp_config.json and
// never create a default under $HOME.
func TestLoadersPreferDataDirConfig(t *testing.T) {
	for _, tc := range managementLoaderCases() {
		t.Run(tc.name, func(t *testing.T) {
			resetLoaderGlobals(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Chdir(t.TempDir())
			d := filepath.Join(t.TempDir(), "d")
			writeListenConfig(t, filepath.Join(d, "mcp_config.json"), "127.0.0.1:18998")
			dataDir = d

			cfg, err := tc.load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.Listen != "127.0.0.1:18998" {
				t.Errorf("listen = %q, want the data dir's 127.0.0.1:18998", cfg.Listen)
			}
			if cfg.DataDir != d {
				t.Errorf("DataDir = %q, want %q", cfg.DataDir, d)
			}
			if _, statErr := os.Stat(filepath.Join(home, ".mcpproxy", "mcp_config.json")); statErr == nil {
				t.Error("a default config was created under $HOME")
			}
		})
	}
}

// A -d without a config file anywhere must not fabricate $HOME/.mcpproxy.
func TestLoadCLIConfigDataDirWithoutFileCreatesNothingInHome(t *testing.T) {
	resetLoaderGlobals(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	dataDir = filepath.Join(t.TempDir(), "empty")

	cfg, err := loadCLIConfig("")
	if err != nil {
		t.Fatalf("loadCLIConfig: %v", err)
	}
	if cfg.DataDir != dataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, dataDir)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".mcpproxy")); statErr == nil {
		t.Error("$HOME/.mcpproxy was created")
	}
}

func TestUpstreamConfigFilePathMatchesLoadPath(t *testing.T) {
	resetLoaderGlobals(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	d := filepath.Join(t.TempDir(), "d")
	want := filepath.Join(d, "mcp_config.json")
	writeListenConfig(t, want, "127.0.0.1:18997")
	dataDir = d

	cfg, err := loadUpstreamConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := upstreamConfigFilePath(cfg); got != want {
		t.Errorf("upstreamConfigFilePath = %q, want the file that was read: %q", got, want)
	}
}

func TestResolveCLIConfigPathPrecedence(t *testing.T) {
	resetLoaderGlobals(t)
	d := t.TempDir()
	writeListenConfig(t, filepath.Join(d, "mcp_config.json"), "127.0.0.1:1")
	dataDir = d

	if got := resolveCLIConfigPath("local.json"); got != "local.json" {
		t.Errorf("local flag must win, got %q", got)
	}
	configFile = "global.json"
	if got := resolveCLIConfigPath(""); got != "global.json" {
		t.Errorf("global -c must beat -d, got %q", got)
	}
	configFile = ""
	if got := resolveCLIConfigPath(""); got != filepath.Join(d, "mcp_config.json") {
		t.Errorf("-d config must be used when present, got %q", got)
	}
	dataDir = t.TempDir()
	if got := resolveCLIConfigPath(""); got != "" {
		t.Errorf("-d without a config file falls back to discovery, got %q", got)
	}
}
