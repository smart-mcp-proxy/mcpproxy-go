package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// MCP-866: a server derived from a custom/unverified registry is ALWAYS
// quarantined and never skips quarantine, even when the global quarantine
// default is off. Its source registry id + provenance are stamped onto the
// config so the approval/quarantine view can surface the origin.
func TestAddFromRegistry_CustomOriginAlwaysQuarantined(t *testing.T) {
	entry := &registries.ServerEntry{ID: "x", Name: "x", InstallCmd: "npx x"}

	cfg, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{
		RegistryID:       "acme",
		ServerID:         "x",
		SourceRegistryID: "acme",
		SourceProvenance: config.RegistryProvenanceCustom,
	}, false) // global default OFF — custom origin must still quarantine.

	require.NoError(t, err)
	assert.True(t, cfg.Quarantined, "custom/unverified origin must always quarantine")
	assert.False(t, cfg.SkipQuarantine, "custom/unverified origin must never skip quarantine")
	assert.Equal(t, "acme", cfg.SourceRegistryID)
	assert.Equal(t, config.RegistryProvenanceCustom, cfg.SourceRegistryProvenance)
	// The derived config must itself pass validation (no skip_quarantine clash).
	full := config.DefaultConfig()
	full.Servers = []*config.ServerConfig{cfg}
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
