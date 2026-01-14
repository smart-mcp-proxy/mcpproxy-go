package registries

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

var registryList []RegistryEntry

// SetRegistriesFromConfig sets the registries list from configuration
func SetRegistriesFromConfig(cfg *config.Config) {
	if cfg != nil && cfg.Registries != nil {
		// Convert config.RegistryEntry to registries.RegistryEntry
		registryList = make([]RegistryEntry, len(cfg.Registries))
		for i := range cfg.Registries {
			r := &cfg.Registries[i]
			registryList[i] = RegistryEntry{
				ID:          r.ID,
				Name:        r.Name,
				Description: r.Description,
				URL:         r.URL,
				ServersURL:  r.ServersURL,
				Tags:        r.Tags,
				Protocol:    r.Protocol,
				Count:       r.Count,
			}
		}
	} else {
		// Use default registries
		registryList = []RegistryEntry{
			{
				ID:          "smithery",
				Name:        "Smithery MCP Registry",
				Description: "The official community registry for Model Context Protocol (MCP) servers.",
				URL:         "https://smithery.ai/protocols",
				ServersURL:  "https://smithery.ai/api/smithery-protocol-registry",
				Tags:        []string{"official", "community"},
				Protocol:    "modelcontextprotocol/registry",
				Count:       -1, // Will be populated at runtime
			},
		}
	}
}

// ListRegistries returns a copy of all available registries
func ListRegistries() []RegistryEntry {
	result := make([]RegistryEntry, len(registryList))
	copy(result, registryList)
	return result
}

// FindRegistry finds a registry by ID or name (case-insensitive)
func FindRegistry(idOrName string) *RegistryEntry {
	for i := range registryList {
		r := &registryList[i]
		if equalIgnoreCase(r.ID, idOrName) || equalIgnoreCase(r.Name, idOrName) {
			return &registryList[i]
		}
	}
	return nil
}

func equalIgnoreCase(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ac := a[i]
		bc := b[i]
		if ac >= 'A' && ac <= 'Z' {
			ac += 'a' - 'A'
		}
		if bc >= 'A' && bc <= 'Z' {
			bc += 'a' - 'A'
		}
		if ac != bc {
			return false
		}
	}
	return true
}
