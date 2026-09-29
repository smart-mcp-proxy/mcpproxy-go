package httpapi

// FR-009a rollout completion: the fake controller's ApplyConfig calls the
// real config.ValidateDetailed(), so these tests pin that both REST write
// doors now admit supported Profiles v3 policies and anonymous confinement.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	runtime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// profileGateController serves a minimal clean live config and routes
// ApplyConfig through the real config.ValidateDetailed (which runs
// ValidateProfiles, the FR-009a gate) instead of a canned result.
type profileGateController struct {
	baseController
	live    *config.Config
	applied int
}

func (m *profileGateController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: "test-key"}
}
func (m *profileGateController) GetConfig() (*config.Config, error) { return m.live, nil }
func (m *profileGateController) GetConfigPath() string              { return "/tmp/mcp_config.json" }
func (m *profileGateController) ApplyConfig(cfg *config.Config, _ string) (*runtime.ConfigApplyResult, error) {
	if errs := cfg.ValidateDetailed(); len(errs) > 0 {
		return &runtime.ConfigApplyResult{
			Success:          false,
			ValidationErrors: errs,
		}, fmt.Errorf("configuration validation failed: %s", errs[0].Message)
	}
	m.applied++
	return &runtime.ConfigApplyResult{Success: true, AppliedImmediately: true}, nil
}

func newProfileGateServer(t *testing.T) (*Server, *profileGateController) {
	t.Helper()
	// DefaultConfig(), not a bare &config.Config{}: ValidateDetailed also
	// enforces unrelated defaults (tools_limit, call_tool_timeout, ...), and a
	// zero-value config would fail those, masking the FR-009a assertions this
	// file is actually about.
	live := config.DefaultConfig()
	live.Listen = "127.0.0.1:8080"
	live.APIKey = "test-key"
	ctrl := &profileGateController{live: live}
	return NewServer(ctrl, zap.NewNop().Sugar(), nil), ctrl
}

func profileGateDo(t *testing.T, srv *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestPatchConfig_AcceptsV3PolicyField(t *testing.T) {
	srv, ctrl := newProfileGateServer(t)

	body := []byte(`{"profiles":[{"name":"prof","servers":[],"max_tier":"read"}]}`)
	w := profileGateDo(t, srv, http.MethodPatch, "/api/v1/config", body)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, ctrl.applied)
}

// TestPatchConfig_RejectsAnonymousProfileWhileGateClosed: PATCH with a bare
// anonymous_profile (no policy fields at all) is refused the same way.
func TestPatchConfig_AcceptsAnonymousProfile(t *testing.T) {
	srv, ctrl := newProfileGateServer(t)

	body := []byte(`{"anonymous_profile":"someone"}`)
	w := profileGateDo(t, srv, http.MethodPatch, "/api/v1/config", body)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, ctrl.applied)
}

// TestPatchConfig_LegacyProfileStillPasses preserves legacy profile support.
func TestPatchConfig_LegacyProfileStillPasses(t *testing.T) {
	srv, ctrl := newProfileGateServer(t)

	body := []byte(`{"profiles":[{"name":"legacy","servers":[],"title":"Legacy"}]}`)
	w := profileGateDo(t, srv, http.MethodPatch, "/api/v1/config", body)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, ctrl.applied)
}

func TestApplyConfig_AcceptsV3PolicyField(t *testing.T) {
	srv, ctrl := newProfileGateServer(t)

	doc := defaultConfigDocument(t)
	doc["profiles"] = []map[string]any{
		{"name": "prof", "servers": []string{}, "max_tier": "write"},
	}
	body, err := json.Marshal(doc)
	require.NoError(t, err)

	w := profileGateDo(t, srv, http.MethodPost, "/api/v1/config/apply", body)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, ctrl.applied)
}

// TestApplyConfig_RejectsAnonymousProfileWhileGateClosed: POST
// /api/v1/config/apply with a bare anonymous_profile is refused the same way.
func TestApplyConfig_AcceptsAnonymousProfile(t *testing.T) {
	srv, ctrl := newProfileGateServer(t)

	doc := defaultConfigDocument(t)
	doc["anonymous_profile"] = "someone"
	body, err := json.Marshal(doc)
	require.NoError(t, err)

	w := profileGateDo(t, srv, http.MethodPost, "/api/v1/config/apply", body)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, ctrl.applied)
}

// TestApplyConfig_LegacyProfileStillPasses is the control for the apply door.
// The document must otherwise be a fully valid config — ValidateDetailed also
// enforces unrelated defaults (tools_limit, ...) — so it starts from
// DefaultConfig() marshaled to a map rather than a handful of bare fields.
func TestApplyConfig_LegacyProfileStillPasses(t *testing.T) {
	srv, ctrl := newProfileGateServer(t)
	doc := defaultConfigDocument(t)
	doc["profiles"] = []map[string]any{
		{"name": "legacy", "servers": []string{}, "title": "Legacy"},
	}
	body, err := json.Marshal(doc)
	require.NoError(t, err)

	w := profileGateDo(t, srv, http.MethodPost, "/api/v1/config/apply", body)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, ctrl.applied)
}

// defaultConfigDocument marshals config.DefaultConfig() (with the api_key set
// so the apply door's own key round-trip is happy) into a generic map, so a
// test can layer one field of interest onto an otherwise-valid document
// without failing unrelated ValidateDetailed defaults.
func defaultConfigDocument(t *testing.T) map[string]any {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Listen = "127.0.0.1:8080"
	cfg.APIKey = "test-key"
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc
}
