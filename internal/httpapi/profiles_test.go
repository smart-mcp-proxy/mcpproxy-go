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

// mockProfilesController serves a fixed profiles config + per-server tool counts.
type mockProfilesController struct {
	baseController
	apiKey string
	cfg    *config.Config
}

func (m *mockProfilesController) GetCurrentConfig() any { return &config.Config{APIKey: m.apiKey} }

func (m *mockProfilesController) GetConfig() (*config.Config, error) { return m.cfg, nil }

func (m *mockProfilesController) GetAllServers() ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{"name": "research-srv", "tool_count": 3},
		{"name": "deploy-srv", "tool_count": 2},
	}, nil
}

func newProfilesTestServer() *Server {
	cfg := &config.Config{
		APIKey: "test-key",
		Servers: []*config.ServerConfig{
			{Name: "research-srv"},
			{Name: "deploy-srv"},
		},
		Profiles: []config.ProfileConfig{
			{Name: "research", Servers: []string{"research-srv"}},
			{Name: "deploy", Servers: []string{"deploy-srv"}},
		},
	}
	ctrl := &mockProfilesController{apiKey: "test-key", cfg: cfg}
	return NewServer(ctrl, zap.NewNop().Sugar(), nil)
}

func doJSON(t *testing.T, srv *Server, method, path string, body []byte) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	return w, resp
}

func TestHandleListProfiles(t *testing.T) {
	srv := newProfilesTestServer()
	w, resp := doJSON(t, srv, http.MethodGet, "/api/v1/profiles", nil)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, true, resp["success"])
	data, _ := resp["data"].(map[string]interface{})
	require.NotNil(t, data)
	profiles, _ := data["profiles"].([]interface{})
	require.Len(t, profiles, 2)

	byName := map[string]map[string]interface{}{}
	for _, p := range profiles {
		pm, _ := p.(map[string]interface{})
		byName[pm["name"].(string)] = pm
	}

	research := byName["research"]
	require.NotNil(t, research)
	assert.EqualValues(t, 3, research["tool_count"])
	servers, _ := research["servers"].([]interface{})
	require.Len(t, servers, 1)
	assert.Equal(t, "research-srv", servers[0])

	assert.EqualValues(t, 2, byName["deploy"]["tool_count"])
}

func TestHandleActiveProfileRoundTrip(t *testing.T) {
	srv := newProfilesTestServer()

	// Default is empty (all servers).
	w, resp := doJSON(t, srv, http.MethodGet, "/api/v1/profiles/active", nil)
	require.Equal(t, http.StatusOK, w.Code)
	data, _ := resp["data"].(map[string]interface{})
	assert.Equal(t, "", data["active_profile"])

	// Set to a valid profile.
	w, resp = doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`{"profile":"research"}`))
	require.Equal(t, http.StatusOK, w.Code, "body: %v", resp)
	data, _ = resp["data"].(map[string]interface{})
	assert.Equal(t, "research", data["active_profile"])

	// Read it back.
	_, resp = doJSON(t, srv, http.MethodGet, "/api/v1/profiles/active", nil)
	data, _ = resp["data"].(map[string]interface{})
	assert.Equal(t, "research", data["active_profile"])

	// Clear it.
	w, resp = doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`{"profile":""}`))
	require.Equal(t, http.StatusOK, w.Code)
	data, _ = resp["data"].(map[string]interface{})
	assert.Equal(t, "", data["active_profile"])
}

func TestHandleSetActiveProfileUnknown(t *testing.T) {
	srv := newProfilesTestServer()
	w, resp := doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`{"profile":"ghost"}`))
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, resp["error"], "unknown profile 'ghost'")
}

func TestHandleSetActiveProfileBadBody(t *testing.T) {
	srv := newProfilesTestServer()
	w, _ := doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`not json`))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// profileEventRecorder records EmitActiveProfileChanged calls so we can assert
// the handler notifies other clients (via SSE) only on an actual change.
type profileEventRecorder struct {
	mockProfilesController
	emitted []string
}

func (m *profileEventRecorder) EmitActiveProfileChanged(profile string) {
	m.emitted = append(m.emitted, profile)
}

func TestSetActiveProfileEmitsChangeEvent(t *testing.T) {
	cfg := &config.Config{
		APIKey: "test-key",
		Servers: []*config.ServerConfig{
			{Name: "research-srv"},
			{Name: "deploy-srv"},
		},
		Profiles: []config.ProfileConfig{
			{Name: "research", Servers: []string{"research-srv"}},
			{Name: "deploy", Servers: []string{"deploy-srv"}},
		},
	}
	rec := &profileEventRecorder{mockProfilesController: mockProfilesController{apiKey: "test-key", cfg: cfg}}
	srv := NewServer(rec, zap.NewNop().Sugar(), nil)

	// Setting a new profile emits one change event.
	w, _ := doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`{"profile":"research"}`))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"research"}, rec.emitted)

	// Setting the same profile again is a no-op — no duplicate event.
	w, _ = doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`{"profile":"research"}`))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"research"}, rec.emitted)

	// Clearing emits an empty-string change event.
	w, _ = doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`{"profile":""}`))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"research", ""}, rec.emitted)

	// A rejected (unknown) profile emits nothing.
	w, _ = doJSON(t, srv, http.MethodPut, "/api/v1/profiles/active", []byte(`{"profile":"ghost"}`))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, []string{"research", ""}, rec.emitted)
}
