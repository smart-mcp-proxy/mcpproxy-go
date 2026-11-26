package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #2031 item 7: pins for the nil cases around the OAuth key and the
// process-override registry. process_overrides.go itself carries no OAuth key
// override; the reachable nil cases are a nil *Config reaching
// ResolveOAuthTokenKey and a nil value written to a pointer field on a config
// without the nested block.

func TestResolveOAuthTokenKey_NilConfig(t *testing.T) {
	clearOAuthKeyEnv(t)
	key, src, err := ResolveOAuthTokenKey(nil)
	require.NoError(t, err)
	assert.Nil(t, key, "no source selected: encryption off")
	assert.Equal(t, KeySource{}, src)

	k := newTestOAuthKey(t)
	t.Setenv(EnvOAuthTokenKey, k)
	key, _, err = ResolveOAuthTokenKey(nil)
	require.NoError(t, err)
	require.NotNil(t, key, "env key must resolve without a config")
	assert.Equal(t, kidOf(t, k), key.ID())
}

// The env key file source is never gated by secret_file_dirs: a key file
// outside every allowed directory still resolves, and an empty allow-list
// changes nothing.
func TestResolveOAuthTokenKey_NotGatedBySecretFileDirs(t *testing.T) {
	clearOAuthKeyEnv(t)
	k := newTestOAuthKey(t)
	f := writeOAuthKeyFile(t, t.TempDir(), "key", k+"\n")
	t.Setenv(EnvOAuthTokenKeyFile, f)
	empty, other := SecretFileDirs{}, SecretFileDirs{t.TempDir()}
	for _, dirs := range []*SecretFileDirs{nil, &empty, &other} {
		key, _, err := ResolveOAuthTokenKey(&Config{SecretFileDirs: dirs})
		require.NoError(t, err, "dirs=%v", dirs != nil)
		require.NotNil(t, key)
		assert.Equal(t, kidOf(t, k), key.ID())
	}
}

func TestOverrideForProcess_NilValueOnMissingBlock(t *testing.T) {
	ResetProcessOverrides()
	t.Cleanup(ResetProcessOverrides)
	cfg := &Config{}
	OverrideForProcess(cfg, FieldAutoBaselineScan, OverrideSourceEnv, nil)
	assert.Contains(t, ProcessOverrideFields(), FieldAutoBaselineScan.Name, "the nil override is registered, so the restore path runs")
	assert.Nil(t, cfg.Security, "nil value must not materialise an empty security block")
	assert.NotPanics(t, func() { _ = PersistableConfig(cfg, "") })
	assert.Nil(t, PersistableConfig(cfg, "").Security)
	OverrideForProcess((*Config)(nil), FieldAutoBaselineScan, OverrideSourceEnv, nil)
}
