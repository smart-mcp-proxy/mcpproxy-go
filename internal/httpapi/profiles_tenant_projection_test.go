package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// T082 (Spec 107 US4): tenant projection of GET /profiles and
// GET /profiles/active (rest-endpoints.md §"profiles"; T085 implements this
// against profiles.go:61-85,104-108).
//
// Contract: "omit every profile whose effective server set ∩ entitlement is
// empty; GET /profiles/active → "" when the global active profile would be
// omitted". Today handleListProfiles narrows the per-profile `servers` array
// through canSeeServer (#1166) but never omits the profile itself, so a
// tenant whose entitlement excludes every server backing a profile still sees
// that profile with an empty `servers` array and a zero `tool_count` — a
// count oracle for a server the tenant may not otherwise enumerate. These
// tests are behaviour-red until T085 lands, and this whole package fails to
// compile until T083 lands SetSessionPrincipalResolver (referenced by the
// sibling sse_session_refresh_test.go in this package).

// newTenantProfilesTestServer builds a Server with two profiles: "research"
// (backed solely by research-srv) and "deploy" (backed solely by
// deploy-srv), so a tenant entitled to only one server has exactly one
// profile it may see.
func newTenantProfilesTestServer() *Server {
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

// carolSessionContext is Carol: a session principal (cookie/JWT, never an
// agent token) entitled to research-srv only — the "deploy" profile is
// hidden-only for her.
func carolSessionContext() *auth.AuthContext {
	return &auth.AuthContext{
		Type:           auth.AuthTypeUser,
		UserID:         "carol",
		Email:          "carol@example.com",
		DisplayName:    "Carol",
		Provider:       "google",
		AllowedServers: []string{"research-srv"},
		CredentialKind: auth.CredentialKindCookie,
	}
}

func doProfilesJSONAs(t *testing.T, srv *Server, ac *auth.AuthContext, method, path string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	if ac != nil {
		r = r.WithContext(auth.WithAuthContext(context.Background(), ac))
	}
	w := httptest.NewRecorder()
	switch path {
	case "/api/v1/profiles":
		srv.handleListProfiles(w, r)
	case "/api/v1/profiles/active":
		srv.handleGetActiveProfile(w, r)
	default:
		t.Fatalf("unhandled path %q", path)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	return w, resp
}

func TestTenantProfiles_OmitsHiddenOnlyProfile(t *testing.T) {
	srv := newTenantProfilesTestServer()

	w, resp := doProfilesJSONAs(t, srv, carolSessionContext(), http.MethodGet, "/api/v1/profiles")
	require.Equal(t, http.StatusOK, w.Code)

	data, _ := resp["data"].(map[string]interface{})
	require.NotNil(t, data)
	profiles, _ := data["profiles"].([]interface{})

	// Carol is entitled to research-srv only: the "deploy" profile's effective
	// server set (deploy-srv) has empty intersection with her entitlement and
	// must be OMITTED entirely, not merely narrowed to an empty servers array.
	require.Len(t, profiles, 1, "deploy must be omitted for Carol, not merely narrowed: %#v", profiles)

	pm, _ := profiles[0].(map[string]interface{})
	assert.Equal(t, "research", pm["name"])
}

// TestTenantProfiles_OmitsAlreadyEmptyProfile pins cross-review round 2,
// chunk 3's P3 finding: handleListProfiles only omitted a profile when
// scoping NARROWED a non-empty effective server set down to empty
// (len(scoped) == 0 && len(eff) > 0). A profile whose effective server set
// was ALREADY empty before scoping — e.g. it names only a server that no
// longer exists in the admin configuration, or an operator misconfigured it
// with no servers at all — fell through that guard and was still shown to a
// tenant, with `servers: []` and `tool_count: 0`, even though its
// intersection with her entitlement is trivially empty. FR-002 requires
// omitting every profile whose effective-set ∩ entitlement intersection is
// empty, with no carve-out for "the effective set was already empty".
func TestTenantProfiles_OmitsAlreadyEmptyProfile(t *testing.T) {
	cfg := &config.Config{
		APIKey: "test-key",
		Servers: []*config.ServerConfig{
			{Name: "research-srv"},
			{Name: "deploy-srv"},
		},
		Profiles: []config.ProfileConfig{
			{Name: "research", Servers: []string{"research-srv"}},
			// "retired" names only a server absent from the admin
			// configuration, so EffectiveServers returns [] BEFORE scoping —
			// the case the len(eff) > 0 guard let slip through.
			{Name: "retired", Servers: []string{"gone-srv"}},
		},
	}
	ctrl := &mockProfilesController{apiKey: "test-key", cfg: cfg}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	w, resp := doProfilesJSONAs(t, srv, carolSessionContext(), http.MethodGet, "/api/v1/profiles")
	require.Equal(t, http.StatusOK, w.Code)

	data, _ := resp["data"].(map[string]interface{})
	require.NotNil(t, data)
	profiles, _ := data["profiles"].([]interface{})

	require.Len(t, profiles, 1, "retired must be omitted for Carol (its effective set was already "+
		"empty, which still intersects her entitlement in nothing): %#v", profiles)
	pm, _ := profiles[0].(map[string]interface{})
	assert.Equal(t, "research", pm["name"])
}

func TestTenantProfiles_ActiveProfileHiddenReadsEmpty(t *testing.T) {
	srv := newTenantProfilesTestServer()

	// The global default active profile is "deploy" — a profile Carol is not
	// entitled to see at all.
	srv.activeProfileMu.Lock()
	srv.activeProfile = "deploy"
	srv.activeProfileMu.Unlock()

	w, resp := doProfilesJSONAs(t, srv, carolSessionContext(), http.MethodGet, "/api/v1/profiles/active")
	require.Equal(t, http.StatusOK, w.Code)
	data, _ := resp["data"].(map[string]interface{})
	assert.Equal(t, "", data["active_profile"],
		"a hidden active profile must read back as empty for a tenant, never the real slug")
}

func TestTenantProfiles_CarolSeesEntitledActiveProfile(t *testing.T) {
	srv := newTenantProfilesTestServer()

	srv.activeProfileMu.Lock()
	srv.activeProfile = "research"
	srv.activeProfileMu.Unlock()

	w, resp := doProfilesJSONAs(t, srv, carolSessionContext(), http.MethodGet, "/api/v1/profiles/active")
	require.Equal(t, http.StatusOK, w.Code)
	data, _ := resp["data"].(map[string]interface{})
	assert.Equal(t, "research", data["active_profile"],
		"Carol is entitled to research-srv, so the research profile must remain visible")
}

func TestTenantProfiles_AdminSeesBothProfiles(t *testing.T) {
	srv := newTenantProfilesTestServer()

	w, resp := doProfilesJSONAs(t, srv, auth.AdminContext(), http.MethodGet, "/api/v1/profiles")
	require.Equal(t, http.StatusOK, w.Code)
	data, _ := resp["data"].(map[string]interface{})
	profiles, _ := data["profiles"].([]interface{})
	assert.Len(t, profiles, 2, "an administrator's projection must stay byte-for-byte unchanged (SC-006)")
}

// TestTenantProfiles_AgentTokenBehaviourUnchanged pins the pre-existing #1166
// per-server narrowing for agent tokens: an agent token scoped to
// research-srv keeps seeing BOTH profiles (the whole-profile omission this
// change adds is session-principal-only), with "deploy"'s servers array
// narrowed to empty exactly as it is today.
func TestTenantProfiles_AgentTokenBehaviourUnchanged(t *testing.T) {
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
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AllowedServers: []string{"research-srv"},
		CredentialKind: auth.CredentialKindAgentToken,
	}

	w, resp := doProfilesJSONAs(t, srv, agentCtx, http.MethodGet, "/api/v1/profiles")
	require.Equal(t, http.StatusOK, w.Code)
	data, _ := resp["data"].(map[string]interface{})
	profiles, _ := data["profiles"].([]interface{})
	require.Len(t, profiles, 2, "agent-token behaviour (#1166 per-server narrowing) must not change")

	byName := map[string]map[string]interface{}{}
	for _, p := range profiles {
		pm, _ := p.(map[string]interface{})
		byName[pm["name"].(string)] = pm
	}
	deployServers, _ := byName["deploy"]["servers"].([]interface{})
	assert.Empty(t, deployServers, "deploy-srv stays narrowed out of the servers array, as today")
}
