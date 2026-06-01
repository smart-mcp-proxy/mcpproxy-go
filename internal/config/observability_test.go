package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultObservabilityConfig(t *testing.T) {
	o := DefaultObservabilityConfig()
	require.NotNil(t, o)
	assert.Equal(t, 5*time.Second, o.UsageCacheTTL.Duration())
	assert.Equal(t, 30*time.Second, o.UsagePersistInterval.Duration())
}

func TestDefaultConfig_HasObservabilityDefaults(t *testing.T) {
	cfg := DefaultConfig()
	require.NotNil(t, cfg.Observability)
	assert.Equal(t, 5*time.Second, cfg.Observability.UsageCacheTTL.Duration())
	assert.Equal(t, 30*time.Second, cfg.Observability.UsagePersistInterval.Duration())
}

func TestValidate_FillsObservabilityDefaults(t *testing.T) {
	// A config loaded without an observability block gets defaults applied
	// on Validate (hot-reload path re-runs Validate).
	cfg := DefaultConfig()
	cfg.Observability = nil
	require.NoError(t, cfg.Validate())
	require.NotNil(t, cfg.Observability)
	assert.Equal(t, 5*time.Second, cfg.Observability.UsageCacheTTL.Duration())
	assert.Equal(t, 30*time.Second, cfg.Observability.UsagePersistInterval.Duration())

	// Zero/negative interval fields are repaired to defaults.
	cfg.Observability = &ObservabilityConfig{}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, 5*time.Second, cfg.Observability.UsageCacheTTL.Duration())
	assert.Equal(t, 30*time.Second, cfg.Observability.UsagePersistInterval.Duration())
}

func TestObservabilityConfig_PreservesUserValues(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Observability = &ObservabilityConfig{
		UsageCacheTTL:        Duration(2 * time.Second),
		UsagePersistInterval: Duration(60 * time.Second),
	}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, 2*time.Second, cfg.Observability.UsageCacheTTL.Duration())
	assert.Equal(t, 60*time.Second, cfg.Observability.UsagePersistInterval.Duration())
}
