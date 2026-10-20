package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// serveDataDirEnv isolates $HOME and cwd and clears every variable that could
// relocate the data dir, so a test only sees what it sets itself.
func serveDataDirEnv(t *testing.T) (home string) {
	t.Helper()
	saveServeGlobals(t)
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.EnvDataDir, "")
	t.Setenv(config.EnvDataDirLegacy, "")
	t.Setenv("MCPPROXY_API_KEY", "")
	t.Chdir(t.TempDir())
	configFile, dataDir = "", ""
	return home
}

// `serve --data-dir X` reads X/mcp_config.json (the file the management
// commands already read), instead of $HOME/.mcpproxy.
func TestServeReadsConfigFromDataDir(t *testing.T) {
	home := serveDataDirEnv(t)
	d := filepath.Join(t.TempDir(), "state")
	writeListenConfig(t, filepath.Join(d, "mcp_config.json"), "127.0.0.1:18997")
	dataDir = d

	cfg, saver, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:18997", cfg.Listen, "config in the data dir was ignored")
	assert.Equal(t, d, cfg.DataDir)
	assert.Equal(t, filepath.Join(d, "mcp_config.json"), saver.path, "later saves must go to the file that was read")
	_, statErr := os.Stat(filepath.Join(home, ".mcpproxy", "mcp_config.json"))
	assert.True(t, os.IsNotExist(statErr), "a default config was created under $HOME")
}

// The same through MCPPROXY_DATA_DIR, which registerRootPathFlags turns into
// the --data-dir default.
func TestServeReadsConfigFromDataDirEnv(t *testing.T) {
	serveDataDirEnv(t)
	d := filepath.Join(t.TempDir(), "state")
	writeListenConfig(t, filepath.Join(d, "mcp_config.json"), "127.0.0.1:18996")
	t.Setenv(config.EnvDataDir, d)

	root := &cobra.Command{Use: "mcpproxy"}
	registerRootPathFlags(root)
	require.NoError(t, root.ParseFlags(nil))
	require.Equal(t, d, dataDir)
	assert.Equal(t, "", root.PersistentFlags().Lookup("data-dir").DefValue, "--help must not change when the variable is set")

	cfg, _, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:18996", cfg.Listen)
	assert.Equal(t, d, cfg.DataDir)
}

// An explicit --data-dir beats MCPPROXY_DATA_DIR.
func TestServeDataDirFlagBeatsEnv(t *testing.T) {
	serveDataDirEnv(t)
	envDir := filepath.Join(t.TempDir(), "env")
	flagDir := filepath.Join(t.TempDir(), "flag")
	writeListenConfig(t, filepath.Join(envDir, "mcp_config.json"), "127.0.0.1:18001")
	writeListenConfig(t, filepath.Join(flagDir, "mcp_config.json"), "127.0.0.1:18002")
	t.Setenv(config.EnvDataDir, envDir)

	root := &cobra.Command{Use: "mcpproxy"}
	registerRootPathFlags(root)
	require.NoError(t, root.ParseFlags([]string{"--data-dir", flagDir}))

	cfg, _, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:18002", cfg.Listen)
	assert.Equal(t, flagDir, cfg.DataDir)
}

// An explicit --config still wins over a config in the data dir.
func TestServeConfigFlagBeatsDataDirConfig(t *testing.T) {
	serveDataDirEnv(t)
	d := filepath.Join(t.TempDir(), "state")
	writeListenConfig(t, filepath.Join(d, "mcp_config.json"), "127.0.0.1:18001")
	explicit := filepath.Join(t.TempDir(), "explicit.json")
	writeListenConfig(t, explicit, "127.0.0.1:18003")
	dataDir, configFile = d, explicit

	cfg, _, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:18003", cfg.Listen)
}

// First run: nothing anywhere, so the default config (and its generated API
// key) is seeded inside the named data dir, not under $HOME.
func TestServeFirstRunSeedsConfigInDataDir(t *testing.T) {
	home := serveDataDirEnv(t)
	d := filepath.Join(t.TempDir(), "fresh")
	dataDir = d

	cfg, _, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	assert.Equal(t, d, cfg.DataDir)
	_, statErr := os.Stat(filepath.Join(d, "mcp_config.json"))
	assert.NoError(t, statErr, "default config was not seeded in the data dir")
	_, statErr = os.Stat(filepath.Join(home, ".mcpproxy", "mcp_config.json"))
	assert.True(t, os.IsNotExist(statErr), "a default config was created under $HOME")
}

// Compatibility: with no config in the data dir but one in the legacy location,
// serve keeps using the legacy file and only relocates the data dir.
func TestServeKeepsLegacyConfigWhenDataDirHasNone(t *testing.T) {
	home := serveDataDirEnv(t)
	writeListenConfig(t, filepath.Join(home, ".mcpproxy", "mcp_config.json"), "127.0.0.1:18004")
	d := filepath.Join(t.TempDir(), "state")
	require.NoError(t, os.MkdirAll(d, 0o700))
	dataDir = d

	cfg, _, err := loadConfig(newServeFlagTestCmd())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:18004", cfg.Listen)
	assert.Equal(t, d, cfg.DataDir)
	_, statErr := os.Stat(filepath.Join(d, "mcp_config.json"))
	assert.True(t, os.IsNotExist(statErr), "no config should be seeded when a legacy one exists")
}

// MCPPROXY_DATA_DIR is the documented name and beats the legacy MCPPROXY_DATA
// when the loader applies env overrides; the legacy name keeps working alone.
func TestDataDirEnvNames(t *testing.T) {
	cases := []struct {
		name, newVar, legacy, want string
	}{
		{"new only", "/n", "", "/n"},
		{"legacy only", "", "/l", "/l"},
		{"both: new wins", "/n", "/l", "/n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvDataDir, tc.newVar)
			t.Setenv(config.EnvDataDirLegacy, tc.legacy)
			cfg := config.DefaultConfig()
			config.ApplyEnvOverrides(cfg)
			assert.Equal(t, tc.want, cfg.DataDir)
		})
	}
}
