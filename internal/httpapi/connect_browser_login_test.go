//go:build !server

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Spec 132 PR-3: the connect doors carry the browser-login choice.

func browserLoginServer(t *testing.T, on bool) (*Server, string) {
	t.Helper()
	mockCtrl := &mockRoutingController{apiKey: "test-key", routingMode: "retrieve_tools"}
	srv := NewServer(mockCtrl, zap.NewNop().Sugar(), nil)
	home := t.TempDir()
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home).WithMCPOAuthEnabled(func() bool { return on })
	srv.SetConnectService(svc)
	return srv, home
}

func doConnect(srv *Server, method, path string, body interface{}) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestConnectREST_AuthOAuthWritesTheAddress(t *testing.T) {
	srv, home := browserLoginServer(t, true)
	w := doConnect(srv, http.MethodPost, "/api/v1/connect/claude-code", ConnectRequest{Auth: "oauth"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		Data connect.ConnectResult `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "oauth", resp.Data.Auth)
	assert.Empty(t, resp.Data.Credential)
	assert.Contains(t, resp.Data.AuthNote, "not revoke")

	raw, err := os.ReadFile(connect.ConfigPath("claude-code", home))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "http://127.0.0.1:8080/mcp/oauth/claude-code")
	assert.NotContains(t, string(raw), "headers")
}

func TestConnectREST_AuthOAuthRefusals(t *testing.T) {
	t.Run("feature off", func(t *testing.T) {
		srv, home := browserLoginServer(t, false)
		w := doConnect(srv, http.MethodPost, "/api/v1/connect/claude-code", ConnectRequest{Auth: "oauth"})
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "mcp_oauth_enabled")
		_, err := os.Stat(connect.ConfigPath("claude-code", home))
		assert.True(t, os.IsNotExist(err), "nothing may be written")
	})
	t.Run("unsupported client", func(t *testing.T) {
		srv, _ := browserLoginServer(t, true)
		w := doConnect(srv, http.MethodPost, "/api/v1/connect/cursor", ConnectRequest{Auth: "oauth"})
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "does not support")
	})
	t.Run("unknown value", func(t *testing.T) {
		srv, _ := browserLoginServer(t, true)
		w := doConnect(srv, http.MethodPost, "/api/v1/connect/claude-code", ConnectRequest{Auth: "magic"})
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	})
}

func TestConnectREST_PreviewAuthOAuth(t *testing.T) {
	srv, _ := browserLoginServer(t, true)
	w := doConnect(srv, http.MethodGet, "/api/v1/connect/claude-code/preview?auth=oauth", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data connect.ConnectPreview `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "oauth", resp.Data.Auth)
	assert.Empty(t, resp.Data.Credential)
	assert.Contains(t, resp.Data.EntryText, "/mcp/oauth/claude-code")
	assert.NotContains(t, resp.Data.EntryText, "mcp_cli_")

	w = doConnect(srv, http.MethodGet, "/api/v1/connect/cursor/preview?auth=oauth", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

	// Without the parameter the preview is the one it always was.
	w = doConnect(srv, http.MethodGet, "/api/v1/connect/claude-code/preview", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), `"auth"`)
	assert.NotContains(t, w.Body.String(), "auth_note")
}

func TestConnectREST_ListCarriesSupportsBrowserLogin(t *testing.T) {
	for _, on := range []bool{false, true} {
		srv, _ := browserLoginServer(t, on)
		w := doConnect(srv, http.MethodGet, "/api/v1/connect", nil)
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Data []connect.ClientStatus `json:"data"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		seen := 0
		for _, c := range resp.Data {
			assert.Equal(t, c.ID == "claude-code", c.SupportsBrowserLogin, c.ID)
			assert.Equal(t, on && c.ID == "claude-code", c.BrowserLoginAvailable, c.ID)
			seen++
		}
		assert.Positive(t, seen)
	}
}
