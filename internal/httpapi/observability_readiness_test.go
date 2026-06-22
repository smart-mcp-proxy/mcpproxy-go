package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/observability"
)

// notReadyController reports IsReady()==false so we can distinguish the
// controller-backed readiness handler (503) from the observability health
// manager's vacuous always-ready handler (200).
type notReadyController struct{ MockServerController }

func (m *notReadyController) IsReady() bool { return false }

// MCP-32 regression: enabling the metrics exporter must NOT take over /readyz.
// When the observability health manager is not active, /readyz must keep
// reflecting controller readiness (a config-gated feature is a no-op for
// readiness). Previously passing a non-nil observability manager skipped the
// controller health block entirely.
func TestReadyz_StaysControllerBackedWhenObservabilityHasNoHealth(t *testing.T) {
	obsCfg := observability.Config{
		Metrics: observability.MetricsConfig{Enabled: true},
		Health:  observability.HealthConfig{Enabled: false},
		Tracing: observability.TracingConfig{Enabled: false},
	}
	mgr, err := observability.NewManager(zap.NewNop().Sugar(), &obsCfg)
	require.NoError(t, err)
	require.NotNil(t, mgr.Metrics())
	require.Nil(t, mgr.Health())

	srv := NewServer(&notReadyController{}, zap.NewNop().Sugar(), mgr)

	// /readyz reflects the controller (not ready -> 503), not a vacuous 200.
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), `"ready":false`)

	// Liveness aliases remain registered too.
	for _, path := range []string{"/healthz", "/livez", "/health"} {
		r := httptest.NewRecorder()
		srv.router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		assert.Equalf(t, http.StatusOK, r.Code, "liveness endpoint %s should be registered", path)
	}

	// And the metrics exporter is still served.
	rm := httptest.NewRecorder()
	srv.router.ServeHTTP(rm, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	assert.Equal(t, http.StatusOK, rm.Code)
	assert.Contains(t, rm.Body.String(), "mcpproxy_uptime_seconds")
}
