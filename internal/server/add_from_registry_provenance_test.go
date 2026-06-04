package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// MCP-1072: a server derived from a custom registry now follows the GLOBAL
// quarantine default like everything else — provenance no longer forces
// quarantine. Its source registry id + provenance are still stamped onto the
// config so the approval/quarantine view can surface the origin.
func TestAddFromRegistry_CustomOriginFollowsGlobalDefault(t *testing.T) {
	entry := &registries.ServerEntry{ID: "x", Name: "x", InstallCmd: "npx x"}

	build := func(quarantineDefault bool) *config.ServerConfig {
		cfg, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{
			RegistryID:       "acme",
			ServerID:         "x",
			SourceRegistryID: "acme",
			SourceProvenance: config.RegistryProvenanceCustom,
		}, quarantineDefault)
		require.NoError(t, err)
		return cfg
	}

	// Global default ON → quarantined.
	on := build(true)
	assert.True(t, on.Quarantined, "custom origin quarantines when the global default is on")
	assert.Equal(t, "acme", on.SourceRegistryID)
	assert.Equal(t, config.RegistryProvenanceCustom, on.SourceRegistryProvenance)

	// Global default OFF → NOT quarantined (no more special-casing).
	off := build(false)
	assert.False(t, off.Quarantined, "custom origin must NOT be force-quarantined when the global default is off")
	assert.False(t, off.SkipQuarantine, "skip_quarantine is not forced either way")
	assert.Equal(t, config.RegistryProvenanceCustom, off.SourceRegistryProvenance)

	// The derived config still passes validation.
	full := config.DefaultConfig()
	full.Servers = []*config.ServerConfig{off}
	assert.NoError(t, full.Validate())
}

// Official-origin servers stamp provenance but keep following the global
// quarantine default (CN-002 unchanged).
func TestAddFromRegistry_OfficialOriginFollowsGlobalDefault(t *testing.T) {
	entry := &registries.ServerEntry{ID: "y", Name: "y", InstallCmd: "npx y"}

	cfg, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{
		RegistryID:       "official",
		ServerID:         "y",
		SourceRegistryID: "official",
		SourceProvenance: config.RegistryProvenanceOfficial,
	}, false)

	require.NoError(t, err)
	assert.False(t, cfg.Quarantined, "official origin follows the (off) global default")
	assert.Equal(t, "official", cfg.SourceRegistryID)
	assert.Equal(t, config.RegistryProvenanceOfficial, cfg.SourceRegistryProvenance)
}
