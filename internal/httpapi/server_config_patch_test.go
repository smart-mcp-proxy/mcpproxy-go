package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	runtime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockPatchConfigController captures the *config.Config passed to ApplyConfig
// so PATCH /api/v1/config tests can assert the deep-merge result that reaches
// the apply pipeline (secret preservation, nested merge, change detection).
type mockPatchConfigController struct {
	baseController
	apiKey       string
	live         *config.Config
	captured     *config.Config
	changedField string
	// validationErrs, when non-empty, is returned from ApplyConfig so a test
	// can assert that invalid values surface as validation errors rather than
	// corrupting config.
	validationErrs []config.ValidationError
}

func (m *mockPatchConfigController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey}
}

func (m *mockPatchConfigController) GetConfig() (*config.Config, error) {
	return m.live, nil
}

func (m *mockPatchConfigController) GetConfigPath() string { return "/tmp/mcp_config.json" }

func (m *mockPatchConfigController) ApplyConfig(cfg *config.Config, _ string) (*runtime.ConfigApplyResult, error) {
	clone := *cfg
	m.captured = &clone
	if len(m.validationErrs) > 0 {
		return &runtime.ConfigApplyResult{
			Success:          false,
			ValidationErrors: m.validationErrs,
		}, nil
	}
	changed := []string{}
	if m.changedField != "" {
		changed = []string{m.changedField}
	}
	return &runtime.ConfigApplyResult{
		Success:            true,
		AppliedImmediately: true,
		ChangedFields:      changed,
	}, nil
}

func boolPtrPatch(b bool) *bool { return &b }

func decodePatchResult(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var envelope map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &envelope))
	// writeSuccess wraps the payload; tolerate either a bare result or a
	// {success, data} envelope.
	if data, ok := envelope["data"].(map[string]interface{}); ok {
		return data
	}
	return envelope
}

// TestHandlePatchConfig_SecretPreservation is the core invariant: patching a
// single non-secret field must leave a pre-set api_key untouched. The PATCH
// handler reads the REAL in-memory config (secrets intact), deep-merges only
// the client-sent keys, and pushes the result through ApplyConfig — so the
// api_key, which was never in the patch body, must survive verbatim.
func TestHandlePatchConfig_SecretPreservation(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchConfigController{
		apiKey: "test-key",
		live: &config.Config{
			APIKey:            "super-secret-api-key",
			QuarantineEnabled: boolPtrPatch(true),
		},
		changedField: "quarantine_enabled",
	}
	srv := NewServer(mockCtrl, logger, nil)

	body, _ := json.Marshal(map[string]any{"quarantine_enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.captured, "ApplyConfig should have been called")

	assert.Equal(t, "super-secret-api-key", mockCtrl.captured.APIKey,
		"api_key must be preserved verbatim — it was never in the PATCH body")
	require.NotNil(t, mockCtrl.captured.QuarantineEnabled)
	assert.False(t, *mockCtrl.captured.QuarantineEnabled,
		"quarantine_enabled must reflect the PATCH body (false)")
}

// TestHandlePatchConfig_NestedMerge verifies that patching one field inside a
// nested object flips it while sibling fields set on the live config survive.
func TestHandlePatchConfig_NestedMerge(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchConfigController{
		apiKey: "test-key",
		live: &config.Config{
			APIKey: "super-secret-api-key",
			DockerIsolation: &config.DockerIsolationConfig{
				Enabled:     false,
				MemoryLimit: "512m",
			},
		},
		changedField: "docker_isolation",
	}
	srv := NewServer(mockCtrl, logger, nil)

	body := []byte(`{"docker_isolation":{"enabled":true}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, mockCtrl.captured)
	require.NotNil(t, mockCtrl.captured.DockerIsolation)

	assert.True(t, mockCtrl.captured.DockerIsolation.Enabled,
		"docker_isolation.enabled must be flipped by the PATCH")
	assert.Equal(t, "512m", mockCtrl.captured.DockerIsolation.MemoryLimit,
		"sibling docker_isolation.memory_limit must be preserved")
	assert.Equal(t, "super-secret-api-key", mockCtrl.captured.APIKey,
		"api_key must be preserved")
}

// TestHandlePatchConfig_ChangedFields verifies the patched key surfaces in the
// response's changed_fields.
func TestHandlePatchConfig_ChangedFields(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchConfigController{
		apiKey:       "test-key",
		live:         &config.Config{QuarantineEnabled: boolPtrPatch(true)},
		changedField: "quarantine_enabled",
	}
	srv := NewServer(mockCtrl, logger, nil)

	body := []byte(`{"quarantine_enabled":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	result := decodePatchResult(t, w.Body.Bytes())
	changed, _ := result["changed_fields"].([]interface{})
	require.NotEmpty(t, changed, "changed_fields must be populated, got %v", result)
	assert.Contains(t, changed, "quarantine_enabled")
}

// TestHandlePatchConfig_EmptyBody rejects an empty object with 400.
func TestHandlePatchConfig_EmptyBody(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchConfigController{apiKey: "test-key", live: &config.Config{}}
	srv := NewServer(mockCtrl, logger, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Nil(t, mockCtrl.captured, "empty body must not reach ApplyConfig")
}

// TestHandlePatchConfig_MalformedJSON rejects invalid JSON with 400.
func TestHandlePatchConfig_MalformedJSON(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchConfigController{apiKey: "test-key", live: &config.Config{}}
	srv := NewServer(mockCtrl, logger, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewReader([]byte(`{not json`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Nil(t, mockCtrl.captured)
}

// TestHandlePatchConfig_ValidationErrors verifies that an invalid value
// surfaces as validation errors in the response rather than corrupting config.
func TestHandlePatchConfig_ValidationErrors(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockPatchConfigController{
		apiKey: "test-key",
		live:   &config.Config{APIKey: "super-secret-api-key"},
		validationErrs: []config.ValidationError{
			{Field: "listen", Message: "invalid address"},
		},
	}
	srv := NewServer(mockCtrl, logger, nil)

	body := []byte(`{"listen":"not a valid addr"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	result := decodePatchResult(t, w.Body.Bytes())
	assert.Equal(t, false, result["success"], "success must be false on validation error")
	verrs, _ := result["validation_errors"].([]interface{})
	require.NotEmpty(t, verrs, "validation_errors must be populated, got %v", result)
}

// TestDeepMergeJSON unit-tests the recursive merge helper directly.
func TestDeepMergeJSON(t *testing.T) {
	base := map[string]interface{}{
		"api_key": "secret",
		"docker_isolation": map[string]interface{}{
			"enabled":      false,
			"memory_limit": "512m",
		},
		"servers": []interface{}{"a", "b"},
		"scalar":  1,
	}
	patch := map[string]interface{}{
		"docker_isolation": map[string]interface{}{
			"enabled": true,
		},
		"servers": []interface{}{"c"}, // arrays replace wholesale
		"scalar":  2,
		"new_key": "x",
	}

	deepMergeJSON(base, patch)

	// Untouched key preserved.
	assert.Equal(t, "secret", base["api_key"])
	// Nested merge: enabled flipped, sibling preserved.
	di := base["docker_isolation"].(map[string]interface{})
	assert.Equal(t, true, di["enabled"])
	assert.Equal(t, "512m", di["memory_limit"])
	// Array replaced wholesale.
	assert.Equal(t, []interface{}{"c"}, base["servers"])
	// Scalar overwritten.
	assert.Equal(t, 2, base["scalar"])
	// New key added.
	assert.Equal(t, "x", base["new_key"])
}
