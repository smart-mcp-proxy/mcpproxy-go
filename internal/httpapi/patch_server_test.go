package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"

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
	allServers      []map[string]interface{}
}

// GetAllServers feeds the legacy GET /api/v1/servers path (used when no
// management service is wired). Returning generic maps lets us assert how
// the handler projects server-status fields into the JSON response.
func (m *mockPatchServerController) GetAllServers() ([]map[string]interface{}, error) {
	return m.allServers, nil
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
// against the redacted view of headers, so any key whose masked-display
// value (`••••<last2> (<N> chars)`) is unchanged stays out of the patch —
// and the backend must NOT wipe it.
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
	// its masked view (`••••<last2> (<N> chars)`) matched the original
	// in the diff, and X-Trace is omitted because the user didn't touch it.
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

// TestHandlePatchServer_HeadersNullDelete verifies that a JSON null value
// in the `headers` map deletes the key under JSON Merge Patch semantics.
// This is the unified delete syntax aligned with the MCP `upstream_servers
// patch` tool — no separate `headers_remove` array is needed.
//
// We use json.RawMessage to inject literal nulls because Go's reflect-based
// marshaling can collapse map[string]any{"k": nil} into omitted entries
// depending on the path; raw bytes make the test independent of that.
func TestHandlePatchServer_HeadersNullDelete(t *testing.T) {
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

	rawBody := []byte(`{"headers":{"X-Old":null,"X-Trace":null}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/synapbus", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)

	got := mockCtrl.capturedUpdates.Headers
	assert.Equal(t, "Bearer token", got["Authorization"], "Authorization untouched")
	assert.Len(t, got, 1, "only Authorization should remain (X-Old and X-Trace deleted via null)")
	_, hasOld := got["X-Old"]
	_, hasTrace := got["X-Trace"]
	assert.False(t, hasOld, "X-Old must be removed")
	assert.False(t, hasTrace, "X-Trace must be removed")
}

// TestHandlePatchServer_HeadersSetAndDelete combines upsert + null-delete
// in a single PATCH. Same body shape the Web UI / macOS tray / CLI emit
// when the user simultaneously edits one header and deletes another.
func TestHandlePatchServer_HeadersSetAndDelete(t *testing.T) {
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

	rawBody := []byte(`{"headers":{"Authorization":"Bearer new-token","X-New":"new-value","X-Trace":null}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/synapbus", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)

	got := mockCtrl.capturedUpdates.Headers
	assert.Equal(t, "Bearer new-token", got["Authorization"], "Authorization updated")
	assert.Equal(t, "new-value", got["X-New"], "X-New added")
	_, hasTrace := got["X-Trace"]
	assert.False(t, hasTrace, "X-Trace deleted via null")
	assert.Len(t, got, 2)
}

// TestHandlePatchServer_EnvDeepMerge mirrors HeadersDeepMerge for env vars,
// using the unified null-delete syntax.
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

	rawBody := []byte(`{"env":{"NEW_VAR":"value","LOG_LEVEL":null}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/demo", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)

	got := mockCtrl.capturedUpdates.Env
	assert.Equal(t, "live-secret", got["API_KEY"], "API_KEY preserved")
	assert.Equal(t, "value", got["NEW_VAR"], "NEW_VAR added")
	_, hasLog := got["LOG_LEVEL"]
	assert.False(t, hasLog, "LOG_LEVEL deleted via null")
	assert.Len(t, got, 2)
}

// TestHandlePatchServer_HeadersEmptyStringSetsNotDeletes pins the
// distinction between `""` (set to empty) and `null` (delete). Empty
// string is a legitimate header / env value — many consumers treat it
// differently from "unset". A client that wants to clear a header to
// empty string must send `""`; a client that wants to remove the key
// entirely must send `null`. Without this test, a future refactor that
// "helpfully" collapses empty string to delete (or vice versa) would go
// unnoticed.
func TestHandlePatchServer_HeadersEmptyStringSetsNotDeletes(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		existingServer: &config.ServerConfig{
			Name:     "demo",
			Protocol: "http",
			URL:      "https://example.com/mcp",
			Enabled:  true,
			Headers: map[string]string{
				"X-Original": "value",
			},
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	rawBody := []byte(`{"headers":{"X-Empty":""}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/demo", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	got := mockCtrl.capturedUpdates.Headers
	v, hasEmpty := got["X-Empty"]
	assert.True(t, hasEmpty, "X-Empty must be present (empty string is not delete)")
	assert.Equal(t, "", v, "X-Empty must be the empty string, not deleted")
	assert.Equal(t, "value", got["X-Original"], "X-Original must be preserved")
	assert.Len(t, got, 2)
}

// TestHandleConvertConfigToSecret_ValidationErrors covers the input
// validation paths on POST /api/v1/servers/{id}/config-to-secret that
// happen BEFORE we touch the secret resolver — so they're testable with
// a nil resolver via the existing mock. Happy-path lives in the live
// verification scripts because secret.Resolver is a concrete struct and
// faking it would mean stubbing the whole keyring provider chain.
func TestHandleConvertConfigToSecret_ValidationErrors(t *testing.T) {
	logger := zap.NewNop().Sugar()

	cases := []struct {
		name       string
		existing   *config.ServerConfig
		body       string
		wantStatus int
		wantInBody string
	}{
		{
			name:       "missing scope",
			existing:   &config.ServerConfig{Name: "synapbus", Protocol: "http", Enabled: true},
			body:       `{"key": "Authorization", "secret_name": "synapbus-auth"}`,
			wantStatus: 400,
			wantInBody: `scope`,
		},
		{
			name:       "invalid scope",
			existing:   &config.ServerConfig{Name: "synapbus", Protocol: "http", Enabled: true},
			body:       `{"scope": "isolation", "key": "image", "secret_name": "foo"}`,
			wantStatus: 400,
			wantInBody: `scope`,
		},
		{
			name:       "missing key",
			existing:   &config.ServerConfig{Name: "synapbus", Protocol: "http", Enabled: true},
			body:       `{"scope": "header", "secret_name": "synapbus-auth"}`,
			wantStatus: 400,
			wantInBody: `key`,
		},
		{
			name:       "missing secret name",
			existing:   &config.ServerConfig{Name: "synapbus", Protocol: "http", Enabled: true},
			body:       `{"scope": "header", "key": "Authorization"}`,
			wantStatus: 400,
			wantInBody: `secret_name`,
		},
		{
			name: "key not present on server",
			existing: &config.ServerConfig{
				Name: "synapbus", Protocol: "http", Enabled: true,
				Headers: map[string]string{"X-Trace": "on"},
			},
			body:       `{"scope": "header", "key": "Authorization", "secret_name": "synapbus-auth"}`,
			wantStatus: 404,
			wantInBody: "Authorization",
		},
		{
			name: "value is already a keyring reference",
			existing: &config.ServerConfig{
				Name: "synapbus", Protocol: "http", Enabled: true,
				Headers: map[string]string{"Authorization": "${keyring:already-stored}"},
			},
			body:       `{"scope": "header", "key": "Authorization", "secret_name": "synapbus-auth"}`,
			wantStatus: 400,
			wantInBody: "already a reference",
		},
		{
			name: "value is already an env reference",
			existing: &config.ServerConfig{
				Name: "synapbus", Protocol: "http", Enabled: true,
				Headers: map[string]string{"Authorization": "${env:FOO}"},
			},
			body:       `{"scope": "header", "key": "Authorization", "secret_name": "synapbus-auth"}`,
			wantStatus: 400,
			wantInBody: "already a reference",
		},
		{
			name: "empty value",
			existing: &config.ServerConfig{
				Name: "synapbus", Protocol: "http", Enabled: true,
				Headers: map[string]string{"Authorization": ""},
			},
			body:       `{"scope": "header", "key": "Authorization", "secret_name": "synapbus-auth"}`,
			wantStatus: 400,
			wantInBody: "no value to store",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := &mockPatchServerController{
				apiKey:         "test-key",
				existingServer: tc.existing,
			}
			srv := NewServer(mockCtrl, logger, nil)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/synapbus/config-to-secret", bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "test-key")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code, "body=%s", w.Body.String())
			require.Contains(t, w.Body.String(), tc.wantInBody)
			// The update path must not have been invoked when validation
			// rejects the request.
			assert.Nil(t, mockCtrl.capturedUpdates, "validation errors must not call UpdateServer")
		})
	}
}

// TestHandlePatchServer_AutoApproveToolChanges covers the MCP-2940 gap: the
// REST PATCH layer must accept and persist the per-server
// auto_approve_tool_changes flag (a tri-state *bool added to ServerConfig in
// MCP-2930). Mirrors the *bool nil-preserve semantics — omitting the field on
// PATCH must not clobber a previously-set value, while an explicit value
// (including false) must round-trip.
func TestHandlePatchServer_AutoApproveToolChanges(t *testing.T) {
	logger := zap.NewNop().Sugar()

	boolPtr := func(b bool) *bool { return &b }

	newServer := func() *config.ServerConfig {
		return &config.ServerConfig{
			Name:        "github",
			Protocol:    "stdio",
			Command:     "npx",
			Enabled:     true,
			Quarantined: false,
		}
	}

	patch := func(t *testing.T, existing *config.ServerConfig, body string) *config.ServerConfig {
		t.Helper()
		mockCtrl := &mockPatchServerController{apiKey: "test-key", existingServer: existing}
		srv := NewServer(mockCtrl, logger, nil)
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/github", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		require.NotNil(t, mockCtrl.capturedUpdates, "UpdateServer should have been called")
		return mockCtrl.capturedUpdates
	}

	t.Run("explicit true sets the pointer", func(t *testing.T) {
		updates := patch(t, newServer(), `{"auto_approve_tool_changes":true}`)
		require.NotNil(t, updates.AutoApproveToolChanges, "pointer must be set when the request provides it")
		assert.True(t, *updates.AutoApproveToolChanges, "value must reflect the request (true)")
	})

	t.Run("omitting preserves nil existing", func(t *testing.T) {
		existing := newServer() // AutoApproveToolChanges is nil
		updates := patch(t, existing, `{"args":["new-arg"]}`)
		assert.Nil(t, updates.AutoApproveToolChanges,
			"omitting the field must preserve the unset (nil) existing value")
	})

	t.Run("omitting preserves a prior true", func(t *testing.T) {
		existing := newServer()
		existing.AutoApproveToolChanges = boolPtr(true)
		updates := patch(t, existing, `{"args":["new-arg"]}`)
		require.NotNil(t, updates.AutoApproveToolChanges,
			"omitting the field must preserve the existing pointer")
		assert.True(t, *updates.AutoApproveToolChanges, "prior true must be preserved")
	})

	t.Run("explicit false overrides a prior true", func(t *testing.T) {
		existing := newServer()
		existing.AutoApproveToolChanges = boolPtr(true)
		updates := patch(t, existing, `{"auto_approve_tool_changes":false}`)
		require.NotNil(t, updates.AutoApproveToolChanges,
			"explicit false must set the pointer, not leave it nil")
		assert.False(t, *updates.AutoApproveToolChanges, "explicit false must override the prior true")
	})
}

// TestHandleGetServers_ExposesAutoApproveToolChanges verifies the GET
// /api/v1/servers payload surfaces auto_approve_tool_changes so the Web UI
// toggle can reflect the persisted value (MCP-2940). Exercises the legacy
// projection path (ConvertGenericServersToTyped) via a generic server map.
func TestHandleGetServers_ExposesAutoApproveToolChanges(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		allServers: []map[string]interface{}{
			{
				"id":                        "github",
				"name":                      "github",
				"enabled":                   true,
				"quarantined":               false,
				"auto_approve_tool_changes": true,
			},
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Data struct {
			Servers []struct {
				Name                   string `json:"name"`
				AutoApproveToolChanges *bool  `json:"auto_approve_tool_changes"`
			} `json:"servers"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Servers, 1)
	require.NotNil(t, resp.Data.Servers[0].AutoApproveToolChanges,
		"auto_approve_tool_changes must appear in the GET payload")
	assert.True(t, *resp.Data.Servers[0].AutoApproveToolChanges)
}

// TestHandlePatchServer_InitTimeout verifies the per-server init_timeout
// override (MCP-3322) plumbs through PATCH with tri-state *Duration
// nil-preserve semantics: a present value is applied, an omitted field
// preserves the existing pointer.
func TestHandlePatchServer_InitTimeout(t *testing.T) {
	logger := zap.NewNop().Sugar()

	durPtr := func(d time.Duration) *config.Duration { v := config.Duration(d); return &v }

	newServer := func() *config.ServerConfig {
		return &config.ServerConfig{
			Name:        "slack",
			Protocol:    "stdio",
			Command:     "docker",
			Enabled:     true,
			Quarantined: false,
		}
	}

	patch := func(t *testing.T, existing *config.ServerConfig, body string) *config.ServerConfig {
		t.Helper()
		mockCtrl := &mockPatchServerController{apiKey: "test-key", existingServer: existing}
		srv := NewServer(mockCtrl, logger, nil)
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/slack", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		require.NotNil(t, mockCtrl.capturedUpdates, "UpdateServer should have been called")
		return mockCtrl.capturedUpdates
	}

	t.Run("explicit value sets the pointer", func(t *testing.T) {
		updates := patch(t, newServer(), `{"init_timeout":"120s"}`)
		require.NotNil(t, updates.InitTimeout, "pointer must be set when the request provides it")
		assert.Equal(t, 120*time.Second, updates.InitTimeout.Duration())
	})

	t.Run("omitting preserves nil existing", func(t *testing.T) {
		updates := patch(t, newServer(), `{"args":["new-arg"]}`)
		assert.Nil(t, updates.InitTimeout, "omitting the field must preserve the unset (nil) value")
	})

	t.Run("omitting preserves a prior value", func(t *testing.T) {
		existing := newServer()
		existing.InitTimeout = durPtr(120 * time.Second)
		updates := patch(t, existing, `{"args":["new-arg"]}`)
		require.NotNil(t, updates.InitTimeout, "omitting the field must preserve the existing pointer")
		assert.Equal(t, 120*time.Second, updates.InitTimeout.Duration())
	})
}

// TestHandleGetServers_ExposesInitTimeout verifies the GET /api/v1/servers
// payload surfaces init_timeout so a configured override round-trips (MCP-3322).
func TestHandleGetServers_ExposesInitTimeout(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		allServers: []map[string]interface{}{
			{
				"id":           "slack",
				"name":         "slack",
				"enabled":      true,
				"quarantined":  false,
				"init_timeout": "120s",
			},
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Data struct {
			Servers []struct {
				Name        string `json:"name"`
				InitTimeout string `json:"init_timeout"`
			} `json:"servers"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Servers, 1)
	assert.Equal(t, "2m0s", resp.Data.Servers[0].InitTimeout,
		"init_timeout must appear in the GET payload as a duration string")
}

// TestHandlePatchServer_URLMaskAwareWritePath is the #872 (Codex round)
// regression: unlike env/headers, `url` is a single string field with no
// field-level diff on the client. The read path masks secrets in the URL query
// string (redactServerSecrets → oauth.RedactURLQueryParams), so a client that
// edits a non-secret part of the URL and echoes the masked value back would
// otherwise persist the mask over the real credential. The write path must
// substitute the stored real secret back in.
func TestHandlePatchServer_URLMaskAwareWritePath(t *testing.T) {
	logger := zap.NewNop().Sugar()
	const storedURL = "https://api.example.com/mcp?apikey=REALSECRETKEY123&team=eng"

	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		existingServer: &config.ServerConfig{
			Name:     "synapbus",
			Protocol: "streamable-http",
			URL:      storedURL,
			Enabled:  true,
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	// What the client saw on the read path, then edited a non-secret part of.
	masked := oauth.RedactURLQueryParams(storedURL)
	require.NotContains(t, masked, "REALSECRETKEY123", "precondition: read path masked the secret")
	echoed := strings.Replace(masked, "team=eng", "team=ops", 1)

	body, _ := json.Marshal(map[string]any{"url": echoed})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/synapbus", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)

	got := mockCtrl.capturedUpdates.URL
	assert.Contains(t, got, "apikey=REALSECRETKEY123",
		"the real secret must survive — the echoed mask must not be persisted over it")
	assert.Contains(t, got, "team=ops", "the genuine non-secret edit must be preserved")
}

// TestHandlePatchServer_URLGenuineSecretEditPersists ensures the mask-aware
// guard does not clobber a real new secret the user actually typed.
func TestHandlePatchServer_URLGenuineSecretEditPersists(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		existingServer: &config.ServerConfig{
			Name:     "synapbus",
			Protocol: "streamable-http",
			URL:      "https://api.example.com/mcp?apikey=OLDSECRET12345",
			Enabled:  true,
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	const newURL = "https://api.example.com/mcp?apikey=BRANDNEWSECRET999"
	body, _ := json.Marshal(map[string]any{"url": newURL})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/synapbus", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)
	assert.Equal(t, newURL, mockCtrl.capturedUpdates.URL,
		"a genuinely edited secret must persist verbatim")
}

// TestHandlePatchServer_EnvMaskEchoRestored covers a non-tray client that
// echoes the FULL masked env map back (the tray diffs, but other clients may
// not). Any value equal to the masked rendering of the stored one must revert.
func TestHandlePatchServer_EnvMaskEchoRestored(t *testing.T) {
	logger := zap.NewNop().Sugar()
	stored := map[string]string{
		"API_KEY":   "live-secret-value",
		"LOG_LEVEL": "debug",
	}
	mockCtrl := &mockPatchServerController{
		apiKey: "test-key",
		existingServer: &config.ServerConfig{
			Name:     "demo",
			Protocol: "stdio",
			Command:  "uvx",
			Enabled:  true,
			Env:      stored,
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	// Client sends the masked API_KEY back verbatim while changing LOG_LEVEL.
	maskedEnv := oauth.RedactEnvValues(stored)
	rawBody, _ := json.Marshal(map[string]any{
		"env": map[string]any{
			"API_KEY":   maskedEnv["API_KEY"],
			"LOG_LEVEL": "info",
		},
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/demo", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.capturedUpdates)
	got := mockCtrl.capturedUpdates.Env
	assert.Equal(t, "live-secret-value", got["API_KEY"],
		"the echoed masked secret must be reverted to the stored real value")
	assert.Equal(t, "info", got["LOG_LEVEL"], "the genuine edit must persist")
}

// TestHandleConvertConfigToSecret_ServerNotFound verifies the 404 path
// for an unknown server name. Separate from the validation-errors table
// because it needs an empty server list, not a single populated entry.
func TestHandleConvertConfigToSecret_ServerNotFound(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchServerController{apiKey: "test-key", existingServer: nil}
	srv := NewServer(mockCtrl, logger, nil)

	body := []byte(`{"scope": "header", "key": "Authorization", "secret_name": "x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/missing/config-to-secret", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
	require.Contains(t, w.Body.String(), `missing`)
}
