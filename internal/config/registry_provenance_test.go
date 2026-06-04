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
			"built-in default %q must be tagged official", r.ID)
		assert.Equalf(t, "official", r.Provenance, "provenance value must be the plain two-value form")
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
	assert.Contains(t, string(b), `"provenance":"custom"`)

	var out RegistryEntry
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, RegistryProvenanceCustom, out.Provenance)
}

// MCP-1072: provenance no longer gates skip_quarantine. A server sourced from a
// custom registry may carry skip_quarantine=true just like any other server;
// validation must NOT reject it.
func TestValidate_AllowsSkipQuarantineForCustomOriginServer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Servers = []*ServerConfig{
		{
			Name:                     "ok-custom",
			Protocol:                 "stdio",
			Command:                  "npx",
			Enabled:                  true,
			SkipQuarantine:           true,
			SourceRegistryID:         "acme",
			SourceRegistryProvenance: RegistryProvenanceCustom,
		},
		{Name: "ok-official", Protocol: "stdio", Command: "npx", Enabled: true, SkipQuarantine: true, SourceRegistryProvenance: RegistryProvenanceOfficial},
		{Name: "ok-manual", Protocol: "stdio", Command: "npx", Enabled: true, SkipQuarantine: true},
	}
	assert.NoError(t, cfg.Validate())
}

// MCP-1072: NormalizeRegistryProvenance maps legacy two-word strings onto the
// plain vocabulary and is idempotent.
func TestNormalizeRegistryProvenance(t *testing.T) {
	cases := map[string]string{
		"official/trusted":  "official",
		"custom/unverified": "custom",
		"official":          "official",
		"custom":            "custom",
		"":                  "",
		"weird":             "weird", // unknown values pass through untouched
	}
	for in, want := range cases {
		assert.Equalf(t, want, NormalizeRegistryProvenance(in), "normalize(%q)", in)
		// Idempotent: normalizing the result again is a no-op.
		assert.Equal(t, want, NormalizeRegistryProvenance(want))
	}
}

// MCP-1072: a config loaded with legacy provenance strings must normalize both
// registry entries and per-server source provenance on read.
func TestNormalizeRegistryProvenanceValues(t *testing.T) {
	cfg := &Config{
		Registries: []RegistryEntry{
			{ID: "acme", Provenance: "custom/unverified"},
			{ID: "official", Provenance: "official/trusted"},
		},
		Servers: []*ServerConfig{
			{Name: "s1", SourceRegistryProvenance: "custom/unverified"},
			{Name: "s2", SourceRegistryProvenance: "official/trusted"},
			nil, // nil-safe
		},
	}
	normalizeRegistryProvenanceValues(cfg)
	assert.Equal(t, "custom", cfg.Registries[0].Provenance)
	assert.Equal(t, "official", cfg.Registries[1].Provenance)
	assert.Equal(t, "custom", cfg.Servers[0].SourceRegistryProvenance)
	assert.Equal(t, "official", cfg.Servers[1].SourceRegistryProvenance)

	// nil config is safe.
	normalizeRegistryProvenanceValues(nil)
}
