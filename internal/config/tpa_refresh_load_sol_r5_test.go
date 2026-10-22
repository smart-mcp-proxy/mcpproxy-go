package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const solR5Signer = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func loadDocR5(t *testing.T, doc string) *Config {
	t.Helper()
	t.Setenv(EnvTPAFetchEnabled, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(doc, "@DIR@", filepath.ToSlash(dir))), 0o600))
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Security)
	return cfg
}

// Sol round-5 (c7): the boot normaliser must repair only the refresh values.
// A document with a malformed refresh key must load to exactly what the typed
// decoder alone produces for the same document with that key absent
// (and the fetch leg off), including every other security setting spread over
// repeated or case-variant `security` objects.
func TestSolR5_RepeatedSecurityObjectsKeepIndependentSettings(t *testing.T) {
	head := `{"listen":"127.0.0.1:18080","data_dir":"@DIR@",`
	cases := map[string]struct{ bad, without string }{
		"malformed in the second object": {
			bad:     head + `"security":{"require_signed_bundle":true,"tpa_active_root":"operator","tpa_operator_signers":["` + solR5Signer + `"],"tpa_operator_baseline":7},"security":{"tpa_refresh_interval":"24hours","tpa_operator_epoch":3}}`,
			without: head + `"security":{"require_signed_bundle":true,"tpa_active_root":"operator","tpa_operator_signers":["` + solR5Signer + `"],"tpa_operator_baseline":7},"security":{"tpa_operator_epoch":3}}`,
		},
		"malformed in the first object": {
			bad:     head + `"security":{"tpa_refresh_interval":"24hours"},"security":{"require_signed_bundle":true,"tpa_active_root":"operator","tpa_operator_signers":["` + solR5Signer + `"]}}`,
			without: head + `"security":{},"security":{"require_signed_bundle":true,"tpa_active_root":"operator","tpa_operator_signers":["` + solR5Signer + `"]}}`,
		},
		"case-variant objects and keys": {
			bad:     head + `"Security":{"REQUIRE_SIGNED_BUNDLE":true},"SECURITY":{"Tpa_Refresh_Interval":"24hours","TPA_ACTIVE_ROOT":"publisher"}}`,
			without: head + `"Security":{"REQUIRE_SIGNED_BUNDLE":true},"SECURITY":{"TPA_ACTIVE_ROOT":"publisher"}}`,
		},
		"malformed stale days across objects": {
			bad:     head + `"security":{"require_signed_bundle":true},"security":{"tpa_stale_after_days":"x"}}`,
			without: head + `"security":{"require_signed_bundle":true},"security":{}}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := loadDocR5(t, tc.bad)
			want := loadDocR5(t, tc.without)
			assert.True(t, got.Security.RequireSignedBundle == want.Security.RequireSignedBundle)
			assert.Equal(t, want.Security, got.Security, "only the malformed refresh key may differ")
			assert.False(t, got.Security.IsTPAFetchEnabled())
			assert.NotEmpty(t, got.LoadDiagnostics())
		})
	}
}

func TestSolR5_RequireSignedBundleSurvivesMalformedInterval(t *testing.T) {
	cfg := loadDocR5(t, `{"listen":"127.0.0.1:18080","data_dir":"@DIR@",`+
		`"security":{"require_signed_bundle":true},"security":{"tpa_refresh_interval":"24hours"}}`)
	assert.True(t, cfg.Security.RequireSignedBundle, "trust policy must not be dropped by the repair")
}

// The repair splices bytes: every byte outside a replaced refresh value is
// preserved (formatting, key order, unrelated and nested values).
func TestSolR5_NormaliserSplicesInPlace(t *testing.T) {
	in := "{\n  \"security\" :  { \"tpa_fetch_enabled\" : true ,\n    \"Tpa_Refresh_Interval\":   \"24hours\" ,\n    \"require_signed_bundle\":true },\n  \"mcpServers\": [ {\"name\":\"a\" , \"n\": 1.50e2} ],\n  \"security\":{\"tpa_fetch_url\":\"https://x.example.test/y\"}\n}\n"
	out, diags := normalizeTPARefreshInterval([]byte(in))
	want := "{\n  \"security\" :  { \"tpa_fetch_enabled\" : false ,\n    \"Tpa_Refresh_Interval\":   \"0s\" ,\n    \"require_signed_bundle\":true },\n  \"mcpServers\": [ {\"name\":\"a\" , \"n\": 1.50e2} ],\n  \"security\":{\"tpa_fetch_url\":\"https://x.example.test/y\"}\n}\n"
	assert.Equal(t, want, string(out))
	require.Len(t, diags, 1)
	assert.Equal(t, "security."+tpaRefreshIntervalKey, diags[0].Key)
}

func TestSolR5_NormaliserDuplicatesReplacedEverywhere(t *testing.T) {
	in := `{"security":{"tpa_stale_after_days":3,"x":1},"security":{"tpa_stale_after_days":9,"tpa_fetch_enabled":true}}`
	out, diags := normalizeTPARefreshInterval([]byte(in))
	assert.Equal(t, `{"security":{"tpa_stale_after_days":null,"x":1},"security":{"tpa_stale_after_days":null,"tpa_fetch_enabled":false}}`, string(out))
	require.Len(t, diags, 1)
}

func TestSolR5_NormaliserValidAndUnscannableUntouched(t *testing.T) {
	valid := []byte(`{"security":{"tpa_fetch_enabled":true,"tpa_refresh_interval":"2h"},"a":1}`)
	out, diags := normalizeTPARefreshInterval(valid)
	assert.Equal(t, string(valid), string(out))
	assert.Empty(t, diags)

	broken := []byte(`{"security":{"tpa_refresh_interval":"24hours",}`)
	out, diags = normalizeTPARefreshInterval(broken)
	assert.Equal(t, string(broken), string(out))
	assert.Empty(t, diags)
}

// Non-object security values and refresh-looking keys elsewhere are ignored.
func TestSolR5_NormaliserIgnoresOtherPlaces(t *testing.T) {
	in := []byte(`{"security":null,"other":{"tpa_refresh_interval":"bad"},"security":{"nested":{"tpa_refresh_interval":"bad"}}}`)
	out, diags := normalizeTPARefreshInterval(in)
	assert.Equal(t, string(in), string(out))
	assert.Empty(t, diags)
}
