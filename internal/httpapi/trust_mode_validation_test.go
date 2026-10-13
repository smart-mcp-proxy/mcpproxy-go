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

// TestHandlePatchServer_RejectsInvalidTrustMode is the REST half of GH #938
// finding 1: `PATCH /api/v1/servers/<name> -d '{"trust_mode":"yolo"}'` used to
// answer 200, echo the bogus value back, and persist it. It must now be a 400
// naming the accepted values, and UpdateServer must never be called.
func TestHandlePatchServer_RejectsInvalidTrustMode(t *testing.T) {
	logger := zap.NewNop().Sugar()

	for _, mode := range []string{"yolo", "Scan", "SCAN", "off"} {
		t.Run(mode, func(t *testing.T) {
			mockCtrl := &mockPatchServerController{
				apiKey: "test-key",
				existingServer: &config.ServerConfig{
					Name:      "github",
					Protocol:  "stdio",
					TrustMode: string(config.TrustModeScan),
				},
			}
			srv := NewServer(mockCtrl, logger, nil)

			body, _ := json.Marshal(map[string]any{"trust_mode": mode})
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/github", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "test-key")
			w := httptest.NewRecorder()

			srv.ServeHTTP(w, req)

			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			assert.Contains(t, w.Body.String(), "auto, scan, manual",
				"the 400 must list the valid trust_mode values")
			assert.Nil(t, mockCtrl.capturedUpdates,
				"an invalid trust_mode must never reach UpdateServer")
		})
	}
}

// TestHandlePatchServer_AcceptsValidTrustMode guards against the validation
// being over-eager: the three real modes still patch through.
func TestHandlePatchServer_AcceptsValidTrustMode(t *testing.T) {
	logger := zap.NewNop().Sugar()
	for _, mode := range []string{"auto", "scan", "manual"} {
		t.Run(mode, func(t *testing.T) {
			mockCtrl := &mockPatchServerController{
				apiKey:         "test-key",
				existingServer: &config.ServerConfig{Name: "github", Protocol: "stdio"},
			}
			srv := NewServer(mockCtrl, logger, nil)

			body, _ := json.Marshal(map[string]any{"trust_mode": mode})
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/github", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "test-key")
			w := httptest.NewRecorder()

			srv.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			require.NotNil(t, mockCtrl.capturedUpdates)
			assert.Equal(t, mode, mockCtrl.capturedUpdates.TrustMode)
		})
	}
}

// TestHandleAddServer_RejectsInvalidTrustMode covers the create half: POST
// /api/v1/servers with a bogus trust_mode must 400 rather than persisting a
// mode the runtime will silently treat as manual.
func TestHandleAddServer_RejectsInvalidTrustMode(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockAddServerController{apiKey: "test-key"}
	srv := NewServer(mockCtrl, logger, nil)

	body, _ := json.Marshal(map[string]any{
		"name":       "srv",
		"url":        "https://example.com/mcp",
		"trust_mode": "yolo",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "auto, scan, manual")
	assert.Nil(t, mockCtrl.captured, "an invalid trust_mode must never reach AddServer")
}

// TestHandleAddServer_AcceptsValidTrustMode keeps the happy path honest.
func TestHandleAddServer_AcceptsValidTrustMode(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockAddServerController{apiKey: "test-key"}
	srv := NewServer(mockCtrl, logger, nil)

	body, _ := json.Marshal(map[string]any{
		"name":       "srv",
		"url":        "https://example.com/mcp",
		"trust_mode": "scan",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.captured)
	assert.Equal(t, "scan", mockCtrl.captured.TrustMode)
}
