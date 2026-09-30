package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-h (T086/T087): the `profiles` admin MCP tool. Every operation is
// driven through the handler over the same runtime, services and REST server the
// routes use, and compared with REST where the contract says the two are equal:
// argument names, result data and error bodies (FR-037).

// apiKeyCtx is an MCP request authenticated with the instance API key, as the
// /mcp auth middleware stamps it.
func apiKeyCtx() context.Context {
	return auth.WithAuthContext(context.Background(), credentialKindContext(auth.AdminContext(), auth.CredentialKindAPIKey))
}

// socketCtx is an MCP request from the tray socket.
func socketCtx() context.Context {
	return auth.WithAuthContext(context.Background(), credentialKindContext(auth.AdminContext(), auth.CredentialKindSocket))
}

// unconfinedAnonymousCtx is the admin-typed anonymous identity (no credential,
// require_mcp_auth off).
func unconfinedAnonymousCtx() context.Context {
	return auth.WithAuthContext(context.Background(), credentialKindContext(auth.AnonymousContext(), auth.CredentialKindAnonymous))
}

type profilesToolFixture struct {
	*restV3Fixture
}

// newProfilesToolFixture is the enforcement-matrix fixture fronted by the REST
// router, with the profiles service wired and the tool's admin views installed
// exactly as server.go installs them. require_mcp_auth is on unless configure
// turns it off, so the FR-008a guard is inert by default.
func newProfilesToolFixture(t *testing.T, configure func(*config.Config)) *profilesToolFixture {
	t.Helper()
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) {
		cfg.RequireMCPAuth = true
		if configure != nil {
			configure(cfg)
		}
	})
	f.wireProfiles()
	f.api.SetClientsService(f.rt.ClientsService())
	f.proxy.SetAdminViews(f.api)
	return &profilesToolFixture{f}
}

// call runs one profiles operation and returns the decoded JSON text content,
// whether the result was an error, and the raw text.
func (f *profilesToolFixture) call(ctx context.Context, args map[string]any) (map[string]any, bool, string) {
	f.t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = "profiles"
	req.Params.Arguments = args
	res, err := f.proxy.handleProfiles(ctx, req)
	require.NoError(f.t, err)
	require.NotNil(f.t, res)
	text := resultText(f.t, res)
	var out map[string]any
	if jsonErr := json.Unmarshal([]byte(text), &out); jsonErr != nil {
		return nil, res.IsError, text
	}
	return out, res.IsError, text
}

// ok runs an operation that must succeed and returns its data.
func (f *profilesToolFixture) ok(args map[string]any) map[string]any {
	f.t.Helper()
	out, isErr, text := f.call(apiKeyCtx(), args)
	require.False(f.t, isErr, text)
	return out
}

// refused runs an operation that must fail and returns the error body.
func (f *profilesToolFixture) refused(ctx context.Context, args map[string]any) map[string]any {
	f.t.Helper()
	out, isErr, text := f.call(ctx, args)
	require.True(f.t, isErr, text)
	require.NotNil(f.t, out, text)
	return out
}

// rest drives the REST router as the administrator and returns the status, the
// `data` object and the error body (the envelope keys removed).
func (f *profilesToolFixture) rest(method, path string, body interface{}) (int, map[string]any, map[string]any) {
	f.t.Helper()
	rec := f.do(method, path, restV3AdminKey, body, "")
	var resp map[string]any
	require.NoError(f.t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	data, _ := resp["data"].(map[string]any)
	errBody := map[string]any{}
	if rec.Code >= 400 {
		for k, v := range resp {
			if k != "success" && k != "request_id" {
				errBody[k] = v
			}
		}
	}
	return rec.Code, data, errBody
}

func (f *profilesToolFixture) profileByName(name string) *config.ProfileConfig {
	f.t.Helper()
	cfg := f.rt.Config()
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == name {
			return &cfg.Profiles[i]
		}
	}
	return nil
}

