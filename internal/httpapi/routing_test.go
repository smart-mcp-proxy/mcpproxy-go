package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockRoutingController is a mock controller for routing endpoint tests
type mockRoutingController struct {
	baseController
	apiKey      string
	routingMode string
}

func (m *mockRoutingController) GetCurrentConfig() any {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *mockRoutingController) GetConfig() (*config.Config, error) {
	return &config.Config{
		APIKey:      m.apiKey,
		RoutingMode: m.routingMode,
	}, nil
}

func TestHandleGetRouting(t *testing.T) {
	t.Run("returns default retrieve_tools mode", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: ""}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/routing", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, config.RoutingModeRetrieveTools, resp.Data["routing_mode"])
		assert.NotEmpty(t, resp.Data["description"])
		assert.NotNil(t, resp.Data["endpoints"])
		assert.NotNil(t, resp.Data["available_modes"])
	})

	t.Run("returns direct mode", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "direct"}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/routing", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, "direct", resp.Data["routing_mode"])
		assert.Contains(t, resp.Data["description"], "directly")
	})

	t.Run("returns code_execution mode", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "code_execution"}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/routing", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, "code_execution", resp.Data["routing_mode"])
		assert.Contains(t, resp.Data["description"], "JavaScript")
	})

	t.Run("includes all endpoints", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/routing", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		endpoints, ok := resp.Data["endpoints"].(map[string]interface{})
		require.True(t, ok, "endpoints should be an object")
		assert.Equal(t, "/mcp", endpoints["default"])
		assert.Equal(t, "/mcp/all", endpoints["direct"])
		assert.Equal(t, "/mcp/code", endpoints["code_execution"])
		assert.Equal(t, "/mcp/call", endpoints["retrieve_tools"])
	})
}

func TestHandleGetStatus_IncludesRoutingMode(t *testing.T) {
	t.Run("includes routing_mode in status response", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "direct"}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, "direct", resp.Data["routing_mode"])
	})

	t.Run("defaults routing_mode to retrieve_tools", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: ""}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                   `json:"success"`
			Data    map[string]interface{} `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, config.RoutingModeRetrieveTools, resp.Data["routing_mode"])
	})
}
