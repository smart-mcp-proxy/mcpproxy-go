package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/observability"
)

// SEC-07: /metrics was registered on the bare router, outside the /api/v1
// group that carries apiKeyAuthMiddleware, so anyone who could reach the
// listener could scrape tool-usage counters and API topology.
//
// These tests are built on a controller whose GetCurrentConfig() returns a
// real *config.Config with a non-empty APIKey. That is load-bearing:
// apiKeyAuthMiddleware deliberately lets requests through when the config is
// nil or not a *config.Config (the testing/bootstrap passthrough), so a test
// built on MockServerController would pass with or without the fix.

const metricsTestAPIKey = "metrics-admin-api-key"

// newMetricsTestServer builds a Server with the metrics exporter enabled and
// the health manager disabled (so the controller-backed health handlers stay
// authoritative), plus an admin API key that must actually be presented.
func newMetricsTestServer(t *testing.T) *Server {
	t.Helper()
	obsCfg := observability.Config{
		Metrics: observability.MetricsConfig{Enabled: true},
		Health:  observability.HealthConfig{Enabled: false},
		Tracing: observability.TracingConfig{Enabled: false},
	}
	mgr, err := observability.NewManager(zap.NewNop().Sugar(), &obsCfg)
	require.NoError(t, err)
	require.NotNil(t, mgr.Metrics(), "metrics exporter must be active for this test to mean anything")
	require.Nil(t, mgr.Health())

	return NewServer(&mockControllerWithKey{apiKey: metricsTestAPIKey}, zap.NewNop().Sugar(), mgr)
}

func metricsGet(t *testing.T, srv *Server, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestMetricsEndpoint_RequiresAPIKey(t *testing.T) {
	srv := newMetricsTestServer(t)

	t.Run("no credential is rejected", func(t *testing.T) {
		rec := metricsGet(t, srv, "/metrics", nil)
		assert.Equal(t, http.StatusUnauthorized, rec.Code,
			"unauthenticated GET /metrics must not expose the exporter; body=%s", rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "mcpproxy_uptime_seconds")
	})

	t.Run("wrong api key is rejected", func(t *testing.T) {
		rec := metricsGet(t, srv, "/metrics", map[string]string{"X-API-Key": "not-the-key"})
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "body=%s", rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "mcpproxy_uptime_seconds")
	})

	t.Run("correct api key is served", func(t *testing.T) {
		rec := metricsGet(t, srv, "/metrics", map[string]string{"X-API-Key": metricsTestAPIKey})
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Contains(t, rec.Body.String(), "mcpproxy_uptime_seconds")
	})

	t.Run("bearer admin key is served (prometheus authorization block)", func(t *testing.T) {
		rec := metricsGet(t, srv, "/metrics", map[string]string{"Authorization": "Bearer " + metricsTestAPIKey})
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Contains(t, rec.Body.String(), "mcpproxy_uptime_seconds")
	})
}

// TestMetricsEndpoint_RejectsScopedAgentToken pins the second half of SEC-07:
// apiKeyAuthMiddleware alone still admits agent tokens, and a scope-restricted
// agent must not be able to scrape fleet-wide tool/server topology out of the
// exporter. The token here is a real mcp_agt_ token run through the real
// handleAgentTokenAuth, not a hand-injected AuthContext.
func TestMetricsEndpoint_RejectsScopedAgentToken(t *testing.T) {
	obsCfg := observability.Config{
		Metrics: observability.MetricsConfig{Enabled: true},
		Health:  observability.HealthConfig{Enabled: false},
		Tracing: observability.TracingConfig{Enabled: false},
	}
	mgr, err := observability.NewManager(zap.NewNop().Sugar(), &obsCfg)
	require.NoError(t, err)
	require.NotNil(t, mgr.Metrics())

	tmpDir := t.TempDir()
	_, err = auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	agentToken := &auth.AgentToken{
		Name:           "scoped-scraper",
		TokenPrefix:    auth.TokenPrefix(rawToken),
		AllowedServers: []string{"alpha"},
		Permissions:    []string{auth.PermRead},
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		CreatedAt:      time.Now(),
	}
	store := &testTokenStore{
		validateFunc: func(token string, _ []byte) (*auth.AgentToken, error) {
			if token == rawToken {
				return agentToken, nil
			}
			return nil, fmt.Errorf("token not found")
		},
	}

	srv := NewServer(&mockControllerWithKey{apiKey: metricsTestAPIKey}, zap.NewNop().Sugar(), mgr)
	srv.SetTokenStore(store, tmpDir)

	rec := metricsGet(t, srv, "/metrics", map[string]string{"X-API-Key": rawToken})
	assert.Equal(t, http.StatusForbidden, rec.Code,
		"a scope-restricted agent token must not scrape the exporter; body=%s", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "mcpproxy_uptime_seconds")

	// The admin key still works on the same server.
	admin := metricsGet(t, srv, "/metrics", map[string]string{"X-API-Key": metricsTestAPIKey})
	require.Equal(t, http.StatusOK, admin.Code, "body=%s", admin.Body.String())
	assert.Contains(t, admin.Body.String(), "mcpproxy_uptime_seconds")
}

// TestHealthEndpoints_StayUnauthenticated guards the other half of the fix:
// liveness/readiness probes are legitimately unauthenticated and must NOT be
// swept behind the API key along with /metrics. All five aliases, not just
// the three under /healthz.
func TestHealthEndpoints_StayUnauthenticated(t *testing.T) {
	srv := newMetricsTestServer(t)

	for _, path := range []string{"/healthz", "/livez", "/health"} {
		t.Run("liveness "+path, func(t *testing.T) {
			rec := metricsGet(t, srv, path, nil)
			assert.Equal(t, http.StatusOK, rec.Code,
				"liveness probe %s must stay open; body=%s", path, rec.Body.String())
		})
	}

	// Readiness reflects the controller (200 or 503) but must never be 401.
	for _, path := range []string{"/readyz", "/ready"} {
		t.Run("readiness "+path, func(t *testing.T) {
			rec := metricsGet(t, srv, path, nil)
			assert.NotEqual(t, http.StatusUnauthorized, rec.Code,
				"readiness probe %s must stay open; body=%s", path, rec.Body.String())
			assert.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, rec.Code,
				"readiness probe %s should answer with controller readiness; body=%s", path, rec.Body.String())
		})
	}
}
