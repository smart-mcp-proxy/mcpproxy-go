package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
