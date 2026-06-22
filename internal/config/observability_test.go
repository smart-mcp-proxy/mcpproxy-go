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

// MCP-32: Prometheus + OTLP exporters are config-gated and OFF by default.

func TestDefaultObservabilityConfig_ExportersOffByDefault(t *testing.T) {
	o := DefaultObservabilityConfig()
	require.NotNil(t, o.Metrics, "metrics sub-config should be present")
	assert.False(t, o.Metrics.Enabled, "Prometheus /metrics must be disabled by default")

	require.NotNil(t, o.Tracing, "tracing sub-config should be present")
	assert.False(t, o.Tracing.Enabled, "OTLP tracing must be disabled by default")
	// Sane transport defaults are pre-filled so enabling is a one-line change.
	assert.Equal(t, "http", o.Tracing.Protocol)
	assert.NotEmpty(t, o.Tracing.Endpoint)
	assert.InDelta(t, 0.1, o.Tracing.SampleRate, 1e-9)
}

func TestValidate_FillsExporterSubConfigs(t *testing.T) {
	// A config whose observability block omits the exporter sub-objects gets
	// them filled (disabled) on Validate, so downstream wiring never nil-panics.
	cfg := DefaultConfig()
	cfg.Observability = &ObservabilityConfig{
		UsageCacheTTL:        Duration(5 * time.Second),
		UsagePersistInterval: Duration(30 * time.Second),
	}
	require.NoError(t, cfg.Validate())
	require.NotNil(t, cfg.Observability.Metrics)
	require.NotNil(t, cfg.Observability.Tracing)
	assert.False(t, cfg.Observability.Metrics.Enabled)
	assert.False(t, cfg.Observability.Tracing.Enabled)
}

func TestValidate_RepairsTracingProtocolAndSampleRate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Observability.Tracing = &TracingExporterConfig{
		Enabled:    true,
		Protocol:   "carrier-pigeon", // unsupported -> repaired to http
		SampleRate: 5.0,              // out of [0,1] -> clamped to default
	}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, "http", cfg.Observability.Tracing.Protocol)
	assert.InDelta(t, 0.1, cfg.Observability.Tracing.SampleRate, 1e-9)
	// A missing endpoint is filled so the exporter can construct.
	assert.NotEmpty(t, cfg.Observability.Tracing.Endpoint)
}

func TestValidate_PreservesValidTracingExporter(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Observability.Metrics = &MetricsExporterConfig{Enabled: true}
	cfg.Observability.Tracing = &TracingExporterConfig{
		Enabled:    true,
		Protocol:   "grpc",
		Endpoint:   "otel-collector:4317",
		SampleRate: 0.5,
	}
	require.NoError(t, cfg.Validate())
	assert.True(t, cfg.Observability.Metrics.Enabled)
	assert.Equal(t, "grpc", cfg.Observability.Tracing.Protocol)
	assert.Equal(t, "otel-collector:4317", cfg.Observability.Tracing.Endpoint)
	assert.InDelta(t, 0.5, cfg.Observability.Tracing.SampleRate, 1e-9)
}
