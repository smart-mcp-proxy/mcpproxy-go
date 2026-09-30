package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-f T074a: the handlers of the /profiles routes, driven against a fake
// profiles service so the HTTP layer's own logic is what is under test: the
// administrator gates, the non-administrator omission and redaction (FR-034) and
// the error mapping. The service's own behaviour is pinned in internal/runtime,
// and the whole stack in internal/server (profile_v3_rest_test.go).

// fakeProfiles is an in-memory ProfilesAPI over a config. Every view carries a
// used_by, so a test proves the HANDLER withholds it from a non-administrator
// even if a service slipped it through.
type fakeProfiles struct {
	cfg       *config.Config
	err       error
	calls     []string
	actor     internalRuntime.Actor
	lastDraft config.ProfileConfig
}

func (f *fakeProfiles) view(p *config.ProfileConfig) internalRuntime.ProfileView {
	v := internalRuntime.ProfileViewFromConfig(f.cfg, p)
	v.UsedBy = &internalRuntime.UsedBy{
		Clients:          []internalRuntime.UsedByClient{{ID: "cursor", Mode: "locked"}},
		Tokens:           []string{"ro-bot"},
		AnonymousProfile: f.cfg.AnonymousProfile == p.Name,
	}
	return v
}

func (f *fakeProfiles) List(_ context.Context, _ internalRuntime.ViewerScope) (*internalRuntime.ProfileList, error) {
	f.calls = append(f.calls, "list")
	if f.err != nil {
		return nil, f.err
	}
	out := &internalRuntime.ProfileList{AnonymousProfile: f.cfg.AnonymousProfile}
	for i := range f.cfg.Profiles {
		out.Profiles = append(out.Profiles, f.view(&f.cfg.Profiles[i]))
	}
	return out, nil
}

func (f *fakeProfiles) Get(_ context.Context, name string, _ internalRuntime.ViewerScope) (*internalRuntime.ProfileView, error) {
	f.calls = append(f.calls, "get:"+name)
	if f.err != nil {
		return nil, f.err
	}
	for i := range f.cfg.Profiles {
		if f.cfg.Profiles[i].Name == name {
			v := f.view(&f.cfg.Profiles[i])
			return &v, nil
		}
	}
	return nil, &internalRuntime.ProfileNotFoundError{Name: name}
}

func (f *fakeProfiles) Create(_ context.Context, a internalRuntime.Actor, p config.ProfileConfig) (*internalRuntime.WriteResult, error) {
	f.calls, f.actor, f.lastDraft = append(f.calls, "create:"+p.Name), a, p
	if f.err != nil {
		return nil, f.err
	}
	return &internalRuntime.WriteResult{Profile: internalRuntime.ProfileViewFromConfig(f.cfg, &p), Warnings: []string{"a warning"}}, nil
}

func (f *fakeProfiles) Update(_ context.Context, a internalRuntime.Actor, name string, p config.ProfileConfig) (*internalRuntime.WriteResult, error) {
	f.calls, f.actor, f.lastDraft = append(f.calls, "update:"+name), a, p
	if f.err != nil {
		return nil, f.err
	}
	return &internalRuntime.WriteResult{Profile: internalRuntime.ProfileViewFromConfig(f.cfg, &p), Warnings: []string{}}, nil
}

func (f *fakeProfiles) Rename(_ context.Context, _ internalRuntime.Actor, name, newName string) (*internalRuntime.RenameResult, error) {
	f.calls = append(f.calls, "rename:"+name+">"+newName)
	if f.err != nil {
		return nil, f.err
	}
	p := config.ProfileConfig{Name: newName}
	return &internalRuntime.RenameResult{Profile: internalRuntime.ProfileViewFromConfig(f.cfg, &p), Moved: internalRuntime.MovedRefs{Clients: []string{"cursor"}, Tokens: []string{}}}, nil
}

func (f *fakeProfiles) Delete(_ context.Context, _ internalRuntime.Actor, name, reassignTo string, force bool) (*internalRuntime.DeleteResult, error) {
	f.calls = append(f.calls, "delete:"+name+":"+reassignTo+":"+boolStr(force))
	if f.err != nil {
		return nil, f.err
	}
	return &internalRuntime.DeleteResult{Deleted: name, Moved: internalRuntime.MovedRefs{Clients: []string{}, Tokens: []string{}}}, nil
}

