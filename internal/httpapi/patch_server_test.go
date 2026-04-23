package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockPatchServerController captures the updates passed to UpdateServer so we
// can assert that PATCH requests preserve existing bool fields when the
// request body omits them.
type mockPatchServerController struct {
	baseController
	apiKey          string
	existingServer  *config.ServerConfig
	capturedUpdates *config.ServerConfig
}

func (m *mockPatchServerController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey}
}

func (m *mockPatchServerController) GetConfig() (*config.Config, error) {
	if m.existingServer == nil {
		return &config.Config{}, nil
	}
	return &config.Config{
		Servers: []*config.ServerConfig{m.existingServer},
	}, nil
}

func (m *mockPatchServerController) UpdateServer(_ context.Context, _ string, updates *config.ServerConfig) error {
	// Capture a shallow copy so subsequent mutations by the handler don't
	// surprise the assertion.
	clone := *updates
	m.capturedUpdates = &clone
	return nil
}

// TestHandlePatchServer_ArgsOnlyPreservesBools is a regression test for the
// macOS tray bug where editing a server's Args on the detail page silently
// disabled the server. The PATCH handler had been zeroing Enabled /
// Quarantined / ReconnectOnUse whenever the request body omitted them,
// because `config.ServerConfig` uses non-pointer bools whose zero value
// cannot be distinguished from "not set".
func TestHandlePatchServer_ArgsOnlyPreservesBools(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		existingServer: &config.ServerConfig{
			Name:           "github",
			Protocol:       "stdio",
			Command:        "npx",
			Args:           []string{"old-arg"},
			Enabled:        true,
			Quarantined:    false,
			ReconnectOnUse: true,
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	// Simulate the macOS tray saving only the Args field.
	body, _ := json.Marshal(map[string]any{
		"args": []string{"new-arg-1", "new-arg-2"},
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "Expected 200 OK, body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates, "UpdateServer should have been called")

	assert.Equal(t, []string{"new-arg-1", "new-arg-2"}, mockCtrl.capturedUpdates.Args,
		"Args should reflect the PATCH body")
	assert.True(t, mockCtrl.capturedUpdates.Enabled,
		"Enabled must be preserved from existing server (was true) when PATCH omits it")
	assert.False(t, mockCtrl.capturedUpdates.Quarantined,
		"Quarantined must be preserved from existing server (was false)")
	assert.True(t, mockCtrl.capturedUpdates.ReconnectOnUse,
		"ReconnectOnUse must be preserved from existing server (was true) when PATCH omits it")
}

// TestHandlePatchServer_ExplicitBoolsTakePrecedence verifies that the
// preservation logic does not clobber bools the request explicitly sets.
func TestHandlePatchServer_ExplicitBoolsTakePrecedence(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		existingServer: &config.ServerConfig{
			Name:           "github",
			Protocol:       "stdio",
			Enabled:        true,
			Quarantined:    false,
			ReconnectOnUse: true,
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	enabled := false
	body, _ := json.Marshal(map[string]any{
		"enabled": enabled,
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "Expected 200 OK, body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)

	assert.False(t, mockCtrl.capturedUpdates.Enabled,
		"Enabled must reflect the explicit request value (false)")
	assert.False(t, mockCtrl.capturedUpdates.Quarantined,
		"Quarantined must be preserved from existing server (was false)")
	assert.True(t, mockCtrl.capturedUpdates.ReconnectOnUse,
		"ReconnectOnUse must be preserved from existing server (was true)")
}
