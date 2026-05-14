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

// TestHandlePatchServer_HeadersDeepMerge verifies that PATCH /api/v1/servers
// preserves existing header keys not mentioned in the request body. This is
// the foundation of the Web UI / macOS tray edit flow: clients send a diff
// against the redacted view of headers, so any key whose value is
// `***REDACTED***` and unchanged stays out of the patch — and the backend
// must NOT wipe it.
func TestHandlePatchServer_HeadersDeepMerge(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		existingServer: &config.ServerConfig{
			Name:     "synapbus",
			URL:      "https://example.com/mcp",
			Protocol: "streamable-http",
			Enabled:  true,
			Headers: map[string]string{
				"Authorization": "Bearer real-secret-token",
				"X-Trace":       "on",
			},
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	// Client sends just X-New-Header. Authorization is omitted because
	// its redacted view (`***REDACTED***`) matched the original, and
	// X-Trace is omitted because the user didn't touch it.
	body, _ := json.Marshal(map[string]any{
		"headers": map[string]string{"X-New-Header": "new-value"},
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/synapbus", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)

	got := mockCtrl.capturedUpdates.Headers
	assert.Equal(t, "Bearer real-secret-token", got["Authorization"],
		"Authorization must be preserved verbatim — it was not in the PATCH body and the real secret must not be wiped")
	assert.Equal(t, "on", got["X-Trace"], "X-Trace must be preserved")
	assert.Equal(t, "new-value", got["X-New-Header"], "X-New-Header must be added")
	assert.Len(t, got, 3, "exactly 3 headers expected (Authorization, X-Trace, X-New-Header)")
}

// TestHandlePatchServer_HeadersRemove verifies that the `headers_remove`
// field deletes the listed keys from the stored map. Combined with the
// merge behaviour above, this is how clients delete a header through PATCH.
func TestHandlePatchServer_HeadersRemove(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		existingServer: &config.ServerConfig{
			Name:     "synapbus",
			Protocol: "http",
			URL:      "https://example.com/mcp",
			Enabled:  true,
			Headers: map[string]string{
				"Authorization": "Bearer token",
				"X-Trace":       "on",
				"X-Old":         "stale",
			},
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	body, _ := json.Marshal(map[string]any{
		"headers_remove": []string{"X-Old", "X-Trace"},
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/synapbus", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)

	got := mockCtrl.capturedUpdates.Headers
	assert.Equal(t, "Bearer token", got["Authorization"], "Authorization untouched")
	assert.Len(t, got, 1, "only Authorization should remain (X-Old and X-Trace removed)")
	_, hasOld := got["X-Old"]
	_, hasTrace := got["X-Trace"]
	assert.False(t, hasOld, "X-Old must be removed")
	assert.False(t, hasTrace, "X-Trace must be removed")
}

// TestHandlePatchServer_HeadersSetAndRemove combines the merge + remove
// operations in a single PATCH. The same body shape the Web UI and macOS
// tray send when the user simultaneously edits one header and deletes
// another.
func TestHandlePatchServer_HeadersSetAndRemove(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		existingServer: &config.ServerConfig{
			Name:     "synapbus",
			Protocol: "http",
			URL:      "https://example.com/mcp",
			Enabled:  true,
			Headers: map[string]string{
				"Authorization": "Bearer old-token",
				"X-Trace":       "on",
			},
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	body, _ := json.Marshal(map[string]any{
		"headers": map[string]string{
			"Authorization": "Bearer new-token",
			"X-New":         "new-value",
		},
		"headers_remove": []string{"X-Trace"},
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/synapbus", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)

	got := mockCtrl.capturedUpdates.Headers
	assert.Equal(t, "Bearer new-token", got["Authorization"], "Authorization updated to new value")
	assert.Equal(t, "new-value", got["X-New"], "X-New added")
	_, hasTrace := got["X-Trace"]
	assert.False(t, hasTrace, "X-Trace deleted")
	assert.Len(t, got, 2)
}

// TestHandlePatchServer_EnvDeepMerge mirrors HeadersDeepMerge for env vars.
// The redaction risk is the same — backend doesn't redact env values today,
// but the merge semantics are needed for symmetric UI editing.
func TestHandlePatchServer_EnvDeepMerge(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		existingServer: &config.ServerConfig{
			Name:     "demo",
			Protocol: "stdio",
			Command:  "uvx",
			Enabled:  true,
			Env: map[string]string{
				"API_KEY":   "live-secret",
				"LOG_LEVEL": "debug",
			},
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	body, _ := json.Marshal(map[string]any{
		"env":        map[string]string{"NEW_VAR": "value"},
		"env_remove": []string{"LOG_LEVEL"},
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/demo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)

	got := mockCtrl.capturedUpdates.Env
	assert.Equal(t, "live-secret", got["API_KEY"], "API_KEY preserved (not in patch body)")
	assert.Equal(t, "value", got["NEW_VAR"], "NEW_VAR added")
	_, hasLog := got["LOG_LEVEL"]
	assert.False(t, hasLog, "LOG_LEVEL deleted via env_remove")
	assert.Len(t, got, 2)
}
