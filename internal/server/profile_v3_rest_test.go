package server

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 108-f: the /profiles REST surface end to end - real router, real
// runtime, real profiles service, real evaluator and binding guard. The
// handlers' own logic is pinned in internal/httpapi with a fake service; this
// file proves the stack behaves as one.

// wireProfiles installs what production wiring installs in server.New: the
// binding-guard evaluator, the profile evaluator, the session hook and the
// profiles service on the REST server.
func (f *restV3Fixture) wireProfiles() {
	f.t.Helper()
	f.srv.logger = zap.NewNop() // the bare fixture Server has none; list reads call GetAllServers
	f.rt.SetBindingGuard(f.proxy)
	f.rt.SetProfileEvaluator(f.proxy)
	f.rt.ProfilesService().SetSessionHook(f.proxy)
	f.api.SetProfilesService(f.rt.ProfilesService())
}

func (f *restV3Fixture) json(method, path string, body interface{}) (int, map[string]interface{}) {
	f.t.Helper()
	rec := f.do(method, path, restV3AdminKey, body, "")
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func dataOf(resp map[string]interface{}) map[string]interface{} {
	d, _ := resp["data"].(map[string]interface{})
	return d
}

func (f *restV3Fixture) profileChanges() []map[string]interface{} {
	f.t.Helper()
	recs, _, err := f.rt.StorageManager().ListActivities(storage.ActivityFilter{Types: []string{string(storage.ActivityTypeProfileChange)}, Limit: 100})
	require.NoError(f.t, err)
	var out []map[string]interface{}
	for _, r := range recs {
		out = append(out, r.Metadata)
	}
	return out
}

func (f *restV3Fixture) configBytes() string {
	f.t.Helper()
	b, err := os.ReadFile(config.GetConfigPath(f.rt.Config().DataDir))
	if err != nil {
		return ""
	}
	return string(b)
}

func TestProfileV3REST_CRUDRenameDeleteAndRecords(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = true })
	f.wireProfiles()
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	f.mint("ro-bot", "work-readonly")

	status, resp := f.json(http.MethodPost, "/api/v1/profiles", map[string]interface{}{"name": "tmp", "servers": []string{"github", "ghost"}, "max_tier": "read"})
	require.Equal(t, http.StatusCreated, status, resp)
	assert.Equal(t, "tmp", dataOf(resp)["profile"].(map[string]interface{})["name"])
	warnings := dataOf(resp)["warnings"].([]interface{})
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], `unknown server "ghost"`)

	// Validation: the FR-007 text and the field.
	status, resp = f.json(http.MethodPut, "/api/v1/profiles/tmp", map[string]interface{}{"name": "tmp", "servers": []string{"github"}, "max_tier": "bogus"})
	require.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "max_tier", resp["field"])
	assert.Contains(t, resp["error"], `invalid max_tier "bogus": must be one of read, write, destructive`)
	status, resp = f.json(http.MethodPost, "/api/v1/profiles", map[string]interface{}{"name": "active"})
	require.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "name", resp["field"])
	status, resp = f.json(http.MethodPost, "/api/v1/profiles", map[string]interface{}{"name": "tmp"})
	require.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "profile_exists", resp["code"])

	status, resp = f.json(http.MethodGet, "/api/v1/profiles/tmp", nil)
	require.Equal(t, http.StatusOK, status)
	view := dataOf(resp)
	assert.Equal(t, "tmp", view["name"])
	assert.Equal(t, []interface{}{"github"}, view["effective_servers"])
	assert.NotNil(t, view["used_by"])

	// Delete: in use is a 409 with used_by; a clean one succeeds.
	status, resp = f.json(http.MethodDelete, "/api/v1/profiles/work-readonly", nil)
	require.Equal(t, http.StatusConflict, status, resp)
	assert.Equal(t, "profile_in_use", resp["code"])
	used := resp["used_by"].(map[string]interface{})
	assert.Equal(t, []interface{}{map[string]interface{}{"id": "cursor", "mode": "locked"}}, used["clients"])
	assert.Equal(t, []interface{}{"ro-bot"}, used["tokens"])

	status, resp = f.json(http.MethodDelete, "/api/v1/profiles/tmp", nil)
	require.Equal(t, http.StatusOK, status, resp)
	assert.Equal(t, "tmp", dataOf(resp)["deleted"])

	// Rename moves the client and the token; both keep resolving.
	status, resp = f.json(http.MethodPost, "/api/v1/profiles/work-readonly/rename", map[string]interface{}{"new_name": "work-ro"})
	require.Equal(t, http.StatusOK, status, resp)
	moved := dataOf(resp)["moved"].(map[string]interface{})
	assert.Equal(t, []interface{}{"cursor"}, moved["clients"])
	assert.Equal(t, []interface{}{"ro-bot"}, moved["tokens"])
	rec, err := f.rt.StorageManager().GetAgentTokenByName("client-cursor")
	require.NoError(t, err)
	assert.Equal(t, "work-ro", rec.ProfilePin)

	// One record per mutation, newest first: rename, delete, create.
	changes := f.profileChanges()
	require.GreaterOrEqual(t, len(changes), 3)
	kinds := []string{}
	for _, c := range changes[:3] {
		kinds = append(kinds, c["change"].(string))
		assert.Equal(t, "api_key", c["actor_kind"])
	}
	assert.Equal(t, []string{"rename", "delete", "create"}, kinds)
}

