package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/contracts"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestHandleAddServer tests the POST /api/v1/servers endpoint
func TestHandleAddServer(t *testing.T) {
	t.Run("adds HTTP server successfully", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		reqBody := AddServerRequest{
			Name:     "test-http",
			URL:      "https://example.com/mcp",
			Protocol: "http",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK")

		var resp struct {
			Success bool                          `json:"success"`
			Data    contracts.ServerActionResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, "test-http", resp.Data.Server)
		assert.Equal(t, "add", resp.Data.Action)
		assert.True(t, resp.Data.Success)
	})

	t.Run("adds stdio server successfully", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		reqBody := AddServerRequest{
			Name:     "test-stdio",
			Command:  "npx",
			Args:     []string{"-y", "@anthropic/mcp-server"},
			Protocol: "stdio",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK")
	})

	t.Run("rejects duplicate server", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{
			apiKey:       "test-key",
			existsServer: "existing-server",
		}
		srv := NewServer(mockCtrl, logger, nil)

		reqBody := AddServerRequest{
			Name:     "existing-server",
			URL:      "https://example.com/mcp",
			Protocol: "http",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code, "Expected 409 Conflict")
	})

	t.Run("rejects missing name", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		reqBody := AddServerRequest{
			URL:      "https://example.com/mcp",
			Protocol: "http",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 Bad Request")
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader([]byte("invalid json")))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 Bad Request")
	})
}

// TestHandleRemoveServer tests the DELETE /api/v1/servers/{name} endpoint
func TestHandleRemoveServer(t *testing.T) {
	t.Run("removes server successfully", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRemoveServerController{
			apiKey:       "test-key",
			existsServer: "test-server",
		}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/servers/test-server", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK")

		var resp struct {
			Success bool                          `json:"success"`
			Data    contracts.ServerActionResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, "test-server", resp.Data.Server)
		assert.Equal(t, "remove", resp.Data.Action)
		assert.True(t, resp.Data.Success)
	})

	t.Run("returns 404 for non-existent server", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRemoveServerController{
			apiKey:       "test-key",
			existsServer: "other-server",
		}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/servers/non-existent", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code, "Expected 404 Not Found")
	})
}

// mockAddServerController is a mock controller for add server tests
type mockAddServerController struct {
	baseController
	apiKey       string
	existsServer string
}

func (m *mockAddServerController) GetCurrentConfig() any {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *mockAddServerController) AddServer(_ context.Context, cfg *config.ServerConfig) error {
	if cfg.Name == m.existsServer {
		return fmt.Errorf("server '%s' already exists", cfg.Name)
	}
	return nil
}

// mockRemoveServerController is a mock controller for remove server tests
type mockRemoveServerController struct {
	baseController
	apiKey       string
	existsServer string
}

func (m *mockRemoveServerController) GetCurrentConfig() any {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *mockRemoveServerController) RemoveServer(_ context.Context, name string) error {
	if name != m.existsServer {
		return fmt.Errorf("server '%s' not found", name)
	}
	return nil
}
