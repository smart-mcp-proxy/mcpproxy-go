package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerConfig_IsAutoApproveToolChanges mirrors IsQuarantineSkipped /
// IsQuarantineEnabled: the accessor reflects the tri-state *bool field, treating
// unset (nil) as false (MCP-2930).
func TestServerConfig_IsAutoApproveToolChanges(t *testing.T) {
	tests := []struct {
		name     string
		config   ServerConfig
		expected bool
	}{
		{name: "unset (nil) is false", config: ServerConfig{}, expected: false},
		{name: "explicit true", config: ServerConfig{AutoApproveToolChanges: boolPtr(true)}, expected: true},
		{name: "explicit false", config: ServerConfig{AutoApproveToolChanges: boolPtr(false)}, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.IsAutoApproveToolChanges())
		})
	}
}

// TestServerConfig_AutoApproveToolChanges_JSONSerialization verifies the new
// tri-state field distinguishes unset / explicit-true / explicit-false through
// JSON and omits when unset.
func TestServerConfig_AutoApproveToolChanges_JSONSerialization(t *testing.T) {
	// Explicit true.
	var scTrue ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"t","auto_approve_tool_changes":true,"enabled":true}`), &scTrue))
	require.NotNil(t, scTrue.AutoApproveToolChanges)
	assert.True(t, *scTrue.AutoApproveToolChanges)
	assert.True(t, scTrue.IsAutoApproveToolChanges())

	// Explicit false is distinguishable from unset (non-nil pointer to false).
	var scFalse ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"t","auto_approve_tool_changes":false,"enabled":true}`), &scFalse))
	require.NotNil(t, scFalse.AutoApproveToolChanges)
	assert.False(t, *scFalse.AutoApproveToolChanges)
	assert.False(t, scFalse.IsAutoApproveToolChanges())

	// Omitted => nil (unset).
	var scUnset ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"t","enabled":true}`), &scUnset))
	assert.Nil(t, scUnset.AutoApproveToolChanges)
	assert.False(t, scUnset.IsAutoApproveToolChanges())

	// nil omits from marshalled output (omitempty); explicit false is emitted.
	outUnset, err := json.Marshal(ServerConfig{Name: "t"})
	require.NoError(t, err)
	assert.NotContains(t, string(outUnset), "auto_approve_tool_changes")
	outFalse, err := json.Marshal(ServerConfig{Name: "t", AutoApproveToolChanges: boolPtr(false)})
	require.NoError(t, err)
	assert.Contains(t, string(outFalse), `"auto_approve_tool_changes":false`)
}

// TestNormalizeServerQuarantineFlags covers the legacy skip_quarantine ->
// auto_approve_tool_changes migration, including that an explicit new-field value
// (even false) always wins over the legacy flag (MCP-2930).
func TestNormalizeServerQuarantineFlags(t *testing.T) {
	t.Run("legacy skip_quarantine true maps when new field unset", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{{Name: "legacy", SkipQuarantine: true}}}
		normalizeServerQuarantineFlags(cfg)
		assert.True(t, cfg.Servers[0].IsAutoApproveToolChanges(), "legacy true should map to new field")
	})

	t.Run("explicit new field true wins over legacy false", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{
			{Name: "new", SkipQuarantine: false, AutoApproveToolChanges: boolPtr(true)},
		}}
		normalizeServerQuarantineFlags(cfg)
		assert.True(t, cfg.Servers[0].IsAutoApproveToolChanges())
	})

	t.Run("explicit new field false wins over legacy true (regression: explicit-false honored)", func(t *testing.T) {
		// The crux: a user who sets skip_quarantine:true AND auto_approve_tool_changes:false
		// must keep auto-approval OFF. A plain bool could not express this.
		cfg := &Config{Servers: []*ServerConfig{
			{Name: "override-off", SkipQuarantine: true, AutoApproveToolChanges: boolPtr(false)},
		}}
		normalizeServerQuarantineFlags(cfg)
		require.NotNil(t, cfg.Servers[0].AutoApproveToolChanges)
		assert.False(t, *cfg.Servers[0].AutoApproveToolChanges, "explicit false must not be clobbered by legacy true")
		assert.False(t, cfg.Servers[0].IsAutoApproveToolChanges())
	})

	t.Run("both legacy and new true is idempotent", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{
			{Name: "both", SkipQuarantine: true, AutoApproveToolChanges: boolPtr(true)},
		}}
		normalizeServerQuarantineFlags(cfg)
		assert.True(t, cfg.Servers[0].IsAutoApproveToolChanges())
	})

	t.Run("neither set stays unset/false", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{{Name: "none"}}}
		normalizeServerQuarantineFlags(cfg)
		assert.Nil(t, cfg.Servers[0].AutoApproveToolChanges)
		assert.False(t, cfg.Servers[0].IsAutoApproveToolChanges())
	})

	t.Run("nil config and nil server are safe", func(t *testing.T) {
		assert.NotPanics(t, func() { normalizeServerQuarantineFlags(nil) })
		cfg := &Config{Servers: []*ServerConfig{nil}}
		assert.NotPanics(t, func() { normalizeServerQuarantineFlags(cfg) })
	})
}

// TestAutoApproveToolChanges_RoundTrip_SaveLoad verifies the field survives a
// SaveConfig -> LoadFromFile round-trip (including explicit false), and that a
// legacy config file is normalized on load.
func TestAutoApproveToolChanges_RoundTrip_SaveLoad(t *testing.T) {
	dir := t.TempDir()

	t.Run("new field true round-trips", func(t *testing.T) {
		path := filepath.Join(dir, "new.json")
		cfg := DefaultConfig()
		cfg.DataDir = dir
		cfg.Servers = []*ServerConfig{
			{Name: "srv", Protocol: "stdio", Command: "npx", Enabled: true, AutoApproveToolChanges: boolPtr(true)},
		}
		require.NoError(t, SaveConfig(cfg, path))

		loaded, err := LoadFromFile(path)
		require.NoError(t, err)
		require.Len(t, loaded.Servers, 1)
		assert.True(t, loaded.Servers[0].IsAutoApproveToolChanges())
	})

	t.Run("explicit false round-trips and is not migrated", func(t *testing.T) {
		path := filepath.Join(dir, "false.json")
		cfg := DefaultConfig()
		cfg.DataDir = dir
		cfg.Servers = []*ServerConfig{
			{Name: "srv", Protocol: "stdio", Command: "npx", Enabled: true, SkipQuarantine: true, AutoApproveToolChanges: boolPtr(false)},
		}
		require.NoError(t, SaveConfig(cfg, path))

		loaded, err := LoadFromFile(path)
		require.NoError(t, err)
		require.Len(t, loaded.Servers, 1)
		require.NotNil(t, loaded.Servers[0].AutoApproveToolChanges)
		assert.False(t, loaded.Servers[0].IsAutoApproveToolChanges(), "explicit false survives load + normalize")
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
