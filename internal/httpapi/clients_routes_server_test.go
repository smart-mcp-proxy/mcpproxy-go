//go:build server

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type serverEditionClientsController struct{ baseController }

func (c *serverEditionClientsController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: "clients-admin-key"}
}

// The Clients inventory is local-machine state. It deliberately has no
// server-edition route, even for an administrator.
func TestClientsRoutes_ServerEditionNotRegistered(t *testing.T) {
	srv := NewServer(&serverEditionClientsController{}, zap.NewNop().Sugar(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	req.Header.Set("X-API-Key", "clients-admin-key")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

// Client bindings are local-machine credential state too: no server-edition
// route exists for PUT /clients/{client}/binding, even for an administrator.
func TestClientBindingRoute_ServerEditionNotRegistered(t *testing.T) {
	srv := NewServer(&serverEditionClientsController{}, zap.NewNop().Sugar(), nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/clients/cursor/binding", strings.NewReader(`{"profile":""}`))
	req.Header.Set("X-API-Key", "clients-admin-key")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

// Spec 108-f: every route the personal edition added for client credentials is
// absent here; `client=` parameters on the routes that DO exist in both editions
// answer 404 "client not found".
func TestClientsRoutes_ServerEditionHasNoneOfTheNewRoutes(t *testing.T) {
	srv := NewServer(&serverEditionClientsController{}, zap.NewNop().Sugar(), nil)
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/clients", `{"id":"x"}`},
		{http.MethodPost, "/api/v1/clients/bulk-assign", `{"from_profile":"","to_profile":""}`},
		{http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", `{}`},
		{http.MethodPost, "/api/v1/clients/cursor/rotate", `{}`},
		{http.MethodPost, "/api/v1/clients/cursor/rotate/finalize", `{}`},
		{http.MethodDelete, "/api/v1/clients/cursor", ``},
	} {
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
		req.Header.Set("X-API-Key", "clients-admin-key")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code, "%s %s: %s", rt.method, rt.path, rec.Body.String())
	}
}

func TestProfilesRoutes_ServerEditionClientParamsAre404(t *testing.T) {
	srv := NewServer(&serverEditionClientsController{}, zap.NewNop().Sugar(), nil)
	for _, path := range []string{
		"/api/v1/access/explain?client=cursor&tool=github:list_issues",
		"/api/v1/profiles/any/effective-tools?client=cursor",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-API-Key", "clients-admin-key")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code, "%s: %s", path, rec.Body.String())
		require.Contains(t, rec.Body.String(), "client not found")
	}
}