// toolPropertyNames lists the input-schema property names of the built tool.
func toolPropertyNames(t *testing.T, tool mcp.Tool) []string {
	t.Helper()
	names := make([]string, 0, len(tool.InputSchema.Properties))
	for name := range tool.InputSchema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// --- argument parity (H6, H7) ---------------------------------------------------

// TestProfilesTool_SchemaPropertiesEqualProfileConfigPlusOpArgs: the input schema
// is exactly the ProfileConfig JSON fields plus operation and the operation
// arguments, so a new ProfileConfig field reaches MCP and a stray one fails.
func TestProfilesTool_SchemaPropertiesEqualProfileConfigPlusOpArgs(t *testing.T) {
	want := []string{"operation", "new_name", "reassign_to", "force", "tool", "tier", "client", "profile", "mode",
		"from_profile", "to_profile", "token", "anonymous", "server", "reason"}
	pc := reflect.TypeOf(config.ProfileConfig{})
	for i := 0; i < pc.NumField(); i++ {
		name, _, _ := strings.Cut(pc.Field(i).Tag.Get("json"), ",")
		want = append(want, name)
	}
	sort.Strings(want)

	tool := buildProfilesTool()
	assert.Equal(t, want, toolPropertyNames(t, tool))
	assert.Equal(t, []string{"operation"}, tool.InputSchema.Required)
	for _, name := range []string{"servers", "title", "description", "max_tier", "unannotated", "tools", "code_execution", "management_tools", "switchable_to", "name"} {
		assert.Contains(t, tool.InputSchema.Properties, name, "REST field %s is an argument", name)
	}
	op := tool.InputSchema.Properties["operation"].(map[string]any)
	assert.Equal(t, []string{"list", "get", "create", "update", "delete", "rename", "classify", "assign", "list_clients", "effective_tools", "explain"}, op["enum"])
	assert.Equal(t, []string{"read", "write", "destructive"}, tool.InputSchema.Properties["max_tier"].(map[string]any)["enum"])
	assert.Equal(t, []string{"deny", "as_write", "as_read"}, tool.InputSchema.Properties["unannotated"].(map[string]any)["enum"])
	assert.Equal(t, []string{"locked", "switchable"}, tool.InputSchema.Properties["mode"].(map[string]any)["enum"])
	assert.Equal(t, []string{"read", "write", "destructive", ""}, tool.InputSchema.Properties["tier"].(map[string]any)["enum"])

	// A tool object is a typed object of allow/deny/classify, never an opaque string.
	tools := tool.InputSchema.Properties["tools"].(map[string]any)
	assert.Equal(t, "object", tools["type"])
	assert.ElementsMatch(t, []string{"allow", "deny", "classify"}, mapKeys(tools["properties"].(map[string]any)))
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestProfilesTool_NoProfileJSONArgument(t *testing.T) {
	tool := buildProfilesTool()
	for name := range tool.InputSchema.Properties {
		assert.NotContains(t, name, "_json", "an opaque profile_json string broke FR-037")
	}
	assert.NotContains(t, tool.InputSchema.Properties, "query", "try is not an MCP operation")
	assert.NotContains(t, tool.InputSchema.Properties, "limit")
	assert.NotNil(t, tool.Annotations.DestructiveHint)
	assert.True(t, *tool.Annotations.DestructiveHint)
	assert.False(t, *tool.Annotations.ReadOnlyHint)
	assert.False(t, *tool.Annotations.OpenWorldHint)
}

func TestProfilesTool_UpdateReplacesProfileNamedByName(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	f.ok(map[string]any{"operation": "create", "name": "tmp", "servers": []any{"github", "notion"}, "max_tier": "read", "title": "Temp"})
	out := f.ok(map[string]any{"operation": "update", "name": "tmp", "servers": []any{"github"}})
	assert.Equal(t, "tmp", out["profile"].(map[string]any)["name"])
	stored := f.profileByName("tmp")
	require.NotNil(t, stored)
	assert.Equal(t, []string{"github"}, stored.Servers)
	assert.Empty(t, stored.MaxTier, "update is a full replace, not a merge")
	assert.Empty(t, stored.Title)

	body := f.refused(apiKeyCtx(), map[string]any{"operation": "update", "servers": []any{"github"}})
	assert.Equal(t, `update: missing required argument "name"`, body["error"])
	body = f.refused(apiKeyCtx(), map[string]any{"operation": "update", "name": "ghost", "servers": []any{"github"}})
	assert.Equal(t, "profile not found", body["error"])
}

// TestProfilesTool_OmissionSemanticsMatchREST: a missing code_execution or
// management_tools is nil, a missing switchable_to is nil and [] is an explicit
// none, round-tripped through the stored config (FR-037).
func TestProfilesTool_OmissionSemanticsMatchREST(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	f.ok(map[string]any{"operation": "create", "name": "omitted", "servers": []any{"github"}})
	f.ok(map[string]any{"operation": "create", "name": "explicit", "servers": []any{"github"},
		"code_execution": false, "management_tools": true, "switchable_to": []any{},
		"tools": map[string]any{"allow": []any{"github:list_issues"}, "classify": map[string]any{"github:search_code": "read"}}})

	omitted := f.profileByName("omitted")
	require.NotNil(t, omitted)
	assert.Nil(t, omitted.CodeExecution)
	assert.Nil(t, omitted.ManagementTools)
	assert.Nil(t, omitted.SwitchableTo, "an omitted switchable_to stays unset")
	assert.Nil(t, omitted.Tools)

	explicit := f.profileByName("explicit")
	require.NotNil(t, explicit)
	require.NotNil(t, explicit.CodeExecution)
	assert.False(t, *explicit.CodeExecution)
	require.NotNil(t, explicit.ManagementTools)
	assert.True(t, *explicit.ManagementTools)
	require.NotNil(t, explicit.SwitchableTo)
	assert.Empty(t, *explicit.SwitchableTo, "[] is an explicit none")
	require.NotNil(t, explicit.Tools)
	assert.Equal(t, map[string]string{"github:search_code": "read"}, explicit.Tools.Classify)

	// The stored file carries the explicit [] and omits what was not sent, as a
	// REST write does.
	raw := f.configBytes()
	assert.Contains(t, raw, `"switchable_to": []`)

	// The same body through REST stores the same profile.
	status, _, _ := f.rest(http.MethodPost, "/api/v1/profiles", map[string]any{
		"name": "via-rest", "servers": []string{"github"}, "code_execution": false, "management_tools": true,
		"switchable_to": []string{}, "tools": map[string]any{"allow": []string{"github:list_issues"}, "classify": map[string]string{"github:search_code": "read"}},
	})
	require.Equal(t, http.StatusCreated, status)
	viaREST := *f.profileByName("via-rest")
	viaREST.Name = "explicit"
	assert.Equal(t, *explicit, viaREST)
}

func TestProfilesTool_TypeMismatchNamesTheField(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	body := f.refused(apiKeyCtx(), map[string]any{"operation": "create", "name": "bad", "servers": "github"})
	assert.Equal(t, "servers", body["field"])
	assert.Contains(t, body["error"], `invalid argument "servers"`)
	body = f.refused(apiKeyCtx(), map[string]any{"operation": "create", "name": "bad", "code_execution": "no"})
	assert.Equal(t, "code_execution", body["field"])
	body = f.refused(apiKeyCtx(), map[string]any{"operation": "create", "name": "bad", "tools": map[string]any{"alow": []any{"a:b"}}})
	assert.Equal(t, "tools", body["field"], "an unknown key inside tools is refused, as on REST")
	assert.Nil(t, f.profileByName("bad"))
}

// F2.1: a top-level typo is refused on create/update as REST refuses it, so a
// dropped restriction never leaves the profile wider than intended.
func TestProfilesTool_UnknownTopLevelArgumentRefused(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	body := f.refused(apiKeyCtx(), map[string]any{"operation": "create", "name": "readonly", "max_teir": "read"})
	assert.Equal(t, "max_teir", body["field"])
	assert.Contains(t, body["error"], `invalid argument "max_teir"`)
	assert.Nil(t, f.profileByName("readonly"), "a refused create stores nothing")

	body = f.refused(apiKeyCtx(), map[string]any{"operation": "update", "name": "legacy", "max_teir": "read"})
	assert.Equal(t, "max_teir", body["field"])

	status, _, _ := f.rest(http.MethodPost, "/api/v1/profiles", map[string]any{"name": "readonly", "max_teir": "read"})
	assert.Equal(t, http.StatusBadRequest, status, "REST refuses the same body")
}

func TestProfilesTool_OperationAndArgumentErrors(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"no operation", map[string]any{}, `missing required argument "operation"`},
		{"unknown operation", map[string]any{"operation": "explode"}, `unknown operation "explode"; valid: list, get, create, update, delete, rename, classify, assign, list_clients, effective_tools, explain`},
		{"get without name", map[string]any{"operation": "get"}, `get: missing required argument "name"`},
		{"rename without new_name", map[string]any{"operation": "rename", "name": "legacy"}, `rename: missing required argument "new_name"`},
		{"classify without tool", map[string]any{"operation": "classify", "name": "legacy", "tier": "read"}, `classify: missing required argument "tool"`},
		{"classify without tier", map[string]any{"operation": "classify", "name": "legacy", "tool": "github:search_code"}, `classify: missing required argument "tier"`},
		{"effective_tools without name", map[string]any{"operation": "effective_tools"}, `effective_tools: missing required argument "name"`},
		{"assign with nothing", map[string]any{"operation": "assign"}, `assign: missing required argument "client"`},
		{"assign mixes forms", map[string]any{"operation": "assign", "client": "cursor", "profile": "legacy", "from_profile": "legacy", "to_profile": "work-full"}, `assign: use either client or from_profile/to_profile, not both`},
		{"force not a bool", map[string]any{"operation": "delete", "name": "legacy", "force": "yes"}, `invalid argument "force": must be true or false`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := f.refused(apiKeyCtx(), tc.args)
			assert.Equal(t, tc.want, body["error"])
			assert.NotContains(t, body, "success")
			assert.NotContains(t, body, "request_id")
		})
	}
	assert.NotNil(t, f.profileByName("legacy"), "a refused call changes nothing")
}

