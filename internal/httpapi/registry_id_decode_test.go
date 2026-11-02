package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// registryIDRecorder records the registry id each registry handler forwards.
type registryIDRecorder struct {
	*MockServerController
	got string
}

func (c *registryIDRecorder) SearchRegistryServers(id, _, _ string, _ int) ([]interface{}, *contracts.RegistryCacheInfo, error) {
	c.got = id
	return []interface{}{}, nil, nil
}

func (c *registryIDRecorder) RefreshRegistryCache(id string) (int, error) {
	c.got = id
	return 0, nil
}

func (c *registryIDRecorder) RemoveRegistrySourceRef(id string) (*config.RegistryEntry, *contracts.RegistryAddError, error) {
	c.got = id
	return &config.RegistryEntry{ID: id}, nil, nil
}

func (c *registryIDRecorder) EditRegistrySourceRef(id, _, _, _ string) (*config.RegistryEntry, *contracts.RegistryAddError, error) {
	c.got = id
	return &config.RegistryEntry{ID: id}, nil, nil
}

// A custom registry id is user-chosen (add-source --id) and may contain
// characters the CLI and Web UI percent-encode. chi routes on RawPath, so the
// {id} param arrives encoded and must be decoded before the exact-match lookup,
// as handleAddFromRegistry already does.
func TestRegistryRoutes_DecodeIDParam(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/registries/acme%3Ainternal/servers", ""},
		{http.MethodPost, "/api/v1/registries/acme%3Ainternal/refresh", ""},
		{http.MethodDelete, "/api/v1/registries/acme%3Ainternal", ""},
		{http.MethodPut, "/api/v1/registries/acme%3Ainternal", `{"name":"Acme"}`},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			ctrl := &registryIDRecorder{MockServerController: &MockServerController{}}
			srv := NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)

			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
			req.Header.Set("X-API-Key", mockControllerAPIKey)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			assert.Equal(t, "acme:internal", ctrl.got)
		})
	}
}