func TestProfileV3REST_DeleteAnonymousProfileIsRefusedEvenWithForce(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) {
		cfg.RequireMCPAuth = true
		cfg.AnonymousProfile = "work-full"
	})
	f.wireProfiles()
	status, resp := f.json(http.MethodDelete, "/api/v1/profiles/work-full?force=true", nil)
	require.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "profile_is_anonymous_profile", resp["code"])
	assert.Equal(t, "profile is the anonymous_profile; pass reassign_to or change anonymous_profile first", resp["error"])

	status, resp = f.json(http.MethodDelete, "/api/v1/profiles/work-full?reassign_to=legacy", nil)
	require.Equal(t, http.StatusOK, status, resp)
	assert.Equal(t, "legacy", dataOf(resp)["anonymous_profile_moved_to"])
}

// FR-008a (require_mcp_auth off): a profile write that would let a bound client
// escape its profile by omitting its credential is refused 409
// binding_bypassable_without_auth with bindings and fixes, and the config file
// is byte-identical afterwards.
func TestProfileV3REST_GuardedWritesAreRefusedAndWriteNothing(t *testing.T) {
	anon := func(cfg *config.Config) {
		cfg.RequireMCPAuth = false
		// anon-ro is what an anonymous caller resolves to: the same scope as
		// work-readonly, so the locked cursor binding is not bypassable.
		ro := cfg.Profiles[0]
		ro.Name = "anon-ro"
		ro.Title = ""
		ro.SwitchableTo = nil // the anonymous side must not already reach work-full
		cfg.Profiles = append(cfg.Profiles, ro)
		cfg.AnonymousProfile = "anon-ro"
	}
	setup := func(t *testing.T) *restV3Fixture {
		f := newProfilesV3RESTFixture(t, anon)
		f.wireProfiles()
		f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
		return f
	}
	refused := func(t *testing.T, f *restV3Fixture, method, path string, body interface{}) {
		t.Helper()
		before, changes := f.configBytes(), len(f.profileChanges())
		status, resp := f.json(method, path, body)
		require.Equal(t, http.StatusConflict, status, "%s %s: %v", method, path, resp)
		assert.Equal(t, "binding_bypassable_without_auth", resp["code"])
		bindings, _ := resp["bindings"].([]interface{})
		require.NotEmpty(t, bindings)
		assert.Equal(t, "cursor", bindings[0].(map[string]interface{})["client_id"])
		assert.NotEmpty(t, resp["fixes"])
		assert.Equal(t, before, f.configBytes(), "%s %s wrote the config", method, path)
		assert.Len(t, f.profileChanges(), changes, "a refusal writes no record")
	}

	t.Run("PUT widening the anonymous-reachable profile", func(t *testing.T) {
		f := setup(t)
		refused(t, f, http.MethodPut, "/api/v1/profiles/anon-ro", map[string]interface{}{
			"name": "anon-ro", "servers": []string{"github", "notion", "filesystem"}, "max_tier": "read",
		})
	})
	t.Run("PUT narrowing the bound profile", func(t *testing.T) {
		f := setup(t)
		refused(t, f, http.MethodPut, "/api/v1/profiles/work-readonly", map[string]interface{}{
			"name": "work-readonly", "servers": []string{"github"}, "max_tier": "read",
		})
	})
	t.Run("DELETE with reassign_to a narrower profile", func(t *testing.T) {
		f := setup(t)
		refused(t, f, http.MethodDelete, "/api/v1/profiles/work-readonly?reassign_to=legacy", nil)
	})
	t.Run("rename that brings a dangling anonymous switchable_to to life", func(t *testing.T) {
		f := newProfilesV3RESTFixture(t, func(cfg *config.Config) {
			anon(cfg)
			dangling := []string{"wide"}
			cfg.Profiles[len(cfg.Profiles)-1].SwitchableTo = &dangling // anon-ro may switch to a profile that does not exist yet
		})
		f.wireProfiles()
		f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
		refused(t, f, http.MethodPost, "/api/v1/profiles/work-full/rename", map[string]interface{}{"new_name": "wide"})
	})
	t.Run("POST that brings a dangling anonymous_profile to life", func(t *testing.T) {
		f := newProfilesV3RESTFixture(t, func(cfg *config.Config) {
			cfg.RequireMCPAuth = false
			cfg.AnonymousProfile = "future" // dangling: anonymous is deny-all today
		})
		f.wireProfiles()
		f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
		refused(t, f, http.MethodPost, "/api/v1/profiles", map[string]interface{}{
			"name": "future", "servers": []string{"github", "notion", "filesystem"},
		})
	})
}

