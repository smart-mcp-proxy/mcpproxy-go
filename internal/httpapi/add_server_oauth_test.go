package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func serveJSON(t *testing.T, srv *Server, method, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// TestHandleAddServer_OAuth: POST /api/v1/servers had no oauth field, so an
// oauth block (and with it `upstream add-json … {"oauth":…}`) was dropped and
// the server failed sign-in with "no oauth.client_id is configured".
func TestHandleAddServer_OAuth(t *testing.T) {
	logger := zap.NewNop().Sugar()

	t.Run("carried through on create", func(t *testing.T) {
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		w := serveJSON(t, NewServer(mockCtrl, logger, nil), http.MethodPost, "/api/v1/servers", map[string]any{
			"name": "ha",
			"url":  "https://ha.example.com/api/mcp",
			"oauth": map[string]any{
				"client_id":    "http://127.0.0.1:8765/",
				"redirect_uri": "http://127.0.0.1:8765/oauth/callback",
			},
		})
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		require.NotNil(t, mockCtrl.captured)
		require.NotNil(t, mockCtrl.captured.OAuth)
		assert.Equal(t, "http://127.0.0.1:8765/", mockCtrl.captured.OAuth.ClientID)
		assert.Equal(t, "http://127.0.0.1:8765/oauth/callback", mockCtrl.captured.OAuth.RedirectURI)
	})

	t.Run("invalid block refused before AddServer", func(t *testing.T) {
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		w := serveJSON(t, NewServer(mockCtrl, logger, nil), http.MethodPost, "/api/v1/servers", map[string]any{
			"name":  "ha",
			"url":   "https://ha.example.com/api/mcp",
			"oauth": map[string]any{"redirect_uri": "https://example.com/callback"},
		})
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), "oauth")
		assert.Nil(t, mockCtrl.captured)
	})
}

// TestHandlePatchServer_RefusesOAuth: PATCH shares the request type but has no
// oauth merge, so the field must be refused rather than decoded and ignored.
func TestHandlePatchServer_RefusesOAuth(t *testing.T) {
	mockCtrl := &mockPatchServerController{
		apiKey:         "test-key",
		existingServer: &config.ServerConfig{Name: "ha", Protocol: "http"},
	}
	w := serveJSON(t, NewServer(mockCtrl, zap.NewNop().Sugar(), nil), http.MethodPatch, "/api/v1/servers/ha", map[string]any{
		"oauth": map[string]any{"client_id": "x"},
	})
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "oauth cannot be changed with PATCH")
	assert.Nil(t, mockCtrl.capturedUpdates)
}
