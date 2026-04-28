package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestHandleConnectClient_OpenCodeAdoptedAlreadyExistsReturns200(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	srv.SetConnectService(svc)

	cfgPath := connect.ConfigPath("opencode", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"mcp":{"proxy-alt":{"type":"remote","url":"http://127.0.0.1:8080/mcp"}}}`), 0o644))

	body, _ := json.Marshal(ConnectRequest{ServerName: "mcpproxy", Force: false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/opencode", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Success bool                   `json:"success"`
		Data    connect.ConnectResult  `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "already_exists", resp.Data.Action)
	assert.Equal(t, "proxy-alt", resp.Data.ServerName)
}

func TestHandleConnectClient_OpenCodeMissingConfigReturns400(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	srv.SetConnectService(connect.NewServiceWithHome("127.0.0.1:8080", "", t.TempDir()))

	body, _ := json.Marshal(ConnectRequest{ServerName: "mcpproxy", Force: false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/opencode", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "does not exist")
}

func TestHandleConnectClient_TrueConflictStillReturns409(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, logger, nil)
	home := t.TempDir()
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	srv.SetConnectService(svc)

	_, err := svc.Connect("claude-code", "mcpproxy", false)
	require.NoError(t, err)

	body, _ := json.Marshal(ConnectRequest{ServerName: "mcpproxy", Force: false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connect/claude-code", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)

	var resp struct {
		Success bool                  `json:"success"`
		Data    connect.ConnectResult `json:"data"`
		Error   string                `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Success)
	assert.Equal(t, "already_exists", resp.Data.Action)
	assert.NotEmpty(t, resp.Error)
}
