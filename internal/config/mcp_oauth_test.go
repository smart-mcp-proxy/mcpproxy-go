package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 132: the opt-in browser login switch is off by default, never written
// to disk while off, and its env override is process-only.
func TestConfig_MCPOAuthEnabled_DefaultOffAndOmitted(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	t.Setenv(EnvMCPOAuthEnabled, "")

	assert.False(t, DefaultConfig().MCPOAuthEnabled)

	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, SaveConfig(DefaultConfig(), path))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "mcp_oauth_enabled")

	// Env override turns it on for this process only.
	t.Setenv(EnvMCPOAuthEnabled, "true")
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.True(t, cfg.MCPOAuthEnabled, "env overrides the file value")

	require.NoError(t, SaveConfig(cfg, path))
	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "mcp_oauth_enabled", "the env override is not persisted")

	// A file value of true is honoured and persisted.
	t.Setenv(EnvMCPOAuthEnabled, "")
	ResetProcessOverrides()
	cfg, err = LoadFromFile(path)
	require.NoError(t, err)
	assert.False(t, cfg.MCPOAuthEnabled)
	cfg.MCPOAuthEnabled = true
	require.NoError(t, SaveConfig(cfg, path))
	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"mcp_oauth_enabled": true`)
}
