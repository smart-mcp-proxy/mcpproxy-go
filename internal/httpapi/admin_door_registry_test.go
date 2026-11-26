//go:build !server

// Personal-edition registrations only: the -tags server router and command
// tree are enumerated from Spec 133 PR-1b on (plan.md).

package httpapi

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

// Spec 133 FR-022: every route and method of the production personal REST
// router — GET included, no default exemption — is classified in the door
// registry, and every REST registry row still names a registered route.
func TestAdminDoorRegistry_CoversEveryRESTRoute(t *testing.T) {
	srv, _ := scopeRound10Server(t, &mockSecurityController{})
	var doors []string
	err := chi.Walk(srv.Router(), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		doors = append(doors, "REST "+method+" "+route)
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, len(doors), 100, "chi.Walk found too few routes: the router changed shape")
	missing, stale := audit.DiffDoors("REST ", doors)
	require.Empty(t, missing, "REST doors with no registry row (internal/audit/admindoors.go): map them to an action or exempt them with a reason")
	require.Empty(t, stale, "registry rows whose REST door no longer exists")
}