func TestProfileV3REST_EffectiveToolsAdminAndClientOnAnotherProfile(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.wireProfiles()
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)

	status, resp := f.json(http.MethodGet, "/api/v1/profiles/work-readonly/effective-tools", nil)
	require.Equal(t, http.StatusOK, status, resp)
	rows := map[string]map[string]interface{}{}
	for _, r := range dataOf(resp)["tools"].([]interface{}) {
		row := r.(map[string]interface{})
		rows[row["server"].(string)+":"+row["tool"].(string)] = row
	}
	assert.Equal(t, "above_tier_cap", rows["github:create_issue"]["access"].(map[string]interface{})["reason"])
	assert.Equal(t, "server_not_in_profile", rows["filesystem:read_text_file"]["access"].(map[string]interface{})["reason"])
	counts := dataOf(resp)["counts"].(map[string]interface{})
	assert.Contains(t, counts, "by_reason")
	assert.Contains(t, counts, "callable")

	if clientsEdition {
		status, resp = f.json(http.MethodGet, "/api/v1/profiles/work-full/effective-tools?client=cursor&server=github", nil)
		require.Equal(t, http.StatusOK, status, resp)
		for _, r := range dataOf(resp)["tools"].([]interface{}) {
			assert.Equal(t, "github", r.(map[string]interface{})["server"])
		}
	}

	status, _ = f.json(http.MethodGet, "/api/v1/profiles/nope/effective-tools", nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, _ = f.json(http.MethodGet, "/api/v1/profiles/work-full/effective-tools?client=ghost", nil)
	assert.Equal(t, http.StatusNotFound, status)
}

