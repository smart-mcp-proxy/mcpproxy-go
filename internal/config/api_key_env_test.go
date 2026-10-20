package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeKeyFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api_key")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// unsetEnv removes name for the test and restores it afterwards (t.Setenv
// records the original value; the Unsetenv that follows leaves it unset).
func unsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	require.NoError(t, os.Unsetenv(name))
}

func TestResolveEnvAPIKey(t *testing.T) {
	t.Run("neither variable", func(t *testing.T) {
		unsetEnv(t, EnvAPIKey)
		t.Setenv(EnvAPIKeyFile, "")
		res, err := ResolveEnvAPIKey()
		require.NoError(t, err)
		assert.False(t, res.Set)
		assert.Empty(t, res.Key)
	})

	t.Run("key only", func(t *testing.T) {
		t.Setenv(EnvAPIKey, "env-key")
		t.Setenv(EnvAPIKeyFile, "")
		res, err := ResolveEnvAPIKey()
		require.NoError(t, err)
		assert.True(t, res.Set)
		assert.Equal(t, "env-key", res.Key)
		assert.False(t, res.FromFile)
		assert.False(t, res.Shadowed)
	})

	t.Run("an empty key variable is still set (it disables authentication)", func(t *testing.T) {
		t.Setenv(EnvAPIKey, "")
		t.Setenv(EnvAPIKeyFile, "")
		res, err := ResolveEnvAPIKey()
		require.NoError(t, err)
		assert.True(t, res.Set)
		assert.Empty(t, res.Key)
	})

	t.Run("file only", func(t *testing.T) {
		path := writeKeyFile(t, "file-key\n")
		unsetEnv(t, EnvAPIKey)
		t.Setenv(EnvAPIKeyFile, path)
		res, err := ResolveEnvAPIKey()
		require.NoError(t, err)
		assert.True(t, res.Set)
		assert.Equal(t, "file-key", res.Key, "one trailing newline is removed")
		assert.True(t, res.FromFile)
		assert.Equal(t, path, res.Path)
		assert.False(t, res.Shadowed)
	})

	t.Run("CRLF file", func(t *testing.T) {
		unsetEnv(t, EnvAPIKey)
		t.Setenv(EnvAPIKeyFile, writeKeyFile(t, "file-key\r\n"))
		res, err := ResolveEnvAPIKey()
		require.NoError(t, err)
		assert.Equal(t, "file-key", res.Key)
	})

	t.Run("both set: the file wins and the result says so", func(t *testing.T) {
		t.Setenv(EnvAPIKey, "env-key")
		t.Setenv(EnvAPIKeyFile, writeKeyFile(t, "file-key"))
		res, err := ResolveEnvAPIKey()
		require.NoError(t, err)
		assert.Equal(t, "file-key", res.Key)
		assert.True(t, res.FromFile)
		assert.True(t, res.Shadowed)
	})

	t.Run("an empty file variable is unset", func(t *testing.T) {
		t.Setenv(EnvAPIKey, "env-key")
		t.Setenv(EnvAPIKeyFile, "")
		res, err := ResolveEnvAPIKey()
		require.NoError(t, err)
		assert.Equal(t, "env-key", res.Key)
		assert.False(t, res.FromFile)
	})
}

// A named file that cannot be used is an error and never falls back to the
// other variable, nor to a generated key.
func TestResolveEnvAPIKey_UnusableFileIsAnError(t *testing.T) {
	const fallback = "must-not-be-used"
	cases := map[string]string{
		"missing":         filepath.Join(t.TempDir(), "nope"),
		"relative":        "run/secrets/key",
		"empty":           writeKeyFile(t, ""),
		"newline only":    writeKeyFile(t, "\n"),
		"whitespace only": writeKeyFile(t, "  \t\n"),
		"directory":       t.TempDir(),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(EnvAPIKey, fallback)
			t.Setenv(EnvAPIKeyFile, path)
			res, err := ResolveEnvAPIKey()
			require.Error(t, err)
			assert.Contains(t, err.Error(), EnvAPIKeyFile)
			assert.False(t, res.Set)
			assert.NotContains(t, err.Error(), fallback)
		})
	}
}

func TestEnsureAPIKey_FileOverridesConfigAndEnv(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "api_key": "config-key", "mcpServers": []}`)
	t.Setenv(EnvAPIKey, "env-key")
	t.Setenv(EnvAPIKeyFile, writeKeyFile(t, "file-key\n"))

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	key, generated, source, keyErr := cfg.EnsureAPIKey()
	require.NoError(t, keyErr)
	assert.Equal(t, "file-key", key)
	assert.False(t, generated)
	assert.Equal(t, APIKeySourceEnvironmentFile, source)

	require.NoError(t, SaveConfig(cfg, path))
	assert.Equal(t, "config-key", readJSON(t, path)["api_key"], "the file's key never replaces the config's on save")
}

func TestEnsureAPIKey_UnusableFileNeverGeneratesAKey(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "api_key": "config-key", "mcpServers": []}`)
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	unsetEnv(t, EnvAPIKey)
	t.Setenv(EnvAPIKeyFile, filepath.Join(t.TempDir(), "missing"))

	key, generated, _, keyErr := cfg.EnsureAPIKey()
	require.Error(t, keyErr)
	assert.Empty(t, key)
	assert.False(t, generated)
	assert.Equal(t, "config-key", cfg.APIKey, "the config's key is untouched")

	// And with no key configured at all: still no generated key.
	cfg2 := &Config{}
	key, generated, _, keyErr = cfg2.EnsureAPIKey()
	require.Error(t, keyErr)
	assert.Empty(t, key)
	assert.False(t, generated)
	assert.Empty(t, cfg2.APIKey, "no key was generated")
}

func TestLoad_APIKeyFromFileIsAProcessOverrideNeverSaved(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)
	unsetEnv(t, EnvAPIKey)
	t.Setenv(EnvAPIKeyFile, writeKeyFile(t, "file-key\n"))

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "file-key", cfg.APIKey, "an empty api_key is filled from the file")
	assert.Contains(t, ProcessOverrideFields(), "api_key")

	require.NoError(t, SaveConfig(cfg, path))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "file-key", "the secret from the file must not be written into the config")
}

func TestLoad_UnusableKeyFileFailsTheLoad(t *testing.T) {
	t.Cleanup(ResetProcessOverrides)
	ResetProcessOverrides()
	path := writeOverrideTestFile(t, `{"listen": "127.0.0.1:8080", "mcpServers": []}`)
	unsetEnv(t, EnvAPIKey)
	t.Setenv(EnvAPIKeyFile, filepath.Join(t.TempDir(), "missing"))

	_, err := LoadFromFile(path)
	require.Error(t, err, "a missing key file must stop the load, not fall through to a generated key")
	assert.Contains(t, err.Error(), EnvAPIKeyFile)
}
