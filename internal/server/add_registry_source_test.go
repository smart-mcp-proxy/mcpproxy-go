package server

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// MCP-866: `registry add-source` derivation core.

func TestBuildRegistrySourceEntry_DerivesFieldsAndProvenance(t *testing.T) {
	entry, err := buildRegistrySourceEntry("https://registry.acme.example/", "", "", "")
	require.NoError(t, err)

	assert.Equal(t, config.RegistryProvenanceCustom, entry.Provenance, "user-added source is always custom/unverified")
	assert.Equal(t, "modelcontextprotocol/registry", entry.Protocol, "default protocol")
	assert.NotEmpty(t, entry.ID, "id derived from host")
	assert.Equal(t, "https://registry.acme.example/", entry.URL)
	// servers_url points at the v0.1 servers collection for the generic protocol.
	assert.Equal(t, "https://registry.acme.example/v0.1/servers", entry.ServersURL)
}

func TestBuildRegistrySourceEntry_RejectsNonHTTPS(t *testing.T) {
	for _, bad := range []string{"http://acme.example/", "ftp://acme.example", "not a url", "", "https://"} {
		_, err := buildRegistrySourceEntry(bad, "", "", "")
		require.Errorf(t, err, "must reject %q", bad)
		assert.Truef(t, errors.Is(err, ErrInvalidRegistryURL), "want ErrInvalidRegistryURL for %q, got %v", bad, err)
	}
}

func TestBuildRegistrySourceEntry_HonorsExplicitIDNameAndServersURL(t *testing.T) {
	entry, err := buildRegistrySourceEntry("https://acme.example/v0.1/servers", "modelcontextprotocol/registry", "acme", "Acme Corp")
	require.NoError(t, err)
	assert.Equal(t, "acme", entry.ID)
	assert.Equal(t, "Acme Corp", entry.Name)
	// A URL already pointing at a servers collection is used verbatim.
	assert.Equal(t, "https://acme.example/v0.1/servers", entry.ServersURL)
}

func TestValidateNewRegistrySource_RejectsLocked(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.RegistriesLocked = true
	err := validateNewRegistrySource(cfg, config.RegistryEntry{ID: "acme", URL: "https://acme.example/"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRegistriesLocked))
}

func TestValidateNewRegistrySource_RejectsShadowingBuiltin(t *testing.T) {
	cfg := config.DefaultConfig()
	// "official" is a shipped default — a user must not be able to shadow it.
	err := validateNewRegistrySource(cfg, config.RegistryEntry{ID: "official", URL: "https://evil.example/"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRegistryShadowsBuiltin))
}

func TestValidateNewRegistrySource_RejectsDuplicateCustom(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Registries = []config.RegistryEntry{{ID: "acme", URL: "https://acme.example/", Provenance: config.RegistryProvenanceCustom}}
	err := validateNewRegistrySource(cfg, config.RegistryEntry{ID: "acme", URL: "https://acme.example/"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDuplicateRegistry))
}

func TestValidateNewRegistrySource_AllowsNewCustom(t *testing.T) {
	cfg := config.DefaultConfig()
	assert.NoError(t, validateNewRegistrySource(cfg, config.RegistryEntry{ID: "acme", URL: "https://acme.example/"}))
}
