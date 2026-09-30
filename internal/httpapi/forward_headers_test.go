package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func fwdPost(t *testing.T, srv *Server, path, method string, body map[string]any) *httptest.ResponseRecorder {
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

// Spec 112 FR-020: create carries forward_headers through and rejects bad names.
func TestHandleAddServer_ForwardHeaders(t *testing.T) {
	logger := zap.NewNop().Sugar()

	t.Run("allowlist is carried through on create", func(t *testing.T) {
		ctrl := &mockAddServerController{apiKey: "test-key"}
		w := fwdPost(t, NewServer(ctrl, logger, nil), "/api/v1/servers", http.MethodPost, map[string]any{
			"name": "up", "url": "https://example.com/mcp", "protocol": "streamable-http",
			"forward_headers": []string{"X-User-Id", "X-Tenant-Id"},
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.NotNil(t, ctrl.captured)
		assert.Equal(t, []string{"X-User-Id", "X-Tenant-Id"}, ctrl.captured.ForwardHeaders)
	})

	for name, tc := range map[string]struct {
		names   []string
		headers map[string]string
	}{
		"denied":    {names: []string{"Authorization"}},
		"wildcard":  {names: []string{"X-*"}},
		"duplicate": {names: []string{"X-A", "x-a"}},
		"static":    {names: []string{"X-Api-Thing"}, headers: map[string]string{"x-api-thing": "topsecret"}},
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			ctrl := &mockAddServerController{apiKey: "test-key"}
			body := map[string]any{
				"name": "up", "url": "https://example.com/mcp", "protocol": "streamable-http",
				"forward_headers": tc.names,
			}
			if tc.headers != nil {
				body["headers"] = tc.headers
			}
			w := fwdPost(t, NewServer(ctrl, logger, nil), "/api/v1/servers", http.MethodPost, body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "forward_headers")
			assert.NotContains(t, w.Body.String(), "topsecret")
			assert.Nil(t, ctrl.captured, "an invalid allowlist must not create the server")
		})
	}
}

// Spec 112 FR-020: PATCH preserves on omit, replaces on non-nil, clears on [].
func TestHandlePatchServer_ForwardHeaders(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mk := func() *mockPatchServerController {
		return &mockPatchServerController{
			apiKey: "test-key",
			existingServer: &config.ServerConfig{
				Name: "up", Protocol: "streamable-http", URL: "https://example.com/mcp", Enabled: true,
				ForwardHeaders: []string{"X-User-Id"},
				Headers:        map[string]string{"X-Static": "v"},
			},
		}
	}

	t.Run("omitted preserves", func(t *testing.T) {
		ctrl := mk()
		w := fwdPost(t, NewServer(ctrl, logger, nil), "/api/v1/servers/up", http.MethodPatch, map[string]any{"args": []string{"x"}})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, []string{"X-User-Id"}, ctrl.capturedUpdates.ForwardHeaders)
	})
	t.Run("non-nil replaces", func(t *testing.T) {
		ctrl := mk()
		w := fwdPost(t, NewServer(ctrl, logger, nil), "/api/v1/servers/up", http.MethodPatch, map[string]any{"forward_headers": []string{"X-Tenant-Id"}})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, []string{"X-Tenant-Id"}, ctrl.capturedUpdates.ForwardHeaders)
	})
	t.Run("empty array clears", func(t *testing.T) {
		ctrl := mk()
		w := fwdPost(t, NewServer(ctrl, logger, nil), "/api/v1/servers/up", http.MethodPatch, map[string]any{"forward_headers": []string{}})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.NotNil(t, ctrl.capturedUpdates.ForwardHeaders, "cleared must be a non-nil empty slice so UpdateServer applies it")
		assert.Empty(t, ctrl.capturedUpdates.ForwardHeaders)
	})
	t.Run("denied name is rejected and nothing is written", func(t *testing.T) {
		ctrl := mk()
		w := fwdPost(t, NewServer(ctrl, logger, nil), "/api/v1/servers/up", http.MethodPatch, map[string]any{"forward_headers": []string{"Cookie"}})
		assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Nil(t, ctrl.capturedUpdates)
	})
	t.Run("collision with an existing static header is rejected", func(t *testing.T) {
		ctrl := mk()
		w := fwdPost(t, NewServer(ctrl, logger, nil), "/api/v1/servers/up", http.MethodPatch, map[string]any{"forward_headers": []string{"x-static"}})
		assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Nil(t, ctrl.capturedUpdates)
	})
	t.Run("a headers change that collides with the stored allowlist is rejected", func(t *testing.T) {
		ctrl := mk()
		w := fwdPost(t, NewServer(ctrl, logger, nil), "/api/v1/servers/up", http.MethodPatch, map[string]any{"headers": map[string]string{"x-user-id": "v"}})
		assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Nil(t, ctrl.capturedUpdates)
	})
}
