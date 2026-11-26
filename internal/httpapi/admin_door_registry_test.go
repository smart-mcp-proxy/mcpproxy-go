//go:build !server

// Personal-edition registrations only: the -tags server router and command
// tree are enumerated from Spec 133 PR-1b on (plan.md).

package httpapi

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/observability"
)

// Spec 133 FR-022: every route and method of the production personal REST
// router — GET included, no default exemption — is classified in the door
// registry, and every REST registry row still names a registered route.
func TestAdminDoorRegistry_CoversEveryRESTRoute(t *testing.T) {
	srv, _ := scopeRound10Server(t, &mockSecurityController{})
	set := map[string]bool{}
	// The second router has observability metrics on: /metrics is mounted
	// only then, and conditional routes must be classified too.
	for _, router := range []chi.Routes{srv.Router(), newMetricsTestServer(t).Router(), newHealthTestServer(t).Router()} {
		err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			set["REST "+method+" "+route] = true
			return nil
		})
		require.NoError(t, err)
	}
	var doors []string
	for d := range set {
		doors = append(doors, d)
	}
	require.Greater(t, len(doors), 100, "chi.Walk found too few routes: the router changed shape")
	missing, stale := audit.DiffDoors("REST ", doors)
	require.Empty(t, missing, "REST doors with no registry row (internal/audit/admindoors.go): map them to an action or exempt them with a reason")
	require.Empty(t, stale, "registry rows whose REST door no longer exists")
}

// newHealthTestServer builds a Server with every observability branch on
// (metrics and the health manager), so routes mounted only then are walked.
func newHealthTestServer(t *testing.T) *Server {
	t.Helper()
	obsCfg := observability.Config{
		Metrics: observability.MetricsConfig{Enabled: true},
		Health:  observability.HealthConfig{Enabled: true},
		Tracing: observability.TracingConfig{Enabled: false},
	}
	mgr, err := observability.NewManager(zap.NewNop().Sugar(), &obsCfg)
	require.NoError(t, err)
	require.NotNil(t, mgr.Health())
	return NewServer(&mockControllerWithKey{apiKey: metricsTestAPIKey}, zap.NewNop().Sugar(), mgr)
}
