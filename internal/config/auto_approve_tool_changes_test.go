package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerConfig_IsAutoApproveToolChanges mirrors IsQuarantineSkipped: the
// accessor simply reflects the new field (MCP-2930).
func TestServerConfig_IsAutoApproveToolChanges(t *testing.T) {
	tests := []struct {
		name     string
		config   ServerConfig
		expected bool
	}{
		{name: "default false", config: ServerConfig{}, expected: false},
		{name: "explicit true", config: ServerConfig{AutoApproveToolChanges: true}, expected: true},
		{name: "explicit false", config: ServerConfig{AutoApproveToolChanges: false}, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.IsAutoApproveToolChanges())
		})
	}
}

// TestServerConfig_AutoApproveToolChanges_JSONSerialization verifies the new
// field round-trips through JSON and omits when false.
func TestServerConfig_AutoApproveToolChanges_JSONSerialization(t *testing.T) {
	serverJSON := `{"name": "test", "auto_approve_tool_changes": true, "enabled": true}`
	var sc ServerConfig
	require.NoError(t, json.Unmarshal([]byte(serverJSON), &sc))
	assert.True(t, sc.AutoApproveToolChanges)
	assert.True(t, sc.IsAutoApproveToolChanges())

	// Omitted defaults to false.
	var sc2 ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name": "test", "enabled": true}`), &sc2))
	assert.False(t, sc2.AutoApproveToolChanges)
	assert.False(t, sc2.IsAutoApproveToolChanges())

	// false omits from marshalled output (omitempty).
	out, err := json.Marshal(ServerConfig{Name: "test"})
	require.NoError(t, err)
	assert.NotContains(t, string(out), "auto_approve_tool_changes")
}

// TestNormalizeServerQuarantineFlags covers the legacy skip_quarantine ->
// auto_approve_tool_changes migration (MCP-2930).
func TestNormalizeServerQuarantineFlags(t *testing.T) {
	t.Run("legacy skip_quarantine true maps when new field unset", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{
			{Name: "legacy", SkipQuarantine: true},
		}}
		normalizeServerQuarantineFlags(cfg)
		assert.True(t, cfg.Servers[0].AutoApproveToolChanges, "legacy true should map to new field")
	})

	t.Run("explicit new field wins over legacy", func(t *testing.T) {
		// New field already true, legacy false: stays true.
		cfg := &Config{Servers: []*ServerConfig{
			{Name: "new", SkipQuarantine: false, AutoApproveToolChanges: true},
		}}
		normalizeServerQuarantineFlags(cfg)
		assert.True(t, cfg.Servers[0].AutoApproveToolChanges)
	})

	t.Run("new field already set is not clobbered by legacy", func(t *testing.T) {
		// Both set true: idempotent, stays true.
		cfg := &Config{Servers: []*ServerConfig{
			{Name: "both", SkipQuarantine: true, AutoApproveToolChanges: true},
		}}
		normalizeServerQuarantineFlags(cfg)
		assert.True(t, cfg.Servers[0].AutoApproveToolChanges)
	})

	t.Run("neither set stays false", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{{Name: "none"}}}
		normalizeServerQuarantineFlags(cfg)
		assert.False(t, cfg.Servers[0].AutoApproveToolChanges)
	})

	t.Run("nil config and nil server are safe", func(t *testing.T) {
		assert.NotPanics(t, func() { normalizeServerQuarantineFlags(nil) })
		cfg := &Config{Servers: []*ServerConfig{nil}}
		assert.NotPanics(t, func() { normalizeServerQuarantineFlags(cfg) })
	})
}

// TestAutoApproveToolChanges_RoundTrip_SaveLoad verifies the field survives a
// SaveConfig -> LoadFromFile round-trip, and that a legacy config file is
// normalized on load.
func TestAutoApproveToolChanges_RoundTrip_SaveLoad(t *testing.T) {
	dir := t.TempDir()

	t.Run("new field round-trips", func(t *testing.T) {
		path := filepath.Join(dir, "new.json")
		cfg := DefaultConfig()
		cfg.DataDir = dir
		cfg.Servers = []*ServerConfig{
			{Name: "srv", Protocol: "stdio", Command: "npx", Enabled: true, AutoApproveToolChanges: true},
		}
		require.NoError(t, SaveConfig(cfg, path))

		loaded, err := LoadFromFile(path)
		require.NoError(t, err)
		require.Len(t, loaded.Servers, 1)
		assert.True(t, loaded.Servers[0].IsAutoApproveToolChanges())
	})

	t.Run("legacy skip_quarantine file normalizes on load", func(t *testing.T) {
		path := filepath.Join(dir, "legacy.json")
		// Build via the config struct + json.Marshal so the temp dir (which contains
		// backslashes on Windows) is escaped correctly in the JSON file.
		legacyCfg := DefaultConfig()
		legacyCfg.DataDir = dir
		legacyCfg.Servers = []*ServerConfig{
			{Name: "srv", Protocol: "stdio", Command: "npx", Enabled: true, SkipQuarantine: true},
		}
		legacyBytes, err := json.Marshal(legacyCfg)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, legacyBytes, 0600))

		loaded, err := LoadFromFile(path)
		require.NoError(t, err)
		require.Len(t, loaded.Servers, 1)
		assert.True(t, loaded.Servers[0].IsAutoApproveToolChanges(), "legacy skip_quarantine should map on load")
		// Legacy field is preserved for back-compat.
		assert.True(t, loaded.Servers[0].SkipQuarantine)
	})
}