// --- error parity (H1, H2) ------------------------------------------------------

// assertErrorEqualsREST drives one refused operation through MCP and through its
// REST route and requires the same body (REST's minus the envelope).
func (f *profilesToolFixture) assertErrorEqualsREST(ctx context.Context, args map[string]any, method, path string, body interface{}) map[string]any {
	f.t.Helper()
	mcpBody := f.refused(ctx, args)
	status, _, restBody := f.rest(method, path, body)
	require.GreaterOrEqual(f.t, status, 400, "REST must refuse too: %v", restBody)
	wantRaw, err := json.Marshal(restBody)
	require.NoError(f.t, err)
	gotRaw, err := json.Marshal(mcpBody)
	require.NoError(f.t, err)
	assert.JSONEq(f.t, string(wantRaw), string(gotRaw), "MCP error body must be the REST body verbatim")
	return mcpBody
}

func TestProfilesTool_DeleteInUse_RESTBodyVerbatim(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	f.mint("ro-bot", "work-readonly")
	body := f.assertErrorEqualsREST(apiKeyCtx(), map[string]any{"operation": "delete", "name": "work-readonly"},
		http.MethodDelete, "/api/v1/profiles/work-readonly", nil)
	assert.Equal(t, "profile_in_use", body["code"])
	used := body["used_by"].(map[string]any)
	assert.Equal(t, []any{"ro-bot"}, used["tokens"])
}

