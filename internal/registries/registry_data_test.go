package registries

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// defaultRegistryIDs are the built-in registries shipped in
// config.DefaultConfig(). A user-supplied config must MERGE with these, not
// replace them (FR-006). The set was standardized on the official MCP registry
// protocol (MCP-865): official + built-in reference primary, Docker kept, Pulse
// and Smithery demoted to opt-in.
var defaultRegistryIDs = []string{
	"official",
	"reference",
	"docker-mcp-catalog",
	"pulse",
	"smithery",
}

func registryIDSet(t *testing.T) map[string]RegistryEntry {
	t.Helper()
	out := map[string]RegistryEntry{}
	for _, r := range ListRegistries() {
		out[r.ID] = r
	}
	return out
}

// FR-006: a custom registry from config must not drop the 5 built-in defaults.
func TestSetRegistriesFromConfig_MergesCustomWithDefaults(t *testing.T) {
	cfg := &config.Config{
		Registries: []config.RegistryEntry{
			{ID: "mycorp", Name: "My Corp Registry", ServersURL: "https://reg.mycorp.example/servers"},
		},
	}

	SetRegistriesFromConfig(cfg)

	got := registryIDSet(t)
	for _, id := range defaultRegistryIDs {
		if _, ok := got[id]; !ok {
			t.Errorf("default registry %q was dropped after merging a custom entry", id)
		}
	}
	if _, ok := got["mycorp"]; !ok {
		t.Errorf("custom registry %q missing after merge", "mycorp")
	}
	if len(got) != len(defaultRegistryIDs)+1 {
		t.Errorf("expected %d registries after merge, got %d", len(defaultRegistryIDs)+1, len(got))
	}
}

// FR-006: a config entry whose ID collides with a default overrides it in place
// (no duplicate, default count preserved).
func TestSetRegistriesFromConfig_CustomOverridesDefaultByID(t *testing.T) {
	cfg := &config.Config{
		Registries: []config.RegistryEntry{
			{ID: "pulse", Name: "Pulse OVERRIDDEN", ServersURL: "https://override.example/servers"},
		},
	}

	SetRegistriesFromConfig(cfg)

	got := registryIDSet(t)
	if len(got) != len(defaultRegistryIDs) {
		t.Errorf("override should not change registry count: want %d got %d", len(defaultRegistryIDs), len(got))
	}
	if got["pulse"].Name != "Pulse OVERRIDDEN" {
		t.Errorf("colliding-ID config entry did not override default: got name %q", got["pulse"].Name)
	}
}

// Nil/empty config yields exactly the built-in defaults.
func TestSetRegistriesFromConfig_NilConfigUsesDefaults(t *testing.T) {
	SetRegistriesFromConfig(nil)

	got := registryIDSet(t)
	if len(got) != len(defaultRegistryIDs) {
		t.Errorf("nil config should give %d defaults, got %d", len(defaultRegistryIDs), len(got))
	}
	for _, id := range defaultRegistryIDs {
		if _, ok := got[id]; !ok {
			t.Errorf("default registry %q missing for nil config", id)
		}
	}
}
