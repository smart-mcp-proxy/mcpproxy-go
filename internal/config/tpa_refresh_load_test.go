package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sol round-1 (c7): a hand-edited malformed tpa_refresh_interval must never
// stop the daemon booting. The shared Duration decoder rejects "24hours" during
// the typed decode, before Validate runs, so the file-load path normalizes the
// value first: a value-free diagnostic, the key dropped, fetch treated as off.
// API writes keep decoding strictly.
func TestLoadFromFile_MalformedTPARefreshIntervalDoesNotBreakBoot(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "")
	for name, raw := range map[string]string{
		"unparsable string": `"24hours"`,
		"bare number":       `24`,
		"null":              `null`,
		"object":            `{"every":"24h"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "mcp_config.json")
			doc := `{"listen":"127.0.0.1:18080","data_dir":"` + filepath.ToSlash(dir) + `","security":{` +
				`"tpa_refresh_interval":` + raw + `,"tpa_fetch_enabled":true,` +
				`"tpa_fetch_url":"https://bundles.example.test/b.json","tpa_stale_after_days":9}}`
			require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))

			cfg, err := LoadFromFile(path)
			require.NoError(t, err, "a malformed refresh interval must not fail the boot")
			require.NotNil(t, cfg.Security)

			assert.Equal(t, DefaultTPARefreshInterval, cfg.Security.EffectiveTPARefreshInterval())
			assert.False(t, cfg.Security.IsTPAFetchEnabled(), "fetch is treated as off")
			assert.Equal(t, 9, cfg.Security.EffectiveTPAStaleAfterDays(), "unrelated keys survive the normalization")
			assert.Equal(t, "https://bundles.example.test/b.json", cfg.Security.TPAFetchURL)

			var found bool
			for _, d := range cfg.LoadDiagnostics() {
				if d.Key == "security.tpa_refresh_interval" {
					found = true
					assert.NotContains(t, d.Message, "24hours", "the warning must not echo the value")
					assert.NotContains(t, d.Message, "every")
					assert.Contains(t, d.Message, "fetch")
				}
			}
			assert.True(t, found, "expected a value-free load diagnostic for security.tpa_refresh_interval, got %+v", cfg.LoadDiagnostics())
		})
	}
}

func TestLoadFromFile_ValidTPARefreshIntervalUntouched(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	doc := `{"listen":"127.0.0.1:18080","data_dir":"` + filepath.ToSlash(dir) + `","security":{` +
		`"tpa_refresh_interval":"6h","tpa_fetch_enabled":true,"tpa_fetch_url":"https://bundles.example.test/b.json"}}`
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, 6*time.Hour, cfg.Security.EffectiveTPARefreshInterval())
	assert.True(t, cfg.Security.IsTPAFetchEnabled())
	for _, d := range cfg.LoadDiagnostics() {
		assert.NotEqual(t, "security.tpa_refresh_interval", d.Key)
	}
}

// The API write path (a plain typed decode) stays strict.
func TestTPARefreshInterval_StrictOutsideFileLoad(t *testing.T) {
	var cfg Config
	err := json.Unmarshal([]byte(`{"security":{"tpa_refresh_interval":"24hours"}}`), &cfg)
	require.Error(t, err)
}
