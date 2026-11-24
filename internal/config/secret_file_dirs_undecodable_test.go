package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An unrelated save, from a config that carries no secret_file_dirs, must not
// write the file back without a key the file holds but could not be decoded.
func TestPersistableConfig_RefusesWhenUndecodableFileHoldsSecretFileDirs(t *testing.T) {
	ResetProcessOverrides()
	t.Cleanup(ResetProcessOverrides)
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFileName)
	// "listen" has the wrong type, so the file cannot be decoded, yet it
	// plainly holds secret_file_dirs: [] (file references off).
	require.NoError(t, os.WriteFile(path, []byte(`{"secret_file_dirs": [], "listen": 5}`), 0o600))

	_, err := persistableConfigWithEdits(&Config{DataDir: dir, Listen: "127.0.0.1:8080"}, nil, path)
	assert.ErrorIs(t, err, ErrSecretFileDirsUnverified)
}

func TestPersistableConfig_UndecodableFileWithoutTheKeyStillSaves(t *testing.T) {
	ResetProcessOverrides()
	t.Cleanup(ResetProcessOverrides)
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFileName)
	for name, body := range map[string]string{
		"no key, bad type": `{"listen": 5}`,
		"not json at all":  `{{{`,
	} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600), name)
		_, err := persistableConfigWithEdits(&Config{DataDir: dir, Listen: "127.0.0.1:8080"}, nil, path)
		assert.NoError(t, err, name+": a repair save must stay possible")
	}
}

// runningAPIKeyFromReference reads the override registry with a type
// assertion on a map value. An absent entry yields a nil interface; the
// comma-ok assertion must report "no" rather than panic (#2031).
func TestRunningAPIKeyFromReference_NoEntryOrLiteralKey(t *testing.T) {
	ResetProcessOverrides()
	t.Cleanup(ResetProcessOverrides)

	v, ok := runningAPIKeyFromReference()
	assert.False(t, ok)
	assert.Empty(t, v)

	cfg := &Config{APIKey: "literal-key"}
	OverrideForProcess(cfg, FieldAPIKey, OverrideSourceEnv, "env-key")
	v, ok = runningAPIKeyFromReference()
	assert.False(t, ok, "a literal file value is not a reference")
	assert.Empty(t, v)
}