func TestProfilesTool_DeleteAnonymous_RESTBodyVerbatim(t *testing.T) {
	f := newProfilesToolFixture(t, func(cfg *config.Config) { cfg.AnonymousProfile = "work-full" })
	body := f.assertErrorEqualsREST(apiKeyCtx(), map[string]any{"operation": "delete", "name": "work-full"},
		http.MethodDelete, "/api/v1/profiles/work-full", nil)
	assert.Equal(t, "profile_is_anonymous_profile", body["code"])
	body = f.assertErrorEqualsREST(apiKeyCtx(), map[string]any{"operation": "delete", "name": "work-full", "force": true},
		http.MethodDelete, "/api/v1/profiles/work-full?force=true", nil)
	assert.Equal(t, "profile_is_anonymous_profile", body["code"], "force never overrides the anonymous_profile")
}

func TestProfilesTool_AssignNoCredential_RESTBodyVerbatim(t *testing.T) {
	if !clientsEdition {
		t.Skip("client credentials exist in the personal edition only")
	}
	f := newProfilesToolFixture(t, nil)
	body := f.assertErrorEqualsREST(apiKeyCtx(), map[string]any{"operation": "assign", "client": "codex", "profile": "work-full"},
		http.MethodPut, "/api/v1/clients/codex/binding", map[string]any{"profile": "work-full"})
	assert.Equal(t, "no_client_credential", body["code"])
}

func TestProfilesTool_ValidatorTextsEqualREST(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	cases := []struct {
		name  string
		args  map[string]any
		rest  map[string]any
		field string
	}{
		{"max_tier", map[string]any{"name": "bad", "servers": []any{"github"}, "max_tier": "bogus"},
			map[string]any{"name": "bad", "servers": []string{"github"}, "max_tier": "bogus"}, "max_tier"},
		{"unannotated", map[string]any{"name": "bad", "servers": []any{"github"}, "unannotated": "maybe"},
			map[string]any{"name": "bad", "servers": []string{"github"}, "unannotated": "maybe"}, "unannotated"},
		{"reserved name", map[string]any{"name": "active", "servers": []any{"github"}},
			map[string]any{"name": "active", "servers": []string{"github"}}, "name"},
		{"title too long", map[string]any{"name": "bad", "servers": []any{"github"}, "title": strings.Repeat("t", 81)},
			map[string]any{"name": "bad", "servers": []string{"github"}, "title": strings.Repeat("t", 81)}, "title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"operation": "create"}
			for k, v := range tc.args {
				args[k] = v
			}
			body := f.assertErrorEqualsREST(apiKeyCtx(), args, http.MethodPost, "/api/v1/profiles", tc.rest)
			assert.Equal(t, tc.field, body["field"])
		})
	}
	assert.Nil(t, f.profileByName("bad"))
}

