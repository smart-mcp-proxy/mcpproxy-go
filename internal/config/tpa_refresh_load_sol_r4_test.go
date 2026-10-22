package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadR4(t *testing.T, secBody string) *Config {
	t.Helper()
	t.Setenv(EnvTPAFetchEnabled, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	doc := `{"listen":"127.0.0.1:18080","data_dir":"` + filepath.ToSlash(dir) + `","security":{` + secBody + `}}`
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Security)
	return cfg
}

func diagKeysR4(cfg *Config) map[string]bool {
	m := map[string]bool{}
	for _, d := range cfg.LoadDiagnostics() {
		m[d.Key] = true
	}
	return m
}

// Sol round-4 (c7): a malformed occurrence of a refresh key followed by a
// valid duplicate must not stop boot (map decoding keeps only the last one,
// the typed decode trips on the first).
func TestSolR4_DuplicateRefreshKey_MalformedThenValid(t *testing.T) {
	cases := map[string]string{
		"security.tpa_refresh_interval": `"tpa_refresh_interval":"24hours","tpa_refresh_interval":"24h"`,
		"security.tpa_fetch_enabled":    `"tpa_fetch_enabled":"yes","tpa_fetch_enabled":true`,
		"security.tpa_fetch_url":        `"tpa_fetch_url":5,"tpa_fetch_url":"https://b.example.test/x"`,
		"security.tpa_stale_after_days": `"tpa_stale_after_days":"x","tpa_stale_after_days":7`,
	}
	for key, body := range cases {
		t.Run(key, func(t *testing.T) {
			// The duplicated key is the only refresh key in the document so
			// the other keys cannot mask a failure.
			cfg := loadR4(t, body)
			assert.False(t, cfg.Security.IsTPAFetchEnabled(), "fetch is treated as off")
			assert.True(t, diagKeysR4(cfg)[key], "diagnostics: %+v", cfg.LoadDiagnostics())
			for _, d := range cfg.LoadDiagnostics() {
				assert.NotContains(t, d.Message, "24hours")
			}
		})
	}
}

// Duplicates that are individually valid are still ambiguous: drop them.
func TestSolR4_DuplicateRefreshKey_BothValidIsAmbiguous(t *testing.T) {
	cfg := loadR4(t, `"tpa_fetch_enabled":true,"tpa_fetch_url":"https://b.example.test/x",`+
		`"tpa_refresh_interval":"1h","tpa_refresh_interval":"2h"`)
	assert.False(t, cfg.Security.IsTPAFetchEnabled())
	assert.True(t, diagKeysR4(cfg)["security.tpa_refresh_interval"])
}

// A single well-formed key is untouched.
func TestSolR4_SingleValidRefreshKeysUnchanged(t *testing.T) {
	cfg := loadR4(t, `"tpa_fetch_enabled":true,"tpa_fetch_url":"https://b.example.test/x","tpa_refresh_interval":"2h"`)
	assert.True(t, cfg.Security.IsTPAFetchEnabled())
	assert.Empty(t, cfg.LoadDiagnostics())
}
