package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/observability"
)

// MCP-32: exporters are config-gated and OFF by default.

func TestBuildObservabilityConfig_OffByDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Observability = nil // simulate a config with no observability block
	out := buildObservabilityConfig(cfg)
	assert.False(t, out.Metrics.Enabled, "metrics must be off by default")
	assert.False(t, out.Tracing.Enabled, "tracing must be off by default")
}

func TestBuildObservabilityConfig_DefaultConfigOff(t *testing.T) {
	// Even the fully-defaulted config keeps exporters off (DefaultObservabilityConfig).
	out := buildObservabilityConfig(config.DefaultConfig())
	assert.False(t, out.Metrics.Enabled)
	assert.False(t, out.Tracing.Enabled)
}

// MCP-32 regression: the observability health manager is never wired (its
// readiness is vacuous), so /readyz stays controller-backed even with metrics on.
func TestBuildObservabilityConfig_HealthAlwaysDisabled(t *testing.T) {
	off := buildObservabilityConfig(config.DefaultConfig())
	assert.False(t, off.Health.Enabled, "health manager must stay off (metrics off)")

	cfg := config.DefaultConfig()
	cfg.Observability.Metrics = &config.MetricsExporterConfig{Enabled: true}
	on := buildObservabilityConfig(cfg)
	assert.True(t, on.Metrics.Enabled)
	assert.False(t, on.Health.Enabled, "health manager must stay off even with metrics on")
}

func TestBuildObservabilityConfig_EnablesAndMapsTracing(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Observability.Metrics = &config.MetricsExporterConfig{Enabled: true}
	cfg.Observability.Tracing = &config.TracingExporterConfig{
		Enabled:    true,
		Protocol:   "grpc",
		Endpoint:   "collector:4317",
		SampleRate: 0.25,
	}
	out := buildObservabilityConfig(cfg)
	assert.True(t, out.Metrics.Enabled)
	assert.True(t, out.Tracing.Enabled)
	assert.Equal(t, "grpc", out.Tracing.Protocol)
	assert.Equal(t, "collector:4317", out.Tracing.OTLPEndpoint)
	assert.InDelta(t, 0.25, out.Tracing.SampleRate, 1e-9)
}

// When metrics are enabled the manager serves a scrapeable /metrics handler.
func TestObservabilityManager_ServesMetricsWhenEnabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Observability.Metrics = &config.MetricsExporterConfig{Enabled: true}
	obsCfg := buildObservabilityConfig(cfg)

	mgr, err := observability.NewManager(zap.NewNop().Sugar(), &obsCfg)
	require.NoError(t, err)
	require.NotNil(t, mgr.Metrics(), "metrics manager should exist when enabled")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", http.NoBody)
	mgr.Metrics().Handler().ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	// Gauges are always emitted (counter Vecs only appear after first observation).
	assert.Contains(t, rec.Body.String(), "mcpproxy_uptime_seconds")
	assert.Contains(t, rec.Body.String(), "mcpproxy_servers_total")
}

func TestObservabilityManager_NoMetricsWhenDisabled(t *testing.T) {
	obsCfg := buildObservabilityConfig(config.DefaultConfig())
	mgr, err := observability.NewManager(zap.NewNop().Sugar(), &obsCfg)
	require.NoError(t, err)
	assert.Nil(t, mgr.Metrics(), "metrics manager must be nil when disabled")
}

// finishToolCall records tool-call latency/outcome metrics (MCP-32).
func TestFinishToolCall_RecordsMetric(t *testing.T) {
	obsCfg := buildObservabilityConfig(config.DefaultConfig())
	obsCfg.Metrics.Enabled = true
	mgr, err := observability.NewManager(zap.NewNop().Sugar(), &obsCfg)
	require.NoError(t, err)

	p := &MCPProxyServer{}
	p.SetObservability(mgr)

	p.finishToolCall(nil, "github", "create_issue", 5_000_000, nil) // 5ms success
	p.finishToolCall(nil, "github", "create_issue", 1_000_000, assert.AnError)

	reg := mgr.Metrics().Registry()
	assert.InDelta(t, 1.0, metricValue(t, reg, "mcpproxy_tool_calls_total",
		map[string]string{"server": "github", "tool": "create_issue", "status": "success"}), 1e-9)
	assert.InDelta(t, 1.0, metricValue(t, reg, "mcpproxy_tool_calls_total",
		map[string]string{"server": "github", "tool": "create_issue", "status": "error"}), 1e-9)
}

// With observability disabled, the inline hooks are safe no-ops.
func TestFinishToolCall_NilObservabilityNoPanic(t *testing.T) {
	p := &MCPProxyServer{}
	_, span := p.startToolCallSpan(context.Background(), "s", "t", "")
	assert.Nil(t, span)
	p.finishToolCall(span, "s", "t", 1, nil) // must not panic
}
