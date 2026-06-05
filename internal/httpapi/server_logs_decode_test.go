package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// capturingLogsController records the serverID the logs handler forwards, so the
// test can assert it was percent-decoded before lookup.
type capturingLogsController struct {
	*MockServerController
	gotServerID string
}

func (c *capturingLogsController) GetServerLogs(serverID string, _ int) ([]contracts.LogEntry, error) {
	c.gotServerID = serverID
	return []contracts.LogEntry{}, nil
}

// TestGetServerLogs_SlashServerIDUnescaped reproduces the daemon-backed half of
// MCP-1111 / #598: official-registry server ids are namespace/name and chi routes
// on RawPath, so the {id} param arrives percent-encoded
// (io.github.evidai%2Fpolymarket-guard). handleGetServerLogs must url.PathUnescape
// it before lookup, otherwise `mcpproxy upstream logs io.github.evidai/polymarket-guard`
// targets the literal encoded name and never finds the server.
func TestGetServerLogs_SlashServerIDUnescaped(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	controller := &capturingLogsController{MockServerController: &MockServerController{}}
	server := NewServer(controller, logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers/io.github.evidai%2Fpolymarket-guard/logs?tail=10", http.NoBody)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "logs request should succeed; body=%s", w.Body.String())
	var resp contracts.APIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "io.github.evidai/polymarket-guard", controller.gotServerID,
		"server id must be percent-decoded before lookup")
}
