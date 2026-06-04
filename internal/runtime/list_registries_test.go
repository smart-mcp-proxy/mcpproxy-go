package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// registryIDsFromList extracts the "id" field from the []interface{} that
// Runtime.ListRegistries returns (each element is a map[string]interface{}).
func registryIDsFromList(t *testing.T, list []interface{}) []string {
	t.Helper()
	ids := make([]string, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]interface{})
		require.True(t, ok, "each registry must be a map")
		id, _ := m["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// FR-006 / MCP-800 finding 2: Runtime.ListRegistries must route through the same
// merged source (built-in defaults + user registries, keyed by ID) that
// search/add use — not return the legacy hard-coded Smithery entry for an empty
// config, nor only the custom entries when set.
func TestListRegistries_MergesDefaultsWithCustom(t *testing.T) {
	logger := zap.NewNop()
	defaults := config.DefaultRegistries()
	require.NotEmpty(t, defaults, "built-in defaults must exist")

	// Empty config → built-in defaults, NOT the hard-coded legacy Smithery entry.
	rtEmpty := &Runtime{logger: logger, cfg: &config.Config{}}
	gotEmpty, err := rtEmpty.ListRegistries()
	require.NoError(t, err)
	idsEmpty := registryIDsFromList(t, gotEmpty)
	assert.Len(t, idsEmpty, len(defaults), "empty config must return exactly the built-in defaults")
	for _, d := range defaults {
		assert.Contains(t, idsEmpty, d.ID, "built-in default %q must be listed", d.ID)
	}
	// Dropped former-default registries must not leak; the list must route
	// through the merged defaults source, not stale hard-coded entries. The
	// shipped set is now exactly official/reference/docker-mcp-catalog (MCP-1049),
	// so pulse, smithery, fleur, azure and remote-mcp-servers are all gone.
	for _, gone := range []string{"fleur", "pulse", "smithery", "azure-mcp-demo", "remote-mcp-servers"} {
		assert.NotContainsf(t, idsEmpty, gone, "deprecated former-default %q must not leak when defaults exist", gone)
	}

	// Custom config → custom entry merges WITH the defaults (does not replace them).
	rtCustom := &Runtime{logger: logger, cfg: &config.Config{
		Registries: []config.RegistryEntry{
			{ID: "custom-reg", Name: "Custom", ServersURL: "http://example.test/x", Protocol: "modelcontextprotocol/registry"},
		},
	}}
	gotCustom, err := rtCustom.ListRegistries()
	require.NoError(t, err)
	idsCustom := registryIDsFromList(t, gotCustom)
	assert.Contains(t, idsCustom, "custom-reg", "custom registry must appear")
	for _, d := range defaults {
		assert.Contains(t, idsCustom, d.ID, "built-in default %q must still appear alongside custom", d.ID)
	}
	assert.Len(t, idsCustom, len(defaults)+1, "custom registry must be additive to defaults")
}

// MCP-1049: the app is config-authoritative — deprecated former-default
// registries (pulse/smithery/fleur/azure/remote) persisted in an existing
// install must NOT resurface in the listing. The merge skips them, so the
// running app converges to the 3 trusted defaults (+ any genuine custom entry)
// regardless of what stale ids are still on disk.
func TestListRegistries_DeprecatedPersistedEntriesDoNotResurface(t *testing.T) {
	logger := zap.NewNop()
	defaults := config.DefaultRegistries()

	rt := &Runtime{logger: logger, cfg: &config.Config{
		Registries: []config.RegistryEntry{
			{ID: "pulse", Name: "Pulse MCP"},
			{ID: "smithery", Name: "Smithery"},
			{ID: "fleur", Name: "Fleur"},
			{ID: "azure-mcp-demo", Name: "Azure MCP Registry Demo"},
			{ID: "remote-mcp-servers", Name: "Remote MCP Servers"},
			{ID: "team-internal", Name: "Team Internal", ServersURL: "http://example.test/x", Protocol: "modelcontextprotocol/registry"},
		},
	}}
	got, err := rt.ListRegistries()
	require.NoError(t, err)
	ids := registryIDsFromList(t, got)

	for _, gone := range []string{"pulse", "smithery", "fleur", "azure-mcp-demo", "remote-mcp-servers"} {
		assert.NotContainsf(t, ids, gone, "deprecated former-default %q must not resurface from persisted config", gone)
	}
	assert.Contains(t, ids, "team-internal", "a genuine user-added custom registry must be preserved")
	for _, d := range defaults {
		assert.Contains(t, ids, d.ID, "built-in default %q must be present", d.ID)
	}
	assert.Len(t, ids, len(defaults)+1, "list must be exactly the 3 defaults plus the custom registry")
}
