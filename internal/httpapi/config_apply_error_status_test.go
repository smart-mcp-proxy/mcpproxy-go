package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	runtime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// #1084: PATCH /api/v1/config answered 500 for a value the config validator
// rejected — e.g. {"direct_tool_response_mode":"bogus"}. The behaviour was safe
// (the file was left untouched) but the status class was wrong, and the status
// class is what tells a client whether retrying could ever help.
type applyErrController struct {
	baseController
	result *runtime.ConfigApplyResult
	err    error
}

func (m *applyErrController) GetCurrentConfig() any { return &config.Config{APIKey: "k"} }
func (m *applyErrController) GetConfig() (*config.Config, error) {
	return &config.Config{Listen: "127.0.0.1:8080"}, nil
}
func (m *applyErrController) GetConfigPath() string { return "/tmp/mcp_config.json" }
func (m *applyErrController) ApplyConfig(*config.Config, string) (*runtime.ConfigApplyResult, error) {
	return m.result, m.err
}

func patchConfigWith(t *testing.T, ctrl *applyErrController, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "k")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// A rejected value is the operator's mistake: 400, with the offending field
// named in a structured payload rather than only inside the prose message.
func TestPatchConfig_RejectedValueIs400WithValidationErrors(t *testing.T) {
	w := patchConfigWith(t, &applyErrController{
		result: &runtime.ConfigApplyResult{
			Success: false,
			ValidationErrors: []config.ValidationError{
				{Field: "direct_tool_response_mode", Message: `invalid direct tool response mode: bogus (must be "full" or "deferred")`},
			},
		},
		err: errors.New(`configuration validation failed: direct_tool_response_mode: invalid direct tool response mode: bogus`),
	}, map[string]any{"direct_tool_response_mode": "bogus"})

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	var envelope map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assert.Equal(t, false, envelope["success"])

	data, ok := envelope["data"].(map[string]interface{})
	require.True(t, ok, "the 400 must carry a structured payload, not just prose: %s", w.Body.String())
	errs, ok := data["validation_errors"].([]interface{})
	require.True(t, ok, "validation_errors must be present")
	require.Len(t, errs, 1)

	first, ok := errs[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "direct_tool_response_mode", first["field"],
		"a client must be able to point at the offending field without parsing the message")
}

// A failure with no validation errors is a genuine server fault and must stay
// in the 500 class — otherwise the fix would mislabel real outages as bad input.
func TestPatchConfig_PersistFailureStays500(t *testing.T) {
	w := patchConfigWith(t, &applyErrController{
		result: &runtime.ConfigApplyResult{Success: false},
		err:    errors.New("failed to write config file: disk full"),
	}, map[string]any{"tools_limit": 20})

	require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "validation_errors")
}

// A nil result alongside an error must not panic the classifier.
func TestPatchConfig_NilResultWithErrorStays500(t *testing.T) {
	w := patchConfigWith(t, &applyErrController{
		result: nil,
		err:    errors.New("controller exploded"),
	}, map[string]any{"tools_limit": 20})

	require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
}
