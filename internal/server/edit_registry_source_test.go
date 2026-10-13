package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// MCP-1072: editing a custom registry updates name/url/servers-url and
// re-derives the servers URL when only the base URL changes.
func TestEditRegistrySourceInConfig_CustomUpdate(t *testing.T) {
	cfg := &config.Config{
		Registries: []config.RegistryEntry{
			{ID: "acme", Name: "Acme", URL: "https://acme.example/", ServersURL: "https://acme.example/v0.1/servers", Provenance: config.RegistryProvenanceCustom},
		},
	}

	updated, remaining, err := editRegistrySourceInConfig(cfg, &EditRegistrySourceRequest{
		ID:   "acme",
		Name: "Acme Prod",
		URL:  "https://acme.example/api",
	})
	require.NoError(t, err)
	assert.Equal(t, "Acme Prod", updated.Name)
	assert.Equal(t, "https://acme.example/api", updated.URL)
	// GH #783: a URL that carries a path is a concrete endpoint — it is used
	// verbatim, never suffixed with a route MCPProxy invented. EditRegistrySource
	// probes the live URL on top of this offline derivation.
	assert.Equal(t, "https://acme.example/api", updated.ServersURL, "a path-carrying URL is used verbatim")
	assert.Equal(t, config.RegistryProvenanceCustom, updated.Provenance)
	// The returned slice is a fresh clone with the entry replaced; the original
	// config slice is untouched (copy-on-write).
	require.Len(t, remaining, 1)
	assert.Equal(t, "Acme Prod", remaining[0].Name)
	assert.Equal(t, "Acme", cfg.Registries[0].Name, "original config snapshot must not be mutated")
}

// A bare base URL is still pointed at the official v0.1 servers collection.
func TestEditRegistrySourceInConfig_DerivesFromBaseURL(t *testing.T) {
	cfg := &config.Config{
		Registries: []config.RegistryEntry{
			{ID: "acme", URL: "https://acme.example/", ServersURL: "https://acme.example/v0.1/servers", Provenance: config.RegistryProvenanceCustom},
		},
	}
	updated, _, err := editRegistrySourceInConfig(cfg, &EditRegistrySourceRequest{ID: "acme", URL: "https://acme2.example"})
	require.NoError(t, err)
	assert.Equal(t, "https://acme2.example/v0.1/servers", updated.ServersURL, "servers URL re-derived from the new base")
}

// An explicit servers-url overrides the derived one.
func TestEditRegistrySourceInConfig_ExplicitServersURL(t *testing.T) {
	cfg := &config.Config{
		Registries: []config.RegistryEntry{
			{ID: "acme", URL: "https://acme.example/", Provenance: config.RegistryProvenanceCustom},
		},
	}
	updated, _, err := editRegistrySourceInConfig(cfg, &EditRegistrySourceRequest{
		ID:         "acme",
		URL:        "https://acme.example/api",
		ServersURL: "https://acme.example/custom/servers",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://acme.example/custom/servers", updated.ServersURL)
}

// Editing a built-in registry is refused with registry_shadows_builtin.
func TestEditRegistrySourceInConfig_RefusesBuiltin(t *testing.T) {
	builtins := config.DefaultRegistries()
	require.NotEmpty(t, builtins)
	cfg := &config.Config{}

	_, _, err := editRegistrySourceInConfig(cfg, &EditRegistrySourceRequest{
		ID:   builtins[0].ID,
		Name: "hijack",
	})
	require.Error(t, err)
	assert.Equal(t, "registry_shadows_builtin", EditRegistrySourceErrorCode(err))
}

// Editing an unknown id yields registry_not_found.
func TestEditRegistrySourceInConfig_UnknownNotFound(t *testing.T) {
	cfg := &config.Config{Registries: []config.RegistryEntry{}}
	_, _, err := editRegistrySourceInConfig(cfg, &EditRegistrySourceRequest{ID: "nope", Name: "x"})
	require.Error(t, err)
	assert.Equal(t, "registry_not_found", EditRegistrySourceErrorCode(err))
}

// A non-https URL is rejected with invalid_registry_url.
func TestEditRegistrySourceInConfig_RejectsNonHTTPSURL(t *testing.T) {
	cfg := &config.Config{
		Registries: []config.RegistryEntry{
			{ID: "acme", URL: "https://acme.example/", Provenance: config.RegistryProvenanceCustom},
		},
	}
	_, _, err := editRegistrySourceInConfig(cfg, &EditRegistrySourceRequest{ID: "acme", URL: "http://insecure.example/"})
	require.Error(t, err)
	assert.Equal(t, "invalid_registry_url", EditRegistrySourceErrorCode(err))
}

// A locked registry set refuses edits.
func TestEditRegistrySourceInConfig_RefusesWhenLocked(t *testing.T) {
	cfg := &config.Config{
		RegistriesLocked: true,
		Registries: []config.RegistryEntry{
			{ID: "acme", URL: "https://acme.example/", Provenance: config.RegistryProvenanceCustom},
		},
	}
	_, _, err := editRegistrySourceInConfig(cfg, &EditRegistrySourceRequest{ID: "acme", Name: "x"})
	require.Error(t, err)
	assert.Equal(t, "registries_locked", EditRegistrySourceErrorCode(err))
}
