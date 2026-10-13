package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// capturingRegistryController records the exact registryID/serverID the HTTP
// handler forwards to the controller, so the test can assert they were
// percent-decoded before lookup (MCP-1056).
type capturingRegistryController struct {
	*MockServerController
	gotRegistryID string
	gotServerID   string
}

func (c *capturingRegistryController) AddServerFromRegistryRef(_ context.Context, registryID, serverID, _ string, _ map[string]string, _ *bool) (*config.ServerConfig, *contracts.RegistryAddError, error) {
	c.gotRegistryID = registryID
	c.gotServerID = serverID
	return &config.ServerConfig{Name: "markitdown", Protocol: "stdio", Enabled: true}, nil, nil
}

// TestAddFromRegistry_SlashServerIDUnescaped reproduces MCP-1056: official v0.1
// registry ids are namespace/name. chi routes on RawPath, so the {serverId}
// path param arrives percent-encoded (microsoft%2Fmarkitdown). The handler must
// url.PathUnescape it before lookup, otherwise the exact-match lookup fails with
// server_not_found and the server is un-addable on CLI/REST/Web UI.
func TestAddFromRegistry_SlashServerIDUnescaped(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	controller := &capturingRegistryController{MockServerController: &MockServerController{}}
	server := NewServer(controller, logger, nil)

	// microsoft/markitdown, percent-encoded as a single path segment.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/registries/github-mcp/servers/microsoft%2Fmarkitdown/add", http.NoBody)
	req.Header.Set("X-API-Key", mockControllerAPIKey)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "add should succeed; body=%s", w.Body.String())

	var resp contracts.APIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success, "response should indicate success")

	assert.Equal(t, "microsoft/markitdown", controller.gotServerID, "serverId must be percent-decoded before registry lookup")
	assert.Equal(t, "github-mcp", controller.gotRegistryID, "registry id must be percent-decoded before lookup")
}

// TestAddFromRegistry_NilConfigIsAnError pins the nil-tolerance of the success
// branch: a controller that returns no config and no error yields a JSON 500,
// never a recovered nil-pointer panic (which corrupts the heap on
// windows/amd64 under Go 1.26 — golang/go#81238 — and took the whole test
// binary down with it).
func TestAddFromRegistry_NilConfigIsAnError(t *testing.T) {
	ctrl := &adminConfigController{ServerController: &MockServerController{}, apiKey: "admin-secret"}
	srv := NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/registries/reg1/servers/srv1/add", nil)
	req.Header.Set("X-API-Key", "admin-secret")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "no server configuration")
}
