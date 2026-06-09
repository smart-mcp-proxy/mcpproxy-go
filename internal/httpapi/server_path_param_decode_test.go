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

// capturingToolCallsController records the serverID forwarded to the controller
// so the test can assert the {id} path param was percent-decoded before lookup.
type capturingToolCallsController struct {
	*MockServerController
	gotServerID string
}

func (c *capturingToolCallsController) GetServerToolCalls(serverID string, _ int) ([]*contracts.ToolCallRecord, error) {
	c.gotServerID = serverID
	return nil, nil
}

// TestServerSubresource_SlashServerIDUnescaped reproduces MCP-1118: official
// modelcontextprotocol/registry v0.1 server ids are namespace/name (e.g.
// io.github.owner/repo) and chi routes on RawPath, so the {id} param of every
// /api/v1/servers/{id}/* sub-resource handler arrives percent-encoded
// (io.github.owner%2Frepo). Without decoding, the exact-match server lookup
// targets the literal encoded name and 404s, so the Web UI server-detail Tools
// and Logs tabs (and CLI sub-resources) never load for slash-name servers.
//
// The fix decodes {id} once at the /servers/{id} route subtree, so this asserts
// the decoded name reaches both the management-service path (tools, restart) and
// the controller path (tool-calls).
func TestServerSubresource_SlashServerIDUnescaped(t *testing.T) {
	const (
		encoded = "io.github.owner%2Frepo"
		decoded = "io.github.owner/repo"
	)

	// dataField unwraps the APIResponse envelope and returns the requested
	// string field from the data object.
	dataString := func(t *testing.T, body []byte, key string) string {
		t.Helper()
		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(body, &resp), "body=%s", string(body))
		require.True(t, resp.Success, "request should succeed; body=%s", string(body))
		v, _ := resp.Data[key].(string)
		return v
	}

	t.Run("GET /tools echoes decoded server name", func(t *testing.T) {
		logger := zaptest.NewLogger(t).Sugar()
		server := NewServer(&MockServerController{}, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/servers/"+encoded+"/tools", http.NoBody)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, decoded, dataString(t, w.Body.Bytes(), "server_name"),
			"server id must be percent-decoded before lookup")
	})

	t.Run("POST /restart echoes decoded server name", func(t *testing.T) {
		logger := zaptest.NewLogger(t).Sugar()
		server := NewServer(&MockServerController{}, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/"+encoded+"/restart", http.NoBody)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, decoded, dataString(t, w.Body.Bytes(), "server"),
			"server id must be percent-decoded before lookup")
	})

	t.Run("GET /tool-calls forwards decoded server name to controller", func(t *testing.T) {
		logger := zaptest.NewLogger(t).Sugar()
		controller := &capturingToolCallsController{MockServerController: &MockServerController{}}
		server := NewServer(controller, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/servers/"+encoded+"/tool-calls", http.NoBody)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, decoded, controller.gotServerID,
			"server id must be percent-decoded before reaching the controller")
		assert.Equal(t, decoded, dataString(t, w.Body.Bytes(), "server_name"))
	})
}
