package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAutoRescanOnChangeEnabled(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	t.Run("nil and unset default to enabled", func(t *testing.T) {
		var sec *SecurityConfig
		assert.True(t, sec.IsAutoRescanOnChangeEnabled())
		assert.True(t, (&SecurityConfig{}).IsAutoRescanOnChangeEnabled())
	})

	t.Run("explicit values", func(t *testing.T) {
		assert.False(t, (&SecurityConfig{AutoRescanOnChange: boolPtr(false)}).IsAutoRescanOnChangeEnabled())
		assert.True(t, (&SecurityConfig{AutoRescanOnChange: boolPtr(true)}).IsAutoRescanOnChangeEnabled())
	})

	t.Run("env override outranks the file value", func(t *testing.T) {
		t.Setenv(EnvAutoRescanOnChange, "false")
		assert.False(t, (&SecurityConfig{AutoRescanOnChange: boolPtr(true)}).IsAutoRescanOnChangeEnabled())
		t.Setenv(EnvAutoRescanOnChange, "0")
		assert.False(t, (&SecurityConfig{}).IsAutoRescanOnChangeEnabled())
		t.Setenv(EnvAutoRescanOnChange, "true")
		assert.True(t, (&SecurityConfig{AutoRescanOnChange: boolPtr(false)}).IsAutoRescanOnChangeEnabled())
		t.Setenv(EnvAutoRescanOnChange, "1")
		assert.True(t, (&SecurityConfig{AutoRescanOnChange: boolPtr(false)}).IsAutoRescanOnChangeEnabled())
	})

	t.Run("unrecognized env value is ignored", func(t *testing.T) {
		t.Setenv(EnvAutoRescanOnChange, "maybe")
		assert.False(t, (&SecurityConfig{AutoRescanOnChange: boolPtr(false)}).IsAutoRescanOnChangeEnabled())
		assert.True(t, (&SecurityConfig{}).IsAutoRescanOnChangeEnabled())
	})
}

// IsAutoChangeScanEnabled is the combined gate: the coarse
// auto_baseline_scan switch AND the finer auto_rescan_on_change switch.
func TestIsAutoChangeScanEnabled(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	var nilSec *SecurityConfig
	assert.True(t, nilSec.IsAutoChangeScanEnabled())
	assert.True(t, (&SecurityConfig{}).IsAutoChangeScanEnabled())
	assert.False(t, (&SecurityConfig{AutoBaselineScan: boolPtr(false)}).IsAutoChangeScanEnabled())
	assert.False(t, (&SecurityConfig{AutoRescanOnChange: boolPtr(false)}).IsAutoChangeScanEnabled())
	assert.True(t, (&SecurityConfig{AutoBaselineScan: boolPtr(true), AutoRescanOnChange: boolPtr(true)}).IsAutoChangeScanEnabled())

	t.Setenv(EnvAutoBaselineScan, "false")
	assert.False(t, (&SecurityConfig{AutoRescanOnChange: boolPtr(true)}).IsAutoChangeScanEnabled())
}

func TestApplyEnvOverrides_AutoRescanOnChange(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	t.Run("recognized value materializes the field", func(t *testing.T) {
		t.Setenv(EnvAutoRescanOnChange, "false")
		cfg := &Config{}
		applyTLSEnvOverrides(cfg)
		require.NotNil(t, cfg.Security)
		require.NotNil(t, cfg.Security.AutoRescanOnChange)
		assert.False(t, *cfg.Security.AutoRescanOnChange)
	})

	t.Run("unrecognized value does not touch the file value", func(t *testing.T) {
		t.Setenv(EnvAutoRescanOnChange, "yes")
		cfg := &Config{Security: &SecurityConfig{AutoRescanOnChange: boolPtr(true)}}
		applyTLSEnvOverrides(cfg)
		require.NotNil(t, cfg.Security.AutoRescanOnChange)
		assert.True(t, *cfg.Security.AutoRescanOnChange)
	})
}