func TestProfilesTool_ExplainSubjectCount400Text(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	body := f.assertErrorEqualsREST(apiKeyCtx(), map[string]any{"operation": "explain", "tool": "github:list_issues"},
		http.MethodGet, "/api/v1/access/explain?tool="+url.QueryEscape("github:list_issues"), nil)
	assert.Equal(t, "exactly one of client, token, profile, anonymous is required", body["error"])
	f.assertErrorEqualsREST(apiKeyCtx(), map[string]any{"operation": "explain", "tool": "github:list_issues", "profile": "work-full", "anonymous": true},
		http.MethodGet, "/api/v1/access/explain?profile=work-full&anonymous=true&tool="+url.QueryEscape("github:list_issues"), nil)
}

func TestProfilesTool_ExplainBuiltinTool400Text(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	body := f.assertErrorEqualsREST(apiKeyCtx(), map[string]any{"operation": "explain", "profile": "work-full", "tool": "upstream_servers"},
		http.MethodGet, "/api/v1/access/explain?profile=work-full&tool=upstream_servers", nil)
	assert.Equal(t, profile.ErrExplainBuiltinTool.Error(), body["error"])
	f.assertErrorEqualsREST(apiKeyCtx(), map[string]any{"operation": "explain", "profile": "ghost", "tool": "github:list_issues"},
		http.MethodGet, "/api/v1/access/explain?profile=ghost&tool="+url.QueryEscape("github:list_issues"), nil)
}

// --- reads (H3, H4) -------------------------------------------------------------

// TestProfilesTool_EachOpResultEqualsRESTData: a table over every read operation
// comparing the tool's result with the `data` of the route it mirrors.
func TestProfilesTool_EachOpResultEqualsRESTData(t *testing.T) {
	f := newProfilesToolFixture(t, func(cfg *config.Config) { cfg.AnonymousProfile = "work-full" })
	if clientsEdition {
		f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	}
	f.mint("ro-bot", "work-readonly")
	indexEnforcementMatrixFixtureTools(t, f.proxy)

	cases := []struct {
		name   string
		args   map[string]any
		path   string
		mutate func(map[string]any)
	}{
		{"list", map[string]any{"operation": "list"}, "/api/v1/profiles", nil},
		{"get", map[string]any{"operation": "get", "name": "work-readonly"}, "/api/v1/profiles/work-readonly", nil},
		{"effective_tools", map[string]any{"operation": "effective_tools", "name": "work-readonly"}, "/api/v1/profiles/work-readonly/effective-tools", nil},
		{"effective_tools server", map[string]any{"operation": "effective_tools", "name": "work-full", "server": "github"}, "/api/v1/profiles/work-full/effective-tools?server=github", nil},
		{"effective_tools reason", map[string]any{"operation": "effective_tools", "name": "work-readonly", "reason": "above_tier_cap"}, "/api/v1/profiles/work-readonly/effective-tools?reason=above_tier_cap", nil},
		{"explain profile", map[string]any{"operation": "explain", "profile": "work-readonly", "tool": "github:create_issue"}, "/api/v1/access/explain?profile=work-readonly&tool=github:create_issue", nil},
		{"explain token", map[string]any{"operation": "explain", "token": "ro-bot", "tool": "github:list_issues"}, "/api/v1/access/explain?token=ro-bot&tool=github:list_issues", nil},
		{"explain anonymous", map[string]any{"operation": "explain", "anonymous": true, "tool": "github:list_issues"}, "/api/v1/access/explain?anonymous=true&tool=github:list_issues", nil},
	}
	if clientsEdition {
		cases = append(cases,
			struct {
				name   string
				args   map[string]any
				path   string
				mutate func(map[string]any)
			}{"explain client", map[string]any{"operation": "explain", "client": "cursor", "tool": "github:create_issue"}, "/api/v1/access/explain?client=cursor&tool=github:create_issue", nil},
			struct {
				name   string
				args   map[string]any
				path   string
				mutate func(map[string]any)
			}{"effective_tools client", map[string]any{"operation": "effective_tools", "name": "work-full", "client": "cursor"}, "/api/v1/profiles/work-full/effective-tools?client=cursor", nil},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, want, _ := f.rest(http.MethodGet, tc.path, nil)
			require.Equal(t, http.StatusOK, status)
			got := f.ok(tc.args)
			wantRaw, err := json.Marshal(want)
			require.NoError(t, err)
			gotRaw, err := json.Marshal(got)
			require.NoError(t, err)
			assert.JSONEq(t, string(wantRaw), string(gotRaw))
		})
	}
	list := f.ok(map[string]any{"operation": "list"})
	assert.Equal(t, "work-full", list["anonymous_profile"], "the caller is an administrator, so the anonymous_profile is included")
}

