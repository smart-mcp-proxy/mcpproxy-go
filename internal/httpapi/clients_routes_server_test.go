//go:build server

package httpapi

import (
	"net/http"
	"net/http/httptest"
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