func TestProfileV3REST_NonAdminSeesVisibleRowsOnly(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.wireProfiles()
	raw := f.mintWith("gh-only", "", []string{"github"}, []string{auth.PermRead})

	rec := f.do(http.MethodGet, "/api/v1/profiles/work-readonly/effective-tools", raw, nil, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, leak := range []string{"above_tier_cap", "denied_by_rule", "server_not_in_profile", "notion", "update_page", "filesystem", "create_issue"} {
		assert.NotContains(t, body, leak, "%q is hidden from a caller that may not see it", leak)
	}
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	counts := dataOf(resp)["counts"].(map[string]interface{})
	assert.NotContains(t, counts, "callable")
	assert.NotContains(t, counts, "by_reason")
	assert.NotContains(t, dataOf(resp), "stale_classifications")

	// The list omits profiles the token cannot reach and never carries used_by.
	rec = f.do(http.MethodGet, "/api/v1/profiles", raw, nil, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "used_by")
	assert.NotContains(t, rec.Body.String(), "notion")

	// Administrator-only surfaces.
	for _, path := range []string{
		"/api/v1/profiles/work-readonly/effective-tools?client=cursor",
		"/api/v1/profiles/work-readonly/effective-tools?reason=above_tier_cap",
		"/api/v1/access/explain?profile=work-readonly&tool=github:list_issues",
	} {
		assert.Equal(t, http.StatusForbidden, f.do(http.MethodGet, path, raw, nil, "").Code, path)
	}
}

func TestProfileV3REST_AccessExplainEndToEnd(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.wireProfiles()
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	f.mint("ro-bot", "work-readonly")

	if clientsEdition {
		status, resp := f.json(http.MethodGet, "/api/v1/access/explain?client=cursor&tool=github:create_issue", nil)
		require.Equal(t, http.StatusOK, status, resp)
		d := dataOf(resp)
		assert.Equal(t, "hidden", d["verdict"])
		assert.Equal(t, "tier_cap", d["first_failure"])
		fixes := d["fixes"].([]interface{})
		require.Len(t, fixes, 2)
		assert.Equal(t, "allow_in_profile", fixes[0].(map[string]interface{})["action"])
		assert.Equal(t, "move_client", fixes[1].(map[string]interface{})["action"])
	}

	status, resp := f.json(http.MethodGet, "/api/v1/access/explain?token=ro-bot&tool=github:list_issues", nil)
	require.Equal(t, http.StatusOK, status, resp)
	assert.Equal(t, "allowed", dataOf(resp)["verdict"])

	for path, want := range map[string]int{
		"/api/v1/access/explain?tool=github:list_issues":                          http.StatusBadRequest,
		"/api/v1/access/explain?token=client-cursor&tool=github:list_issues":      http.StatusBadRequest,
		"/api/v1/access/explain?profile=work-full&tool=upstream_servers":          http.StatusBadRequest,
		"/api/v1/access/explain?token=nope&tool=github:list_issues":               http.StatusNotFound,
		"/api/v1/access/explain?profile=nope&tool=github:list_issues":             http.StatusNotFound,
		"/api/v1/access/explain?client=nope&tool=github:list_issues":              http.StatusNotFound,
		"/api/v1/access/explain?client=cursor&profile=work-full&tool=github:list": http.StatusBadRequest,
	} {
		status, _ = f.json(http.MethodGet, path, nil)
		assert.Equal(t, want, status, path)
	}
}

func TestProfileV3REST_TryAndActiveDeprecation(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.wireProfiles()
	indexEnforcementMatrixFixtureTools(t, f.proxy)
	before, changes := f.configBytes(), len(f.profileChanges())

	status, resp := f.json(http.MethodPost, "/api/v1/profiles/try", map[string]interface{}{
		"profile": map[string]interface{}{"servers": []string{"github"}, "max_tier": "read"}, "query": "issue",
	})
	require.Equal(t, http.StatusOK, status, resp)
	d := dataOf(resp)
	assert.Contains(t, d, "hidden_by_profile")
	assert.Contains(t, d, "hidden")
	assert.Equal(t, before, f.configBytes(), "try persists nothing")
	assert.Len(t, f.profileChanges(), changes, "try writes no record")

	rec := f.do(http.MethodGet, "/api/v1/profiles/active", restV3AdminKey, nil, "")
	assert.Equal(t, "true", rec.Header().Get("Deprecation"))
}