// TestProfilesTool_EffectiveToolsClassificationStaleEqualsREST pins FR-005: a
// classify entry for a tool that is now annotated is reported stale, and the
// report is the REST one.
func TestProfilesTool_EffectiveToolsClassificationStaleEqualsREST(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	indexEnforcementMatrixFixtureTools(t, f.proxy)
	// github:list_issues is annotated read-only upstream; classifying it is stale.
	out := f.ok(map[string]any{"operation": "classify", "name": "work-full", "tool": "github:list_issues", "tier": "write"})
	assert.Contains(t, out, "warnings")

	got := f.ok(map[string]any{"operation": "effective_tools", "name": "work-full"})
	status, want, _ := f.rest(http.MethodGet, "/api/v1/profiles/work-full/effective-tools", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, want["stale_classifications"], got["stale_classifications"])
	assert.Equal(t, []any{"github:list_issues"}, got["stale_classifications"])
	stale := false
	for _, row := range got["tools"].([]any) {
		r := row.(map[string]any)
		if r["server"] == "github" && r["tool"] == "list_issues" {
			stale = r["classification_stale"] == true
		}
	}
	assert.True(t, stale, "the row carries classification_stale")
}

func TestProfilesTool_EffectiveToolsServerAndReasonArgs(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	indexEnforcementMatrixFixtureTools(t, f.proxy)
	out := f.ok(map[string]any{"operation": "effective_tools", "name": "work-full", "server": "notion"})
	rows := out["tools"].([]any)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		assert.Equal(t, "notion", row.(map[string]any)["server"])
	}
	out = f.ok(map[string]any{"operation": "effective_tools", "name": "work-readonly", "reason": "above_tier_cap"})
	for _, row := range out["tools"].([]any) {
		assert.Equal(t, "above_tier_cap", row.(map[string]any)["access"].(map[string]any)["reason"])
	}
}

// TestProfilesTool_ListClientsEqualsRESTMinusPaths: list_clients returns the REST
// list without the routing object and the config paths.
func TestProfilesTool_ListClientsEqualsRESTMinusPaths(t *testing.T) {
	if !clientsEdition {
		t.Skip("client credentials exist in the personal edition only")
	}
	f := newProfilesToolFixture(t, nil)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	f.mintClient("windsurf", "", auth.ProfileModeSwitchable)
	for _, tc := range []struct{ profile, query string }{
		{"work-readonly", "?profile=work-readonly"},
		{"-", "?profile=-"},
		{"", ""},
	} {
		t.Run("profile="+tc.profile, func(t *testing.T) {
			status, want, _ := f.rest(http.MethodGet, "/api/v1/clients"+tc.query, nil)
			require.Equal(t, http.StatusOK, status)
			delete(want, "routing")
			for _, row := range want["clients"].([]any) {
				delete(row.(map[string]any), "config_path")
				delete(row.(map[string]any), "display_path")
			}
			args := map[string]any{"operation": "list_clients"}
			if tc.profile != "" {
				args["profile"] = tc.profile
			}
			got := f.ok(args)
			assert.NotContains(t, got, "routing")
			assert.Equal(t, []string{"clients", "warnings"}, profilesToolSortedKeys(got))
			for _, row := range got["clients"].([]any) {
				assert.NotContains(t, row, "config_path")
				assert.NotContains(t, row, "display_path")
			}
			wantRaw, _ := json.Marshal(want)
			gotRaw, _ := json.Marshal(got)
			assert.JSONEq(t, string(wantRaw), string(gotRaw))
		})
	}
	got := f.ok(map[string]any{"operation": "list_clients", "profile": "work-readonly"})
	ids := []string{}
	for _, row := range got["clients"].([]any) {
		ids = append(ids, row.(map[string]any)["id"].(string))
	}
	assert.Equal(t, []string{"cursor"}, ids)
}

func profilesToolSortedKeys(m map[string]any) []string {
	out := mapKeys(m)
	sort.Strings(out)
	return out
}

// --- mutations ------------------------------------------------------------------

func TestProfilesTool_CreateRenameDeleteClassifyResults(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	created := f.ok(map[string]any{"operation": "create", "name": "tmp", "servers": []any{"github", "ghost"}, "max_tier": "read"})
	assert.Equal(t, "tmp", created["profile"].(map[string]any)["name"])
	warnings := created["warnings"].([]any)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], `unknown server "ghost"`)

	classified := f.ok(map[string]any{"operation": "classify", "name": "tmp", "tool": "github:search_code", "tier": "read"})
	assert.Equal(t, map[string]any{"github:search_code": "read"}, classified["profile"].(map[string]any)["tools"].(map[string]any)["classify"])
	assert.Contains(t, classified, "warnings")
	cleared := f.ok(map[string]any{"operation": "classify", "name": "tmp", "tool": "github:search_code", "tier": ""})
	assert.Nil(t, cleared["profile"].(map[string]any)["tools"].(map[string]any)["classify"])

	renamed := f.ok(map[string]any{"operation": "rename", "name": "tmp", "new_name": "tmp2"})
	assert.Equal(t, "tmp2", renamed["profile"].(map[string]any)["name"])
	assert.Contains(t, renamed, "moved")

	deleted := f.ok(map[string]any{"operation": "delete", "name": "tmp2"})
	assert.Equal(t, "tmp2", deleted["deleted"])
	assert.Nil(t, f.profileByName("tmp2"))
}

