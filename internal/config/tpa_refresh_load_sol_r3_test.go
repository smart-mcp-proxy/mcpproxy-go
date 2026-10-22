package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sol round-3 (c7): a negative tpa_stale_after_days is malformed. On the
// file-load path it must be dropped to the default with a value-free
// diagnostic and the fetch leg forced off, not silently disable freshness
// warnings.
func TestSolR3_LoadFromFile_NegativeStaleAfterDaysIsMalformed(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	doc := `{"listen":"127.0.0.1:18080","data_dir":"` + filepath.ToSlash(dir) + `","security":{` +
		`"tpa_fetch_enabled":true,"tpa_fetch_url":"https://b.example.test/x","tpa_stale_after_days":-7}}`
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Security)
	assert.Equal(t, DefaultTPAStaleAfterDays, cfg.Security.EffectiveTPAStaleAfterDays())
	assert.False(t, cfg.Security.IsTPAFetchEnabled(), "fetch is treated as off")

	var found bool
	for _, d := range cfg.LoadDiagnostics() {
		assert.NotContains(t, d.Message, "-7", "the warning must not echo the value")
		if d.Key == "security.tpa_stale_after_days" {
			found = true
		}
	}
	assert.True(t, found, "expected a diagnostic, got %+v", cfg.LoadDiagnostics())
}

func TestSolR3_LoadFromFile_ExplicitZeroStaleAfterDaysPreserved(t *testing.T) {
	t.Setenv(EnvTPAFetchEnabled, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	doc := `{"listen":"127.0.0.1:18080","data_dir":"` + filepath.ToSlash(dir) + `","security":{` +
		`"tpa_fetch_enabled":true,"tpa_fetch_url":"https://b.example.test/x","tpa_stale_after_days":0}}`
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.Security.EffectiveTPAStaleAfterDays())
	assert.True(t, cfg.Security.IsTPAFetchEnabled())
	assert.Empty(t, cfg.LoadDiagnostics())
}

func TestSolR3_EffectiveTPAStaleAfterDaysNegativeUsesDefault(t *testing.T) {
	neg, zero := -3, 0
	assert.Equal(t, DefaultTPAStaleAfterDays, (&SecurityConfig{TPAStaleAfterDays: &neg}).EffectiveTPAStaleAfterDays())
	assert.Equal(t, 0, (&SecurityConfig{TPAStaleAfterDays: &zero}).EffectiveTPAStaleAfterDays())
}
