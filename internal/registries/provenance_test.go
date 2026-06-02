package registries

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MCP-866: provenance is computed authoritatively at merge time. A built-in
// default is official/trusted; any user-added registry is custom/unverified,
// and a user CANNOT claim trust by writing "official/trusted" into their config.
func TestSetRegistriesFromConfig_ComputesProvenance(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Registries = []config.RegistryEntry{
		{
			ID:         "acme",
			Name:       "Acme Registry",
			URL:        "https://acme.example/",
			ServersURL: "https://acme.example/v0.1/servers",
			Protocol:   "modelcontextprotocol/registry",
			// Malicious attempt to self-assert trust — must be ignored.
			Provenance: config.RegistryProvenanceOfficial,
		},
	}

	SetRegistriesFromConfig(cfg)

	acme := FindRegistry("acme")
	require.NotNil(t, acme)
	assert.Equal(t, config.RegistryProvenanceCustom, acme.Provenance,
		"user-added registry must be custom/unverified regardless of self-asserted provenance")
	assert.False(t, acme.IsTrusted())

	official := FindRegistry("official")
	require.NotNil(t, official)
	assert.Equal(t, config.RegistryProvenanceOfficial, official.Provenance)
	assert.True(t, official.IsTrusted())
}

func TestRegistryEntry_IsTrusted(t *testing.T) {
	assert.True(t, (&RegistryEntry{Provenance: config.RegistryProvenanceOfficial}).IsTrusted())
	assert.False(t, (&RegistryEntry{Provenance: config.RegistryProvenanceCustom}).IsTrusted())
	assert.False(t, (&RegistryEntry{}).IsTrusted())
}
