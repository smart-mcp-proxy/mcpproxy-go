package server

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// MCP-1057: `registry remove` derivation core — the pure removal logic that
// every surface (CLI, REST) shares.

func customCfg() *config.Config {
	cfg := config.DefaultConfig()
	cfg.Registries = []config.RegistryEntry{
		{ID: "acme", Name: "Acme", URL: "https://acme.example/", Provenance: config.RegistryProvenanceCustom},
		{ID: "globex", Name: "Globex", URL: "https://globex.example/", Provenance: config.RegistryProvenanceCustom},
	}
	return cfg
}

func TestRemoveRegistrySourceFromConfig_RemovesCustomEntry(t *testing.T) {
	cfg := customCfg()
	removed, remaining, err := removeRegistrySourceFromConfig(cfg, "acme")
	require.NoError(t, err)

	assert.Equal(t, "acme", removed.ID)
	require.Len(t, remaining, 1, "exactly one registry removed")
	assert.Equal(t, "globex", remaining[0].ID)
	// The input slice must be untouched (copy-on-write contract).
	assert.Len(t, cfg.Registries, 2, "source config must not be mutated in place")
}

func TestRemoveRegistrySourceFromConfig_IsCaseInsensitive(t *testing.T) {
	cfg := customCfg()
	removed, remaining, err := removeRegistrySourceFromConfig(cfg, "ACME")
	require.NoError(t, err)
	assert.Equal(t, "acme", removed.ID)
	assert.Len(t, remaining, 1)
}

func TestRemoveRegistrySourceFromConfig_RejectsBuiltin(t *testing.T) {
	cfg := customCfg()
	// "official" is a shipped default — refused via the same shadow guard add-source uses.
	_, _, err := removeRegistrySourceFromConfig(cfg, "official")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRegistryShadowsBuiltin))
}

func TestRemoveRegistrySourceFromConfig_NotFound(t *testing.T) {
	cfg := customCfg()
	_, _, err := removeRegistrySourceFromConfig(cfg, "no-such-registry")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRegistryNotFound))
}

func TestRemoveRegistrySourceFromConfig_EmptyID(t *testing.T) {
	cfg := customCfg()
	_, _, err := removeRegistrySourceFromConfig(cfg, "   ")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRegistryNotFound))
}

func TestRemoveRegistrySourceFromConfig_RejectsLocked(t *testing.T) {
	cfg := customCfg()
	cfg.RegistriesLocked = true
	_, _, err := removeRegistrySourceFromConfig(cfg, "acme")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRegistriesLocked))
}

func TestRemoveRegistrySourceErrorCode(t *testing.T) {
	assert.Equal(t, "", RemoveRegistrySourceErrorCode(nil))
	assert.Equal(t, "registry_not_found", RemoveRegistrySourceErrorCode(ErrRegistryNotFound))
	assert.Equal(t, "registries_locked", RemoveRegistrySourceErrorCode(ErrRegistriesLocked))
	assert.Equal(t, "registry_shadows_builtin", RemoveRegistrySourceErrorCode(ErrRegistryShadowsBuiltin))
}
