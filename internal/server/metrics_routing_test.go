package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/observability"
)

// MCP-3135 regression: the /metrics handler is registered on the httpapi chi
// router, but the OUTER http.ServeMux in startCustomHTTPServer must forward
// /metrics to it. Before the fix the outer mux only forwarded /api/, /events,
// and the health endpoints, so GET /metrics returned 404 even with metrics
// enabled. These tests exercise the real outer mux via registerHTTPHandlers.

// sentinel stands in for the httpapi chi handler: it serves a scrapeable body
// for any request that the outer mux actually forwards to it.
func metricsSentinel() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mcpproxy_uptime_seconds 1\n"))
	})
}

func TestRegisterHTTPHandlers_MetricsRoutedWhenEnabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Observability.Metrics = &config.MetricsExporterConfig{Enabled: true}
	obsCfg := buildObservabilityConfig(cfg)
	mgr, err := observability.NewManager(zap.NewNop().Sugar(), &obsCfg)
	require.NoError(t, err)
	require.NotNil(t, mgr.Metrics(), "metrics manager should exist when enabled")

	s := &Server{logger: zap.NewNop(), observability: mgr}
	mux := http.NewServeMux()
	s.registerHTTPHandlers(mux, metricsSentinel())

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))

	// Before the fix this was 404 — the outer mux never forwarded /metrics.
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "mcpproxy_uptime_seconds")
}

func TestRegisterHTTPHandlers_MetricsNotRoutedWhenDisabled(t *testing.T) {
	// observability nil == metrics disabled (default). /metrics must stay 404.
	s := &Server{logger: zap.NewNop(), observability: nil}
	mux := http.NewServeMux()
	s.registerHTTPHandlers(mux, metricsSentinel())

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))

	assert.Equal(t, http.StatusNotFound, rec.Code, "/metrics must not be routed when metrics are disabled")
}

// The fix must not regress the routes the outer mux already forwarded.
func TestRegisterHTTPHandlers_ForwardsAPIAndHealth(t *testing.T) {
	s := &Server{logger: zap.NewNop(), observability: nil}
	mux := http.NewServeMux()
	s.registerHTTPHandlers(mux, metricsSentinel())

	for _, path := range []string{"/api/v1/status", "/events", "/healthz", "/readyz", "/livez", "/ready", "/health"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		assert.Equal(t, http.StatusOK, rec.Code, "outer mux should forward %s", path)
	}
}