func TestProfilesTool_AssignModeOmittedKeepsMode(t *testing.T) {
	if !clientsEdition {
		t.Skip("client credentials exist in the personal edition only")
	}
	f := newProfilesToolFixture(t, nil)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	out := f.ok(map[string]any{"operation": "assign", "client": "cursor", "profile": "work-full"})
	client := out["client"].(map[string]any)
	assert.Equal(t, "work-full", client["profile"])
	assert.Equal(t, "locked", client["profile_mode"], "mode omitted keeps the current mode")

	out = f.ok(map[string]any{"operation": "assign", "client": "cursor", "profile": "work-readonly", "mode": "switchable"})
	assert.Equal(t, "switchable", out["client"].(map[string]any)["profile_mode"])
}

func TestProfilesTool_AssignAllServersBecomesSwitchable(t *testing.T) {
	if !clientsEdition {
		t.Skip("client credentials exist in the personal edition only")
	}
	f := newProfilesToolFixture(t, nil)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	out := f.ok(map[string]any{"operation": "assign", "client": "cursor", "profile": ""})
	client := out["client"].(map[string]any)
	assert.Empty(t, client["profile"])
	assert.Equal(t, "switchable", client["profile_mode"], `profile "" (All servers) is switchable`)
}

func TestProfilesTool_AssignReturnsClientViewAndWarnings(t *testing.T) {
	if !clientsEdition {
		t.Skip("client credentials exist in the personal edition only")
	}
	f := newProfilesToolFixture(t, nil)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	out := f.ok(map[string]any{"operation": "assign", "client": "cursor", "profile": "work-full"})
	assert.Equal(t, []string{"client", "warnings"}, profilesToolSortedKeys(out))
	assert.Equal(t, "cursor", out["client"].(map[string]any)["id"])
	assert.NotNil(t, out["warnings"])
	// The row is the REST row.
	status, want, _ := f.rest(http.MethodGet, "/api/v1/clients", nil)
	require.Equal(t, http.StatusOK, status)
	var restRow map[string]any
	for _, row := range want["clients"].([]any) {
		if row.(map[string]any)["id"] == "cursor" {
			restRow = row.(map[string]any)
		}
	}
	require.NotNil(t, restRow)
	assert.Equal(t, restRow["profile"], out["client"].(map[string]any)["profile"])
	assert.Equal(t, restRow["credential_state"], out["client"].(map[string]any)["credential_state"])
}

func TestProfilesTool_BulkAssignMovedSkipped(t *testing.T) {
	if !clientsEdition {
		t.Skip("client credentials exist in the personal edition only")
	}
	f := newProfilesToolFixture(t, nil)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	f.mintClient("windsurf", "work-readonly", auth.ProfileModeLocked)
	out := f.ok(map[string]any{"operation": "assign", "from_profile": "work-readonly", "to_profile": "work-full"})
	assert.Equal(t, []any{"cursor", "windsurf"}, out["moved"])
	assert.Equal(t, []any{}, out["skipped"])

	body := f.refused(apiKeyCtx(), map[string]any{"operation": "assign", "from_profile": "work-full", "to_profile": "ghost"})
	assert.Equal(t, "to_profile", body["field"])
	assert.Equal(t, `unknown profile "ghost"`, body["error"])
	body = f.refused(apiKeyCtx(), map[string]any{"operation": "assign", "from_profile": "work-full"})
	assert.Equal(t, "to_profile", body["field"])
}

func TestProfilesTool_AssignMixedFormsRejected(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	body := f.refused(apiKeyCtx(), map[string]any{"operation": "assign", "client": "cursor", "profile": "work-full", "from_profile": "a", "to_profile": "b"})
	assert.Equal(t, "assign: use either client or from_profile/to_profile, not both", body["error"])
	assert.Equal(t, "client", body["field"])
}

// --- actor and records (H11, FR-030) ---------------------------------------------

