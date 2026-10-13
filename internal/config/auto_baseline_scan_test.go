package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAutoBaselineScanEnabled(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	t.Run("nil security block defaults to enabled", func(t *testing.T) {
		var sec *SecurityConfig
		assert.True(t, sec.IsAutoBaselineScanEnabled())
	})

	t.Run("unset field defaults to enabled", func(t *testing.T) {
		assert.True(t, (&SecurityConfig{}).IsAutoBaselineScanEnabled())
	})

	t.Run("explicit false disables", func(t *testing.T) {
		assert.False(t, (&SecurityConfig{AutoBaselineScan: boolPtr(false)}).IsAutoBaselineScanEnabled())
	})

	t.Run("explicit true enables", func(t *testing.T) {
		assert.True(t, (&SecurityConfig{AutoBaselineScan: boolPtr(true)}).IsAutoBaselineScanEnabled())
	})

	t.Run("env override outranks the file value", func(t *testing.T) {
		t.Setenv(EnvAutoBaselineScan, "false")
		assert.False(t, (&SecurityConfig{AutoBaselineScan: boolPtr(true)}).IsAutoBaselineScanEnabled())

		t.Setenv(EnvAutoBaselineScan, "0")
		assert.False(t, (&SecurityConfig{}).IsAutoBaselineScanEnabled())

		t.Setenv(EnvAutoBaselineScan, "true")
		assert.True(t, (&SecurityConfig{AutoBaselineScan: boolPtr(false)}).IsAutoBaselineScanEnabled())

		t.Setenv(EnvAutoBaselineScan, "1")
		assert.True(t, (&SecurityConfig{AutoBaselineScan: boolPtr(false)}).IsAutoBaselineScanEnabled())
	})

	t.Run("unrecognized env value is ignored", func(t *testing.T) {
		t.Setenv(EnvAutoBaselineScan, "maybe")
		assert.False(t, (&SecurityConfig{AutoBaselineScan: boolPtr(false)}).IsAutoBaselineScanEnabled())
		assert.True(t, (&SecurityConfig{}).IsAutoBaselineScanEnabled())
	})
}

// The loader's env pass must use the SAME vocabulary as the accessor. A bare
// non-empty check there would materialize AutoBaselineScan=false for a typo like
// "yes", and because the accessor then ignores the unrecognized env value it
// would read that overwritten false — silently disabling automatic scanning for
// a config that had explicitly enabled it.
func TestAutoBaselineScanEnvOverride_LoaderVocabulary(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	t.Run("unrecognized value leaves the configured field alone", func(t *testing.T) {
		t.Setenv(EnvAutoBaselineScan, "yes")
		cfg := &Config{Security: &SecurityConfig{AutoBaselineScan: boolPtr(true)}}
		applyTLSEnvOverrides(cfg)
		require.NotNil(t, cfg.Security.AutoBaselineScan)
		assert.True(t, *cfg.Security.AutoBaselineScan)
		assert.True(t, cfg.Security.IsAutoBaselineScanEnabled())
	})

	t.Run("unrecognized value does not materialize a security block", func(t *testing.T) {
		t.Setenv(EnvAutoBaselineScan, "maybe")
		cfg := &Config{}
		applyTLSEnvOverrides(cfg)
		assert.Nil(t, cfg.Security)
	})

	t.Run("recognized values still override and materialize the block", func(t *testing.T) {
		t.Setenv(EnvAutoBaselineScan, "false")
		cfg := &Config{}
		applyTLSEnvOverrides(cfg)
		require.NotNil(t, cfg.Security)
		require.NotNil(t, cfg.Security.AutoBaselineScan)
		assert.False(t, *cfg.Security.AutoBaselineScan)

		t.Setenv(EnvAutoBaselineScan, "1")
		cfg = &Config{Security: &SecurityConfig{AutoBaselineScan: boolPtr(false)}}
		applyTLSEnvOverrides(cfg)
		require.NotNil(t, cfg.Security.AutoBaselineScan)
		assert.True(t, *cfg.Security.AutoBaselineScan)
	})
}
