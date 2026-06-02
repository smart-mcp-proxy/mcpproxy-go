package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MCP-866: provenance/trust model for registries.

func TestDefaultRegistries_AreOfficialTrusted(t *testing.T) {
	for _, r := range DefaultRegistries() {
		assert.Equalf(t, RegistryProvenanceOfficial, r.Provenance,
			"built-in default %q must be tagged official/trusted", r.ID)
		assert.Truef(t, r.IsTrusted(), "built-in default %q must report IsTrusted", r.ID)
	}
}

func TestRegistryEntry_IsTrusted(t *testing.T) {
	assert.True(t, (&RegistryEntry{Provenance: RegistryProvenanceOfficial}).IsTrusted())
	assert.False(t, (&RegistryEntry{Provenance: RegistryProvenanceCustom}).IsTrusted())
	// Absent provenance is NOT trusted — never grant trust by omission.
	assert.False(t, (&RegistryEntry{}).IsTrusted())
}

func TestRegistryEntry_ProvenanceJSONRoundTrip(t *testing.T) {
	in := RegistryEntry{ID: "acme", Name: "Acme", URL: "https://acme.example/", Provenance: RegistryProvenanceCustom}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"provenance":"custom/unverified"`)

	var out RegistryEntry
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, RegistryProvenanceCustom, out.Provenance)
}

// A server added from a custom/unverified registry must never carry
// skip_quarantine=true; config validation rejects it (FR: quarantine-always).
func TestValidate_RejectsSkipQuarantineForCustomOriginServer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Servers = []*ServerConfig{
		{
			Name:                     "evil",
			Protocol:                 "stdio",
			Command:                  "npx",
			Enabled:                  true,
			SkipQuarantine:           true,
			SourceRegistryID:         "acme",
			SourceRegistryProvenance: RegistryProvenanceCustom,
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skip_quarantine")
}

// skip_quarantine remains allowed for trusted/official-origin servers and for
// servers with no registry origin (manually-added), to avoid breaking existing
// configs.
func TestValidate_AllowsSkipQuarantineForTrustedOrigin(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Servers = []*ServerConfig{
		{Name: "ok-trusted", Protocol: "stdio", Command: "npx", Enabled: true, SkipQuarantine: true, SourceRegistryProvenance: RegistryProvenanceOfficial},
		{Name: "ok-manual", Protocol: "stdio", Command: "npx", Enabled: true, SkipQuarantine: true},
	}
	assert.NoError(t, cfg.Validate())
}