func TestProfilesTool_SurfaceMCPRecorded(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	if clientsEdition {
		f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	}
	steps := []struct {
		args map[string]any
		ctx  context.Context
		kind string
	}{
		{map[string]any{"operation": "create", "name": "tmp", "servers": []any{"github"}}, apiKeyCtx(), "api_key"},
		{map[string]any{"operation": "update", "name": "tmp", "servers": []any{"github", "notion"}}, socketCtx(), "socket"},
		{map[string]any{"operation": "classify", "name": "tmp", "tool": "github:search_code", "tier": "read"}, apiKeyCtx(), "api_key"},
		{map[string]any{"operation": "rename", "name": "tmp", "new_name": "tmp2"}, apiKeyCtx(), "api_key"},
		{map[string]any{"operation": "delete", "name": "tmp2"}, apiKeyCtx(), "api_key"},
	}
	if clientsEdition {
		steps = append(steps, struct {
			args map[string]any
			ctx  context.Context
			kind string
		}{map[string]any{"operation": "assign", "client": "cursor", "profile": "work-full"}, apiKeyCtx(), "api_key"})
	}
	for _, step := range steps {
		before := len(f.profileChanges())
		out, isErr, text := f.call(step.ctx, step.args)
		require.False(t, isErr, text)
		require.NotNil(t, out)
		changes := f.profileChanges()
		require.Len(t, changes, before+1, "%v writes exactly one profile_change record", step.args["operation"])
		newest := changes[0]
		assert.Equal(t, string(profile.SurfaceMCP), newest["surface"], "%v", step.args["operation"])
		assert.Equal(t, step.kind, newest["actor_kind"], "%v", step.args["operation"])
	}
	// Reads write nothing.
	before := len(f.profileChanges())
	f.ok(map[string]any{"operation": "list"})
	assert.Len(t, f.profileChanges(), before)
}

func TestProfilesTool_InternalToolCallRecorded(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	f.ok(map[string]any{"operation": "get", "name": "work-full"})
	var found bool
	require.Eventually(t, func() bool {
		for _, rec := range f.activities() {
			if rec.ToolName == "profiles" {
				found = true
				assert.Equal(t, "internal_tool_call", string(rec.Type))
				assert.Equal(t, "", rec.ServerName)
				assert.Equal(t, "get", rec.Arguments["operation"])
			}
		}
		return found
	}, 3e9, 5e6, "the call writes an internal_tool_call record")
}

// --- hidden from the REST tools/call door and the code sandbox ---------------------

func TestProfilesTool_NotInCallToolDirect(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	rec := f.callTool(restV3AdminKey, "profiles", map[string]any{"operation": "list"}, "req-1")
	missing := f.callTool(restV3AdminKey, "no_such_tool", map[string]any{"operation": "list"}, "req-1")
	assert.Equal(t, missing.Code, rec.Code)
	var got, want map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got), rec.Body.String())
	require.NoError(t, json.Unmarshal(missing.Body.Bytes(), &want), missing.Body.String())
	assert.Equal(t, strings.ReplaceAll(toJSONString(want), "no_such_tool", "profiles"), toJSONString(got),
		"REST tools/call answers the same unknown-tool response")
}

func toJSONString(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// TestProfilesTool_NotReachableFromCodeExecution: a sandbox script's call_tool
// dispatches upstream server:tool names only, so the admin tool cannot be reached
// from a session it is hidden from (H16).
func TestProfilesTool_NotReachableFromCodeExecution(t *testing.T) {
	f := newProfilesToolFixture(t, func(cfg *config.Config) { cfg.EnableCodeExecution = true })
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"code": `[call_tool('profiles', 'profiles', {operation: 'list'}), call_tool('github', 'profiles', {operation: 'list'}), call_tool('', 'profiles', {operation: 'list'})]`,
	}
	res, err := f.proxy.handleCodeExecution(apiKeyCtx(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	var payload struct {
		Value []struct {
			OK    bool `json:"ok"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"value"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &payload), resultText(t, res))
	require.Len(t, payload.Value, 3)
	for i, r := range payload.Value {
		assert.False(t, r.OK, "call %d must be refused", i)
		assert.NotEmpty(t, r.Error.Code)
	}
	assert.NotContains(t, resultText(t, res), "work-full")
}

// TestProfilesTool_UnwiredViewsAnswerUnavailable: a bare proxy with no
// admin views answers 503 instead of panicking.
func TestProfilesTool_UnwiredViewsAnswerUnavailable(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"operation": "list"}
	res, err := proxy.handleProfiles(apiKeyCtx(), req)
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.JSONEq(t, `{"error":"profiles service unavailable"}`, resultText(t, res))
}

var _ = httpapi.ProfilesErrorBody
var _ = runtime.ErrEvaluatorUnavailable
