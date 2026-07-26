package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// mockPerServerDiagController drives GET /api/v1/servers/{id}/diagnostics with a
// server whose health.detail and diagnostic.cause echo raw URL query secrets.
type mockPerServerDiagController struct {
	baseController
	apiKey string
	reveal bool
}

func (m *mockPerServerDiagController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey}
}

func (m *mockPerServerDiagController) GetConfig() (*config.Config, error) {
	return &config.Config{APIKey: m.apiKey, RevealSecretHeaders: m.reveal}, nil
}

func (m *mockPerServerDiagController) GetAllServers() ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{
			"name":      "leaky",
			"connected": false,
			"status":    "Error",
			"health": &contracts.HealthStatus{
				Level:      "unhealthy",
				AdminState: "enabled",
				Summary:    "Connection error",
				Detail:     `connect error: apikey=DETAILSECRET rejected`,
			},
			"diagnostic": map[string]interface{}{
				"code":  "MCPX_HTTP_DNS_FAILED",
				"cause": `Post "https://api.example.com/mcp?access_token=CAUSESECRET": no such host`,
			},
			"error_code": "MCPX_HTTP_DNS_FAILED",
		},
	}, nil
}

// Issue #872: the per-server diagnostics endpoint must scrub URL secrets from
// health.detail and diagnostic.cause — parity with the /api/v1/servers list
// route — unless reveal_secret_headers is set.
func TestHandleGetServerDiagnostics_RedactsSecrets(t *testing.T) {
	logger := zap.NewNop().Sugar()
	const apiKey = "test-per-server-diag-key"

	ctrl := &mockPerServerDiagController{apiKey: apiKey}
	srv := NewServer(ctrl, logger, nil)

	req := httptest.NewRequest("GET", "/api/v1/servers/leaky/diagnostics?apikey="+apiKey, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	body := w.Body.String()
	assert.NotContains(t, body, "DETAILSECRET", "health.detail must be scrubbed")
	assert.NotContains(t, body, "CAUSESECRET", "diagnostic.cause must be scrubbed")
}

// With reveal_secret_headers=true the operator opted out, so the raw values pass.
func TestHandleGetServerDiagnostics_RevealKeepsRaw(t *testing.T) {
	logger := zap.NewNop().Sugar()
	const apiKey = "test-per-server-diag-key"

	ctrl := &mockPerServerDiagController{apiKey: apiKey, reveal: true}
	srv := NewServer(ctrl, logger, nil)

	req := httptest.NewRequest("GET", "/api/v1/servers/leaky/diagnostics?apikey="+apiKey, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// Sanity: the response actually carries the fields under test.
	var resp struct {
		Data struct {
			Health     *contracts.HealthStatus `json:"health"`
			Diagnostic map[string]interface{}  `json:"diagnostic"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Data.Health)
	assert.Contains(t, resp.Data.Health.Detail, "DETAILSECRET")
	assert.Contains(t, resp.Data.Diagnostic["cause"], "CAUSESECRET")
}
