package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sol round-2 (c7 #1): the boot tolerance covers all four refresh keys, not only
// the interval. A wrong JSON type for tpa_stale_after_days, tpa_fetch_enabled or
// tpa_fetch_url must not fail the load; the bad key is dropped with a value-free
// diagnostic and the fetch leg is treated as off.
func TestSolR2_LoadFromFile_MalformedTPARefreshKeysDoNotBreakBoot(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "")
	cases := []struct {
		name    string
		sec     string
		key     string
		secret  string
		wantURL string
		wantDay int
	}{
		{"stale days string", `"tpa_stale_after_days":"30secret","tpa_fetch_enabled":true,"tpa_fetch_url":"https://b.example.test/x"`, "security.tpa_stale_after_days", "30secret", "https://b.example.test/x", DefaultTPAStaleAfterDays},
		{"stale days fraction", `"tpa_stale_after_days":1.5,"tpa_fetch_enabled":true,"tpa_fetch_url":"https://b.example.test/x"`, "security.tpa_stale_after_days", "1.5", "https://b.example.test/x", DefaultTPAStaleAfterDays},
		{"fetch enabled string", `"tpa_fetch_enabled":"truesecret","tpa_fetch_url":"https://b.example.test/x","tpa_stale_after_days":9`, "security.tpa_fetch_enabled", "truesecret", "https://b.example.test/x", 9},
		{"fetch url number", `"tpa_fetch_url":12345678,"tpa_fetch_enabled":true,"tpa_stale_after_days":9`, "security.tpa_fetch_url", "12345678", "", 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "mcp_config.json")
			doc := `{"listen":"127.0.0.1:18080","data_dir":"` + filepath.ToSlash(dir) + `","security":{` + tc.sec + `}}`
			require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))

			cfg, err := LoadFromFile(path)
			require.NoError(t, err, "a malformed refresh key must not fail the boot")
			require.NotNil(t, cfg.Security)
			assert.False(t, cfg.Security.IsTPAFetchEnabled(), "fetch is treated as off")
			assert.Equal(t, tc.wantDay, cfg.Security.EffectiveTPAStaleAfterDays())
			assert.Equal(t, tc.wantURL, cfg.Security.TPAFetchURL)

			var found bool
			for _, d := range cfg.LoadDiagnostics() {
				assert.NotContains(t, d.Message, tc.secret, "the warning must not echo the value")
				if d.Key == tc.key {
					found = true
					assert.Contains(t, d.Message, "fetch")
				}
			}
			assert.True(t, found, "expected a diagnostic for %s, got %+v", tc.key, cfg.LoadDiagnostics())
		})
	}
}

func TestSolR2_LoadFromFile_MultipleMalformedRefreshKeysEachWarn(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	doc := `{"listen":"127.0.0.1:18080","data_dir":"` + filepath.ToSlash(dir) + `","security":{` +
		`"tpa_refresh_interval":"24hours","tpa_stale_after_days":"x","tpa_fetch_enabled":"y","tpa_fetch_url":7}}`
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	var keys []string
	for _, d := range cfg.LoadDiagnostics() {
		keys = append(keys, d.Key)
	}
	assert.Equal(t, []string{
		"security.tpa_refresh_interval", "security.tpa_fetch_enabled",
		"security.tpa_fetch_url", "security.tpa_stale_after_days",
	}, keys)
}

func TestSolR2_ValidRefreshKeysUntouchedAndWritesStayStrict(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	doc := `{"listen":"127.0.0.1:18080","data_dir":"` + filepath.ToSlash(dir) + `","security":{` +
		`"tpa_fetch_enabled":true,"tpa_fetch_url":"https://b.example.test/x","tpa_stale_after_days":12}}`
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.True(t, cfg.Security.IsTPAFetchEnabled())
	assert.Equal(t, 12, cfg.Security.EffectiveTPAStaleAfterDays())
	assert.Empty(t, cfg.LoadDiagnostics())

	// API-style typed decode stays strict.
	var strict Config
	require.Error(t, json.Unmarshal([]byte(`{"security":{"tpa_stale_after_days":"30"}}`), &strict))
	require.Error(t, json.Unmarshal([]byte(`{"security":{"tpa_fetch_enabled":"true"}}`), &strict))
}
