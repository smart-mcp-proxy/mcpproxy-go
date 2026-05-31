package registries

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

var registryList []RegistryEntry

// SetRegistriesFromConfig builds the effective registry list by MERGING the
// built-in defaults with the user's configured registries, keyed by ID
// (FR-006). Built-in defaults come first (in their canonical order); a config
// entry with a new ID is appended, and a config entry whose ID collides with a
// default overrides it in place. This means adding one custom registry no
// longer drops the shipped defaults, and no rebuild is required.
func SetRegistriesFromConfig(cfg *config.Config) {
	index := make(map[string]int) // ID -> position in merged
	merged := make([]RegistryEntry, 0, len(config.DefaultRegistries()))

	upsert := func(r RegistryEntry) {
		if pos, ok := index[r.ID]; ok {
			merged[pos] = r
			return
		}
		index[r.ID] = len(merged)
		merged = append(merged, r)
	}

	defaults := config.DefaultRegistries()
	for i := range defaults {
		upsert(fromConfigEntry(&defaults[i]))
	}
	if cfg != nil {
		for i := range cfg.Registries {
			upsert(fromConfigEntry(&cfg.Registries[i]))
		}
	}

	registryList = merged
}

// fromConfigEntry converts a config.RegistryEntry to a registries.RegistryEntry.
func fromConfigEntry(r *config.RegistryEntry) RegistryEntry {
	return RegistryEntry{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		URL:         r.URL,
		ServersURL:  r.ServersURL,
		Tags:        r.Tags,
		Protocol:    r.Protocol,
		Count:       r.Count,
		RequiresKey: r.RequiresKey,
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
