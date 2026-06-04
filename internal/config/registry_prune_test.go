package config

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MCP-1049: the shipped default registry set is trimmed to exactly three
// official/trusted entries, and former-default registries that were removed from
// the shipped set are pruned from a user's persisted config on load (the merge in
// registry_data.go keys by id and never prunes, so without this they live forever
// in ~/.mcpproxy/config.json).

func TestDefaultRegistries_ExactlyThree(t *testing.T) {
	defaults := DefaultRegistries()
	ids := make([]string, 0, len(defaults))
	for _, r := range defaults {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	assert.Equal(t, []string{"docker-mcp-catalog", "official", "reference"}, ids,
		"the shipped default registry set must be exactly official/reference/docker-mcp-catalog")
}

func TestDefaultRegistries_DoesNotShipDeprecated(t *testing.T) {
	for _, r := range DefaultRegistries() {
		assert.Falsef(t, IsDeprecatedDefaultRegistry(r.ID),
			"shipped default %q must not also be in the deprecated former-default set", r.ID)
	}
}

func TestIsDeprecatedDefaultRegistry(t *testing.T) {
	for _, id := range []string{"pulse", "smithery", "fleur", "azure-mcp-demo", "remote-mcp-servers"} {
		assert.Truef(t, IsDeprecatedDefaultRegistry(id), "%q must be a known deprecated former-default", id)
	}
	for _, id := range []string{"official", "reference", "docker-mcp-catalog", "my-custom-registry", ""} {
		assert.Falsef(t, IsDeprecatedDefaultRegistry(id), "%q must NOT be flagged deprecated", id)
	}
}

func TestPruneDeprecatedRegistries_RemovesFormerDefaultsPreservesCustom(t *testing.T) {
	cfg := &Config{
		Registries: []RegistryEntry{
			{ID: "official", Name: "Official MCP Registry"},
			{ID: "reference", Name: "Reference Servers"},
			{ID: "docker-mcp-catalog", Name: "Docker MCP Catalog"},
			{ID: "pulse", Name: "Pulse MCP"},
			{ID: "smithery", Name: "Smithery"},
			{ID: "fleur", Name: "Fleur"},
			{ID: "azure-mcp-demo", Name: "Azure MCP Registry Demo"},
			{ID: "remote-mcp-servers", Name: "Remote MCP Servers"},
			{ID: "my-custom-registry", Name: "My Custom Registry"},
		},
	}

	removed := PruneDeprecatedRegistries(cfg)
	assert.Equal(t, 5, removed, "all five deprecated former-defaults must be removed")

	ids := make([]string, 0, len(cfg.Registries))
	for _, r := range cfg.Registries {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	assert.Equal(t, []string{"docker-mcp-catalog", "my-custom-registry", "official", "reference"}, ids,
		"prune must keep the 3 current defaults plus the genuinely user-added custom registry")
}

func TestPruneDeprecatedRegistries_Idempotent(t *testing.T) {
	cfg := &Config{
		Registries: []RegistryEntry{
			{ID: "official"},
			{ID: "pulse"},
			{ID: "my-custom-registry"},
		},
	}
	first := PruneDeprecatedRegistries(cfg)
	require.Equal(t, 1, first)
	second := PruneDeprecatedRegistries(cfg)
	assert.Equal(t, 0, second, "a second prune must be a no-op (idempotent)")
	assert.Len(t, cfg.Registries, 2)
}

func TestPruneDeprecatedRegistries_NilAndEmptySafe(t *testing.T) {
	assert.Equal(t, 0, PruneDeprecatedRegistries(nil))
	assert.Equal(t, 0, PruneDeprecatedRegistries(&Config{}))
}

// initializeRegistries runs on every config load; it must prune the persisted
// deprecated entries so an existing install converges to the trimmed set.
func TestInitializeRegistries_PrunesDeprecatedOnLoad(t *testing.T) {
	cfg := &Config{
		Registries: []RegistryEntry{
			{ID: "official"},
			{ID: "pulse"},
			{ID: "smithery"},
			{ID: "fleur"},
			{ID: "azure-mcp-demo"},
			{ID: "remote-mcp-servers"},
			{ID: "my-custom-registry"},
		},
	}
	initializeRegistries(cfg)

	ids := make([]string, 0, len(cfg.Registries))
	for _, r := range cfg.Registries {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	assert.Equal(t, []string{"my-custom-registry", "official"}, ids,
		"load must prune deprecated former-defaults from the persisted config")
}
