package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectiveSecretFileDirRoots(t *testing.T) {
	t.Run("absent uses defaults on linux", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("linux-only defaults")
		}
		_, roots, disabled := EffectiveSecretFileDirRoots(&Config{}, true)
		assert.False(t, disabled)
		assert.Contains(t, roots, "/run/secrets")
	})

	t.Run("empty disables", func(t *testing.T) {
		empty := SecretFileDirs{}
		_, _, disabled := EffectiveSecretFileDirRoots(&Config{SecretFileDirs: &empty}, true)
		assert.True(t, disabled)
	})

	t.Run("wildcard", func(t *testing.T) {
		w := SecretFileDirs{"*"}
		wildcard, _, disabled := EffectiveSecretFileDirRoots(&Config{SecretFileDirs: &w}, true)
		assert.True(t, wildcard)
		assert.False(t, disabled)
	})

	t.Run("credentials directory env", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(envCredentialsDir, dir)
		_, roots, _ := EffectiveSecretFileDirRoots(&Config{}, false)
		assert.Contains(t, roots, dir)
	})
}

func TestValidateSecretFileDirs(t *testing.T) {
	rel := SecretFileDirs{"./secrets"}
	errs := validateSecretFileDirs(&Config{SecretFileDirs: &rel})
	require.Len(t, errs, 1)

	mixed := SecretFileDirs{"/run/secrets", "*"}
	errs = validateSecretFileDirs(&Config{SecretFileDirs: &mixed})
	require.NotEmpty(t, errs)
}

func TestValidateAdminAPIKeyFilePath(t *testing.T) {
	data := t.TempDir()
	cfgPath := filepath.Join(data, ConfigFileName)
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{}`), 0o600))
	cfg := &Config{DataDir: data}

	require.Error(t, validateAdminAPIKeyFilePath(cfg, cfgPath, cfgPath))
	assert.ErrorIs(t, validateAdminAPIKeyFilePath(cfg, cfgPath, cfgPath), ErrAdminAPIKeyFilePath)

	db := filepath.Join(data, "config.db")
	require.NoError(t, os.WriteFile(db, []byte("x"), 0o600))
	assert.ErrorIs(t, validateAdminAPIKeyFilePath(cfg, cfgPath, db), ErrAdminAPIKeyFilePath)

	secrets := filepath.Join(data, "secrets")
	require.NoError(t, os.MkdirAll(secrets, 0o700))
	key := filepath.Join(secrets, "api_key")
	require.NoError(t, validateAdminAPIKeyFilePath(cfg, cfgPath, key))
}

func TestSecretFileDirsNullRejected(t *testing.T) {
	var s SecretFileDirs
	err := s.UnmarshalJSON([]byte("null"))
	require.Error(t, err)
}
