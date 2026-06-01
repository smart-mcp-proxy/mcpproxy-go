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
	assert.NotContains(t, idsEmpty, "smithery", "legacy hard-coded Smithery must not leak when defaults exist")

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