func (f *fakeProfiles) Try(_ context.Context, draft config.ProfileConfig, query string, limit int) (*internalRuntime.TryResult, error) {
	f.calls = append(f.calls, "try:"+query)
	f.lastDraft = draft
	if f.err != nil {
		return nil, f.err
	}
	return &internalRuntime.TryResult{Results: []map[string]interface{}{}, Hidden: []internalRuntime.TryHidden{}}, nil
}

func (f *fakeProfiles) EffectiveTools(_ context.Context, name string, opt internalRuntime.EffectiveToolsOptions) (*internalRuntime.EffectiveToolsResult, error) {
	f.calls = append(f.calls, "effective:"+name+":"+opt.Client+":"+opt.Reason)
	if f.err != nil {
		return nil, f.err
	}
	return &internalRuntime.EffectiveToolsResult{Profile: name, Tools: []internalRuntime.EffectiveTool{}}, nil
}

func (f *fakeProfiles) Explain(_ context.Context, subject profile.AccessSubject, tool string) (*internalRuntime.AccessExplanation, error) {
	f.calls = append(f.calls, "explain:"+string(subject.Kind)+":"+tool)
	if f.err != nil {
		return nil, f.err
	}
	return &internalRuntime.AccessExplanation{
		Subject: internalRuntime.ExplainSubjectView{Kind: subject.Kind}, Tool: tool,
		Steps: []internalRuntime.ExplainStepView{}, Verdict: profile.ExplainVerdictAllowed, Fixes: []internalRuntime.Fix{},
	}, nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// profilesFixtureConfig: alpha is what the scoped token may see. "research" is
// reachable (alpha) but its rules and switchable_to also name beta things;
// "deploy" lives entirely on beta.
func profilesFixtureConfig() *config.Config {
	cfg := scopeFixtureConfig(false)
	switchTo := []string{"deploy", "shared"}
	cfg.AnonymousProfile = "research"
	cfg.Profiles = []config.ProfileConfig{
		{
			Name: "research", Servers: []string{"alpha", "beta"}, MaxTier: "read",
			Tools: &config.ProfileToolRules{
				Allow:    []string{"alpha:list", "beta:secret_tool", "*:ping"},
				Deny:     []string{"beta:*"},
				Classify: map[string]string{"alpha:search": "read", "beta:hidden_admin": "write"},
			},
			SwitchableTo: &switchTo,
		},
		{Name: "deploy", Servers: []string{"beta"}},
		{Name: "shared", Servers: []string{"alpha"}},
	}
	return cfg
}

type profilesRig struct {
	t      *testing.T
	admin  *Server
	scoped *Server
	token  string
	fake   *fakeProfiles
}

func newProfilesRig(t *testing.T) *profilesRig {
	t.Helper()
	cfg := profilesFixtureConfig()
	ctrl := &scopeController{cfg: cfg, servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	fake := &fakeProfiles{cfg: cfg}
	srv.SetProfilesService(fake)
	return &profilesRig{t: t, admin: srv, scoped: srv, token: token, fake: fake}
}

func (g *profilesRig) do(key, method, path, body string) *httptest.ResponseRecorder {
	g.t.Helper()
	var rdr *bytes.Reader
	if body == "" {
		rdr = bytes.NewReader(nil)
	} else {
		rdr = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	rec := httptest.NewRecorder()
	g.admin.ServeHTTP(rec, req)
	return rec
}

func decodeProfilesData(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	data, _ := resp["data"].(map[string]interface{})
	return data
}

func TestProfilesCrud_AdminShapes(t *testing.T) {
	g := newProfilesRig(t)

	// Create: 201 with the profile and the validator's warnings.
	rec := g.do(scopeAdminAPIKey, http.MethodPost, "/api/v1/profiles", `{"name":"tmp","servers":["alpha"],"max_tier":"read"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data := decodeProfilesData(t, rec)
	assert.Equal(t, "tmp", data["profile"].(map[string]interface{})["name"])
	assert.Equal(t, []interface{}{"a warning"}, data["warnings"])
	assert.Equal(t, "tmp", g.fake.lastDraft.Name)
	assert.Equal(t, "read", g.fake.lastDraft.MaxTier)

	// Update: a body without a name takes the path's.
	rec = g.do(scopeAdminAPIKey, http.MethodPut, "/api/v1/profiles/tmp", `{"servers":["alpha"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "tmp", g.fake.lastDraft.Name)

	// Rename.
	rec = g.do(scopeAdminAPIKey, http.MethodPost, "/api/v1/profiles/tmp/rename", `{"new_name":"tmp2"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []interface{}{"cursor"}, decodeProfilesData(t, rec)["moved"].(map[string]interface{})["clients"])
	rec = g.do(scopeAdminAPIKey, http.MethodPost, "/api/v1/profiles/tmp/rename", `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"field":"new_name"`)

	// Delete: reassign_to and force reach the service.
	rec = g.do(scopeAdminAPIKey, http.MethodDelete, "/api/v1/profiles/tmp?reassign_to=shared&force=true", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "tmp", decodeProfilesData(t, rec)["deleted"])
	assert.Contains(t, g.fake.calls, "delete:tmp:shared:true")
	rec = g.do(scopeAdminAPIKey, http.MethodDelete, "/api/v1/profiles/tmp?force=maybe", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Try needs a query; an unknown body field is a 400, never ignored.
	rec = g.do(scopeAdminAPIKey, http.MethodPost, "/api/v1/profiles/try", `{"profile":{"servers":["alpha"]},"query":"issue"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = g.do(scopeAdminAPIKey, http.MethodPost, "/api/v1/profiles/try", `{"profile":{"servers":["alpha"]}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = g.do(scopeAdminAPIKey, http.MethodPost, "/api/v1/profiles", `{"name":"x","max_teir":"read"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = g.do(scopeAdminAPIKey, http.MethodPost, "/api/v1/profiles", ``)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestProfilesCrud_ErrorMapping(t *testing.T) {
	guard := &internalRuntime.BindingGuardError{
		Bindings: []internalRuntime.BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "research", Mode: "locked"}},
		Fixes:    []internalRuntime.GuardFix{{Kind: profile.GuardFixRequireMCPAuth}},
	}
	cases := []struct {
		name   string
		err    error
		status int
		code   string
		field  string
	}{
		{"validation", &internalRuntime.ValidationError{Field: "max_tier", Message: `invalid max_tier "x"`}, 400, "", "max_tier"},
		{"not found", &internalRuntime.ProfileNotFoundError{Name: "x"}, 404, "", ""},
		{"unknown profile sentinel", profile.ErrUnknownProfile, 404, "", ""},
		{"exists", &internalRuntime.ProfileExistsError{Name: "x"}, 409, "profile_exists", ""},
		{"mismatch", &internalRuntime.NameMismatchError{Path: "a", Body: "b"}, 409, "name_mismatch", ""},
		{"in use", &internalRuntime.ProfileInUseError{UsedBy: internalRuntime.UsedBy{Clients: []internalRuntime.UsedByClient{{ID: "cursor", Mode: "locked"}}}}, 409, "profile_in_use", ""},
		{"anonymous", &internalRuntime.ProfileIsAnonymousError{UsedBy: internalRuntime.UsedBy{AnonymousProfile: true}}, 409, "profile_is_anonymous_profile", ""},
		{"guard", guard, 409, "binding_bypassable_without_auth", ""},
		{"evaluator", internalRuntime.ErrEvaluatorUnavailable, 503, "", ""},
		{"boom", errors.New("storage exploded"), 500, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newProfilesRig(t)
			g.fake.err = tc.err
			rec := g.do(scopeAdminAPIKey, http.MethodDelete, "/api/v1/profiles/research", "")
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			var body map[string]interface{}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, false, body["success"])
			if tc.code != "" {
				assert.Equal(t, tc.code, body["code"])
			}
			if tc.field != "" {
				assert.Equal(t, tc.field, body["field"])
			}
			if tc.status == 500 {
				assert.NotContains(t, rec.Body.String(), "storage exploded", "an internal error never echoes its text")
			}
			if tc.name == "in use" {
				used, _ := body["used_by"].(map[string]interface{})
				require.NotNil(t, used)
				assert.NotEmpty(t, used["clients"])
			}
		})
	}
	// The messages are the contract's.
	g := newProfilesRig(t)
	g.fake.err = &internalRuntime.ProfileExistsError{Name: "tmp"}
	assert.Contains(t, g.do(scopeAdminAPIKey, http.MethodPost, "/api/v1/profiles", `{"name":"tmp"}`).Body.String(), `profile \"tmp\" already exists`)
	g.fake.err = &internalRuntime.NameMismatchError{}
	assert.Contains(t, g.do(scopeAdminAPIKey, http.MethodPut, "/api/v1/profiles/a", `{"name":"b"}`).Body.String(), "name must equal the path; use POST /profiles/{name}/rename")
	g.fake.err = &internalRuntime.ProfileInUseError{}
	assert.Contains(t, g.do(scopeAdminAPIKey, http.MethodDelete, "/api/v1/profiles/a", "").Body.String(), `"error":"profile in use"`)
	g.fake.err = &internalRuntime.ProfileIsAnonymousError{}
	assert.Contains(t, g.do(scopeAdminAPIKey, http.MethodDelete, "/api/v1/profiles/a?force=true", "").Body.String(),
		"profile is the anonymous_profile; pass reassign_to or change anonymous_profile first")
}

// FR-034 differential: a scoped token (and a server-edition session principal)
// gets an unreachable profile OMITTED from the list and a uniform 404 on the
// item and on effective-tools, byte-equal to an unknown name; an administrator
// sees it.
func TestProfilesCrud_ScopedCallerSeesUnreachableProfileAsUnknown(t *testing.T) {
	g := newProfilesRig(t)

	list := decodeProfilesData(t, g.do(g.token, http.MethodGet, "/api/v1/profiles", ""))
	names := []string{}
	for _, p := range list["profiles"].([]interface{}) {
		names = append(names, p.(map[string]interface{})["name"].(string))
	}
	assert.ElementsMatch(t, []string{"research", "shared"}, names, "deploy (beta only) is omitted, not shown with servers:[]")

	adminList := decodeProfilesData(t, g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/profiles", ""))
	assert.Len(t, adminList["profiles"], 3, "an administrator sees every profile")

	for _, path := range []string{"/api/v1/profiles/%s", "/api/v1/profiles/%s/effective-tools"} {
		unreachable := g.do(g.token, http.MethodGet, fmt.Sprintf(path, "deploy"), "")
		unknown := g.do(g.token, http.MethodGet, fmt.Sprintf(path, "no-such-profile"), "")
		require.Equal(t, http.StatusNotFound, unreachable.Code, path)
		require.Equal(t, http.StatusNotFound, unknown.Code, path)
		assert.JSONEq(t, stripRequestID(t, unknown.Body.String()), stripRequestID(t, unreachable.Body.String()), "byte-equal answer for %s", path)
		assert.Contains(t, unreachable.Body.String(), "profile not found")
	}
	adminItem := g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/profiles/deploy", "")
	assert.Equal(t, http.StatusOK, adminItem.Code)

	// The same for a session principal (server edition), which never reaches
	// the fake service's ViewerScope.Restricted=false path.
	carol := &auth.AuthContext{Type: auth.AuthTypeUser, UserID: "carol", AllowedServers: []string{"alpha"}, CredentialKind: auth.CredentialKindCookie}
	rec := callAs(g.admin.handleGetProfile, carol, "/api/v1/profiles/deploy", map[string]string{"name": "deploy"})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func stripRequestID(t *testing.T, body string) string {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(body), &m))
	delete(m, "request_id")
	out, err := json.Marshal(m)
	require.NoError(t, err)
	return string(out)
}

// callAs runs one handler directly with an AuthContext and chi URL params, the
// way the auth middleware and router would have set them.
func callAs(h http.HandlerFunc, ac *auth.AuthContext, path string, params map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	req = req.WithContext(auth.WithAuthContext(ctx, ac))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// used_by and anonymous_profile disclose other credentials' bindings: they are
// administrator-only, OMITTED (not emptied) for every other caller.
func TestProfilesCrud_UsedByAndAnonymousAreAdminOnly(t *testing.T) {
	g := newProfilesRig(t)

	scopedBody := g.do(g.token, http.MethodGet, "/api/v1/profiles", "").Body.String()
	assert.NotContains(t, scopedBody, "used_by")
	assert.NotContains(t, scopedBody, "anonymous_profile")
	assert.NotContains(t, scopedBody, "ro-bot")
	assert.NotContains(t, scopedBody, "cursor")
	item := g.do(g.token, http.MethodGet, "/api/v1/profiles/research", "").Body.String()
	assert.NotContains(t, item, "used_by")

	adminData := decodeProfilesData(t, g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/profiles", ""))
	assert.Equal(t, "research", adminData["anonymous_profile"])
	for _, p := range adminData["profiles"].([]interface{}) {
		used, ok := p.(map[string]interface{})["used_by"].(map[string]interface{})
		require.True(t, ok, "an administrator's rows carry used_by")
		assert.Contains(t, used, "clients")
		assert.Contains(t, used, "tokens")
		assert.Contains(t, used, "anonymous_profile")
	}

	carol := &auth.AuthContext{Type: auth.AuthTypeUser, UserID: "carol", AllowedServers: []string{"alpha"}, CredentialKind: auth.CredentialKindCookie}
	body := callAs(g.admin.handleListProfiles, carol, "/api/v1/profiles", nil).Body.String()
	assert.NotContains(t, body, "used_by")
	assert.NotContains(t, body, "anonymous_profile")
}

// F26: rules and switchable_to name servers and profiles a scoped caller must
// not learn; a byte search of the body finds none of them.
func TestProfilesCrud_RedactsRulesServersAndSwitchableTo(t *testing.T) {
	g := newProfilesRig(t)
	body := g.do(g.token, http.MethodGet, "/api/v1/profiles/research", "").Body.String()
	for _, leak := range []string{"beta", "secret_tool", "hidden_admin", "deploy"} {
		assert.NotContains(t, body, leak, "a scoped caller must not see %q", leak)
	}
	data := decodeProfilesData(t, g.do(g.token, http.MethodGet, "/api/v1/profiles/research", ""))
	assert.Equal(t, []interface{}{"alpha"}, data["servers"])
	assert.Equal(t, []interface{}{"alpha"}, data["effective_servers"])
	tools := data["tools"].(map[string]interface{})
	assert.ElementsMatch(t, []interface{}{"alpha:list", "*:ping"}, tools["allow"], "a wildcard server segment names no server and stays")
	assert.Equal(t, map[string]interface{}{"alpha:search": "read"}, tools["classify"])
	assert.Equal(t, []interface{}{"shared"}, data["switchable_to"], "only reachable profiles")

	admin := g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/profiles/research", "").Body.String()
	for _, name := range []string{"beta", "secret_tool", "hidden_admin", "deploy"} {
		assert.Contains(t, admin, name, "an administrator sees the whole profile")
	}
}

func TestProfilesCrud_V2FieldsAreKept(t *testing.T) {
	g := newProfilesRig(t)
	data := decodeProfilesData(t, g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/profiles", ""))
	first := data["profiles"].([]interface{})[0].(map[string]interface{})
	for _, key := range []string{"name", "servers", "tool_count", "effective_servers", "tool_counts", "is_legacy", "calls_24h", "blocked_24h", "effective_unannotated", "effective_code_execution"} {
		assert.Contains(t, first, key)
	}
}

func TestProfilesCrud_ScopedTokenOnMutatingRoutesIs403(t *testing.T) {
	g := newProfilesRig(t)
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/profiles", `{"name":"x"}`},
		{http.MethodPut, "/api/v1/profiles/research", `{"name":"research"}`},
		{http.MethodDelete, "/api/v1/profiles/research", ""},
		{http.MethodPost, "/api/v1/profiles/research/rename", `{"new_name":"r2"}`},
		{http.MethodPost, "/api/v1/profiles/try", `{"profile":{},"query":"q"}`},
		{http.MethodGet, "/api/v1/access/explain?profile=research&tool=alpha:list", ""},
	} {
		rec := g.do(g.token, rt.method, rt.path, rt.body)
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s: %s", rt.method, rt.path, rec.Body.String())
	}
	assert.Empty(t, g.fake.calls, "a refused write never reaches the service")
}

func TestProfilesEffectiveTools_ScopedCallerGetsVisibleRowsOnly(t *testing.T) {
	g := newProfilesRig(t)

	// client= and reason= are administrator-only.
	for _, q := range []string{"?client=cursor", "?reason=above_tier_cap"} {
		rec := g.do(g.token, http.MethodGet, "/api/v1/profiles/research/effective-tools"+q, "")
		require.Equal(t, http.StatusForbidden, rec.Code, q)
		assert.Contains(t, rec.Body.String(), "operation requires admin access")
	}
	// A reachable profile is served, with the viewer marked restricted.
	rec := g.do(g.token, http.MethodGet, "/api/v1/profiles/research/effective-tools?server=alpha", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, g.fake.calls, "effective:research::")

	// An administrator may pass both.
	rec = g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/profiles/research/effective-tools?client=cursor&reason=above_tier_cap", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, g.fake.calls, "effective:research:cursor:above_tier_cap")
}

func TestProfilesEffectiveTools_ErrorMapping(t *testing.T) {
	g := newProfilesRig(t)
	g.fake.err = profile.ErrUnknownClient
	rec := g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/profiles/research/effective-tools?client=ghost", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "client not found")
	g.fake.err = internalRuntime.ErrEvaluatorUnavailable
	rec = g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/profiles/research/effective-tools", "")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "an unwired evaluator is never an empty 'allowed'")
}
