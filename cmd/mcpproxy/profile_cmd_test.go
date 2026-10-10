package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// --- fixtures ---------------------------------------------------------------

func readonlyView() runtime.ProfileView {
	return runtime.ProfileView{
		Name: "work-ro2", Title: "Work Read-only",
		Servers: []string{"github", "notion"}, MaxTier: "read",
		SwitchableTo:     &[]string{"work-full"},
		EffectiveServers: []string{"github", "notion"}, EffectiveUnannotated: "deny", EffectiveCodeExecution: false,
		ToolCounts: runtime.ToolCounts{Read: 4, Write: 0, Destructive: 0, UnannotatedHidden: 2},
		ToolCount:  6, Calls24h: 12, Blocked24h: 3,
		UsedBy: &runtime.UsedBy{Clients: []runtime.UsedByClient{{ID: "cursor", Mode: "locked"}}, Tokens: []string{"ci"}},
	}
}

func fullView() runtime.ProfileView {
	return runtime.ProfileView{
		Name: "work-full", Servers: []string{"github"}, MaxTier: "destructive", Unannotated: "as_write",
		EffectiveServers: []string{"github"}, EffectiveUnannotated: "as_write", EffectiveCodeExecution: true,
		ToolCounts: runtime.ToolCounts{Read: 2, Write: 3, Destructive: 1},
		UsedBy:     &runtime.UsedBy{AnonymousProfile: true},
	}
}

func guardBody(t *testing.T) string {
	return `{"success":false,"error":"a client bound to profile work-ro2 could escape it by omitting its credential while require_mcp_auth is off","code":"binding_bypassable_without_auth","bindings":[{"client_id":"cursor","token_name":"client-cursor","profile":"work-ro2","mode":"locked"}],"fixes":[{"kind":"require_mcp_auth"},{"kind":"set_anonymous_profile","target":"work-ro2"}]}`
}

func requireBothGuardFixes(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	msg := err.Error()
	require.Contains(t, msg, "could escape it by omitting its credential")
	require.Contains(t, msg, "Bindings:\n  - cursor → work-ro2 (locked)")
	require.Contains(t, msg, "set require_mcp_auth: true (config or Settings → Security)")
	require.Contains(t, msg, "mcpproxy profile anonymous work-ro2")
}

func withNoDaemon(t *testing.T) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:1"
	cfg.APIKey = "no-daemon-key"
	path := filepath.Join(t.TempDir(), "mcp_config.json")
	require.NoError(t, config.SaveConfig(cfg, path))
	prev := configFile
	configFile = path
	t.Cleanup(func() { configFile = prev })
	t.Setenv("MCPPROXY_TRAY_ENDPOINT", "")
	t.Setenv("MCPPROXY_API_KEY", "no-daemon-key")
}

func indented(t *testing.T, raw string) string {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, json.Indent(&b, []byte(raw), "", "  "))
	return b.String() + "\n"
}

// --- list / show ------------------------------------------------------------

func TestProfileList_TableAndJSON(t *testing.T) {
	data := `{"profiles":[` + mustJSON(t, readonlyView()) + `,` + mustJSON(t, fullView()) + `],"anonymous_profile":"work-full"}`
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles": okResp(data)})

	out, _, err := runCLI(t, GetProfileCommand, "table", "list")
	require.NoError(t, err)
	assertGolden108(t, "profile-list.golden", out)

	out, _, err = runCLI(t, GetProfileCommand, "json", "list")
	require.NoError(t, err)
	require.Equal(t, indented(t, data), out, "JSON must equal the REST data object")
}

func TestProfileList_NonAdminUsedByDash(t *testing.T) {
	v := readonlyView()
	v.UsedBy = nil
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles": okResp(`{"profiles":[` + mustJSON(t, v) + `]}`)})
	out, _, err := runCLI(t, GetProfileCommand, "table", "list")
	require.NoError(t, err)
	require.Contains(t, out, "—")
}

func TestProfileShow_EffectiveOrigins(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/profiles/work-ro2": okResp(mustJSON(t, readonlyView())),
		"GET /api/v1/profiles/legacy":   okResp(mustJSON(t, runtime.ProfileView{Name: "legacy", Servers: []string{"github"}, EffectiveServers: []string{"github"}, EffectiveUnannotated: "as_read", EffectiveCodeExecution: true, IsLegacy: true})),
		"GET /api/v1/profiles/explicit": okResp(mustJSON(t, runtime.ProfileView{Name: "explicit", Servers: []string{"github"}, MaxTier: "read", Unannotated: "as_read", CodeExecution: boolPtr(true), EffectiveServers: []string{"github"}, EffectiveUnannotated: "as_read", EffectiveCodeExecution: true})),
	})
	out, _, err := runCLI(t, GetProfileCommand, "table", "show", "work-ro2")
	require.NoError(t, err)
	require.Contains(t, out, "Profile: work-ro2 (Work Read-only)")
	require.Contains(t, out, "Unannotated: deny (default under max tier read)")
	require.Contains(t, out, "Code execution: off (default under max tier read)")
	require.Contains(t, out, "Used by: client cursor (locked), token ci")
	require.Equal(t, "cli", rec.all()[0].Surface)

	out, _, err = runCLI(t, GetProfileCommand, "table", "show", "legacy")
	require.NoError(t, err)
	require.Contains(t, out, "Code execution: on (inherited)")
	require.Contains(t, out, "Unannotated: as_read (default)")
	require.Contains(t, out, "Legacy: yes")

	out, _, err = runCLI(t, GetProfileCommand, "table", "show", "explicit")
	require.NoError(t, err)
	require.Contains(t, out, "Code execution: on\n")
	require.Contains(t, out, "Unannotated: as_read\n")
}

func TestProfileShow_PrintsCanonicalAsWrite(t *testing.T) {
	data := mustJSON(t, fullView())
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles/work-full": okResp(data)})
	out, _, err := runCLI(t, GetProfileCommand, "json", "show", "work-full")
	require.NoError(t, err)
	require.Contains(t, out, `"unannotated": "as_write"`)
	out, _, err = runCLI(t, GetProfileCommand, "table", "show", "work-full")
	require.NoError(t, err)
	require.Contains(t, out, "Unannotated: as_write\n")
	require.NotContains(t, out, "as-write")
}

const effectiveFixture = `{"profile":"work-ro2","tools":[
 {"server":"github","tool":"list_issues","intrinsic_tier":"read","profile_tier":"read","access":{"visible":true,"callable":true,"reason":""},"classification_stale":true},
 {"server":"github","tool":"create_issue","intrinsic_tier":"write","profile_tier":"write","access":{"visible":false,"callable":false,"reason":"above_tier_cap"},"classification_stale":false},
 {"server":"github","tool":"search_code","intrinsic_tier":"unannotated","profile_tier":"unannotated","access":{"visible":false,"callable":false,"reason":"unannotated_hidden"},"classification_stale":false}],
 "counts":{"visible":1,"hidden":2,"callable":1,"by_reason":{"above_tier_cap":1,"unannotated_hidden":1}},
 "stale_classifications":["github:list_issues","github:gone_tool"]}`

func TestProfileShow_EffectiveStaleMarker(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles/work-ro2/effective-tools": okResp(effectiveFixture)})
	out, _, err := runCLI(t, GetProfileCommand, "table", "show", "work-ro2", "--effective")
	require.NoError(t, err)
	assertGolden108(t, "profile-show-effective.golden", out)
	require.Contains(t, out, "classification ignored — tool is now annotated")
	require.Contains(t, out, "github:gone_tool: classification ignored — tool not found")
}

func TestProfileShow_EffectiveQueryMapping(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles/work-ro2/effective-tools": okResp(`{"profile":"work-ro2","tools":[{"server":"github","tool":"list_issues","intrinsic_tier":"read","profile_tier":"read","access":{"visible":true,"callable":true,"reason":""},"classification_stale":false}],"counts":{"visible":1,"hidden":2}}`)})
	out, _, err := runCLI(t, GetProfileCommand, "table", "show", "work-ro2", "--effective", "--client", "cursor", "--server", "github", "--reason", "above_tier_cap")
	require.NoError(t, err)
	q, err := url.ParseQuery(rec.only(t).RawQuery)
	require.NoError(t, err)
	require.Equal(t, "cursor", q.Get("client"))
	require.Equal(t, "github", q.Get("server"))
	require.Equal(t, "above_tier_cap", q.Get("reason"))
	require.Contains(t, out, "Hidden: 2", "a non-administrator response has no counts.callable")

	_, _, err = runCLI(t, GetProfileCommand, "table", "show", "work-ro2", "--client", "cursor")
	require.Error(t, err)
	require.Contains(t, err.Error(), "need --effective")
}

func TestProfileShow_EffectiveJSONIsData(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles/work-ro2/effective-tools": okResp(effectiveFixture)})
	out, _, err := runCLI(t, GetProfileCommand, "json", "show", "work-ro2", "--effective")
	require.NoError(t, err)
	require.Equal(t, indented(t, effectiveFixture), out)
}

// --- create / update ----------------------------------------------------------

func TestProfileCreate_BodyFieldNames(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/profiles": createdResp(`{"profile":` + mustJSON(t, readonlyView()) + `,"warnings":["server linear is not configured"]}`)})
	out, errOut, err := runCLI(t, GetProfileCommand, "table", "create", "work-ro2",
		"--servers", "github,notion", "--title", "Work Read-only", "--description", "d", "--max-tier", "read",
		"--unannotated", "as-write", "--allow", "github:list_*", "--deny", "github:delete_*",
		"--code-execution", "on", "--management-tools", "off", "--switchable-to", "work-full,work-ro")
	require.NoError(t, err)
	require.Contains(t, out, "Created profile work-ro2")
	require.Contains(t, errOut, "warning: server linear is not configured")

	req := rec.only(t)
	require.Equal(t, http.MethodPost, req.Method)
	require.Equal(t, "cli", req.Surface)
	body := req.jsonBody(t)
	require.Equal(t, "work-ro2", body["name"])
	require.Equal(t, []any{"github", "notion"}, body["servers"])
	require.Equal(t, "Work Read-only", body["title"])
	require.Equal(t, "d", body["description"])
	require.Equal(t, "read", body["max_tier"])
	require.Equal(t, "as_write", body["unannotated"], "the kebab alias must be sent canonically")
	require.Equal(t, map[string]any{"allow": []any{"github:list_*"}, "deny": []any{"github:delete_*"}}, body["tools"])
	require.Equal(t, true, body["code_execution"])
	require.Equal(t, false, body["management_tools"])
	require.Equal(t, []any{"work-full", "work-ro"}, body["switchable_to"])
}

func TestProfileCreate_TriStateAndEmptySwitchable(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/profiles": createdResp(`{"profile":` + mustJSON(t, fullView()) + `,"warnings":[]}`)})
	_, _, err := runCLI(t, GetProfileCommand, "table", "create", "p", "--servers", "github", "--unannotated", "as_write",
		"--code-execution", "inherit", "--switchable-to", "")
	require.NoError(t, err)
	body := rec.only(t).jsonBody(t)
	require.Equal(t, "as_write", body["unannotated"])
	require.NotContains(t, body, "code_execution", "inherit omits the field")
	require.NotContains(t, body, "management_tools")
	require.Equal(t, []any{}, body["switchable_to"], "--switchable-to '' sends [] (explicitly none)")

	rec2 := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/profiles": createdResp(`{"profile":` + mustJSON(t, fullView()) + `,"warnings":[]}`)})
	_, _, err = runCLI(t, GetProfileCommand, "table", "create", "p", "--servers", "github")
	require.NoError(t, err)
	require.NotContains(t, rec2.only(t).jsonBody(t), "switchable_to")

	_, _, err = runCLI(t, GetProfileCommand, "table", "create", "p", "--servers", "github", "--code-execution", "maybe")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
}

func TestProfileUpdate_ReadModifyWrite(t *testing.T) {
	stored := readonlyView()
	stored.Tools = &config.ProfileToolRules{Allow: []string{"github:list_*"}, Classify: map[string]string{"github:search_code": "read"}}
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/profiles/work-ro2": okResp(mustJSON(t, stored)),
		"PUT /api/v1/profiles/work-ro2": okResp(`{"profile":` + mustJSON(t, stored) + `,"warnings":[]}`),
	})
	out, _, err := runCLI(t, GetProfileCommand, "table", "update", "work-ro2",
		"--add-server", "linear", "--remove-server", "notion", "--add-deny", "github:delete_*", "--clear-title", "--max-tier", "write", "--clear-classify")
	require.NoError(t, err)
	require.Contains(t, out, "Updated profile work-ro2")
	reqs := rec.all()
	require.Len(t, reqs, 2)
	require.Equal(t, http.MethodGet, reqs[0].Method)
	require.Equal(t, http.MethodPut, reqs[1].Method)
	body := reqs[1].jsonBody(t)
	for _, derived := range []string{"effective_servers", "effective_unannotated", "effective_code_execution", "tool_counts", "tool_count", "calls_24h", "blocked_24h", "used_by", "is_legacy"} {
		require.NotContains(t, body, derived, "only stored fields are written back")
	}
	require.Equal(t, "work-ro2", body["name"])
	require.Equal(t, []any{"github", "linear"}, body["servers"])
	require.NotContains(t, body, "title")
	require.Equal(t, "write", body["max_tier"])
	require.Equal(t, []any{"work-full"}, body["switchable_to"])
	require.Equal(t, map[string]any{"allow": []any{"github:list_*"}, "deny": []any{"github:delete_*"}}, body["tools"])
}

func TestProfileUpdate_ConflictingFlagsSendNothing(t *testing.T) {
	for _, args := range [][]string{
		{"--servers", "a", "--add-server", "b"},
		{"--servers", "a", "--remove-server", "b"},
		{"--allow", "a:b", "--add-allow", "c:d"},
		{"--deny", "a:b", "--remove-deny", "c:d"},
		{"--title", "x", "--clear-title"},
		{"--max-tier", "read", "--clear-max-tier"},
		{"--code-execution", "on", "--clear-code-execution"},
		{"--switchable-to", "a", "--clear-switchable-to"},
	} {
		rec := newRESTRecorder(t, map[string]cannedResponse{})
		_, _, err := runCLI(t, GetProfileCommand, "table", append([]string{"update", "work-ro2"}, args...)...)
		require.Error(t, err, args)
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
		require.Empty(t, rec.all(), "a conflicting flag pair must be rejected before any request: %v", args)
	}
}

func TestProfileUpdate_AddThenClearClears(t *testing.T) {
	stored := readonlyView()
	stored.Tools = &config.ProfileToolRules{Allow: []string{"github:a"}}
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/profiles/work-ro2": okResp(mustJSON(t, stored)),
		"PUT /api/v1/profiles/work-ro2": okResp(`{"profile":` + mustJSON(t, stored) + `,"warnings":[]}`),
	})
	_, _, err := runCLI(t, GetProfileCommand, "table", "update", "work-ro2", "--add-allow", "github:b", "--clear-allow")
	require.NoError(t, err)
	require.NotContains(t, rec.all()[1].jsonBody(t), "tools", "clear runs last and an empty rule set is dropped")
}

// --- rename / delete / classify ------------------------------------------------

func TestProfileDelete_InUsePrintsUsedByExit1(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/profiles/work-ro2": refuse(http.StatusConflict,
		`{"success":false,"error":"profile in use","code":"profile_in_use","used_by":{"clients":[{"id":"cursor","mode":"locked"}],"tokens":["ci"],"anonymous_profile":false}}`)})
	_, _, err := runCLI(t, GetProfileCommand, "table", "delete", "work-ro2")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), "profile in use")
	require.Contains(t, err.Error(), "cursor")
	require.Contains(t, err.Error(), "locked")
	require.Contains(t, err.Error(), "ci")
	require.Empty(t, rec.only(t).RawQuery)
}

func TestProfileDelete_AnonymousRefusedEvenWithForce(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/profiles/work-full": refuse(http.StatusConflict,
		`{"success":false,"error":"profile is the anonymous_profile; pass reassign_to or change anonymous_profile first","code":"profile_is_anonymous_profile","used_by":{"clients":[],"tokens":[],"anonymous_profile":true}}`)})
	_, _, err := runCLI(t, GetProfileCommand, "table", "delete", "work-full", "--force")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), "profile is the anonymous_profile")
	require.Contains(t, err.Error(), "anonymous_profile")
	q, _ := url.ParseQuery(rec.only(t).RawQuery)
	require.Equal(t, "true", q.Get("force"))
}

func TestProfileDelete_ReassignPrintsMoved(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/profiles/old": okResp(`{"deleted":"old","moved":{"clients":["cursor"],"tokens":["ci"]},"anonymous_profile_moved_to":"new"}`)})
	out, _, err := runCLI(t, GetProfileCommand, "table", "delete", "old", "--reassign-to", "new")
	require.NoError(t, err)
	require.Contains(t, out, "Deleted profile old")
	require.Contains(t, out, "Moved clients: cursor")
	require.Contains(t, out, "anonymous_profile moved to new")
	q, _ := url.ParseQuery(rec.only(t).RawQuery)
	require.Equal(t, "new", q.Get("reassign_to"))
}

func TestProfileRename_PrintsMoved(t *testing.T) {
	v := readonlyView()
	v.Name = "work-ro3"
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/profiles/work-ro2/rename": okResp(`{"profile":` + mustJSON(t, v) + `,"moved":{"clients":["cursor"],"tokens":["ci"]}}`)})
	out, _, err := runCLI(t, GetProfileCommand, "table", "rename", "work-ro2", "work-ro3")
	require.NoError(t, err)
	require.Contains(t, out, "Renamed profile work-ro2 → work-ro3")
	require.Contains(t, out, "Moved clients: cursor")
	require.Contains(t, out, "Moved tokens: ci")
	require.Equal(t, map[string]any{"new_name": "work-ro3"}, rec.only(t).jsonBody(t))
}

func TestProfileClassify_SetsAndClears(t *testing.T) {
	stored := readonlyView()
	stored.Tools = &config.ProfileToolRules{Classify: map[string]string{"github:search_code": "read", "notion:x": "write"}}
	routes := map[string]cannedResponse{
		"GET /api/v1/profiles/work-ro2": okResp(mustJSON(t, stored)),
		"PUT /api/v1/profiles/work-ro2": okResp(`{"profile":` + mustJSON(t, stored) + `,"warnings":[]}`),
	}
	rec := newRESTRecorder(t, routes)
	_, _, err := runCLI(t, GetProfileCommand, "table", "classify", "work-ro2", "github:list_issues", "write")
	require.NoError(t, err)
	tools := rec.all()[1].jsonBody(t)["tools"].(map[string]any)
	require.Equal(t, map[string]any{"github:search_code": "read", "notion:x": "write", "github:list_issues": "write"}, tools["classify"])

	rec = newRESTRecorder(t, routes)
	_, _, err = runCLI(t, GetProfileCommand, "table", "classify", "work-ro2", "github:search_code", "--clear")
	require.NoError(t, err)
	tools = rec.all()[1].jsonBody(t)["tools"].(map[string]any)
	require.Equal(t, map[string]any{"notion:x": "write"}, tools["classify"])

	rec = newRESTRecorder(t, routes)
	_, _, err = runCLI(t, GetProfileCommand, "table", "classify", "work-ro2", "github:search_code")
	require.Error(t, err, "a tier is required without --clear")
	require.Empty(t, rec.all())
}

// --- try -----------------------------------------------------------------------

const tryFixture = `{"results":[{"tool":{"name":"github:create_issue","server_name":"github","annotations":{"readOnlyHint":false,"destructiveHint":false}},"score":1.2},{"tool":{"name":"github:list_issues","server_name":"github","annotations":{"readOnlyHint":true}},"score":1.0}],"hidden_by_profile":2,"hidden":[{"server":"github","tool":"delete_repo","reason":"above_tier_cap"}],"hidden_truncated":true}`

func TestProfileTry_DraftFromSavedOrNew(t *testing.T) {
	stored := readonlyView()
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/profiles/work-ro2": okResp(mustJSON(t, stored)),
		"POST /api/v1/profiles/try":     okResp(tryFixture),
	})
	out, _, err := runCLI(t, GetProfileCommand, "table", "try", "work-ro2", "--query", "issue", "--limit", "5",
		"--set", "max_tier=write", "--set", "tools.classify.github:search_code=read", "--set", "tools.deny=github:x,github:y",
		"--set", "code_execution=inherit", "--set", "switchable_to=")
	require.NoError(t, err)
	assertGolden108(t, "profile-try.golden", out)
	reqs := rec.all()
	require.Len(t, reqs, 2)
	body := reqs[1].jsonBody(t)
	require.Equal(t, "issue", body["query"])
	require.EqualValues(t, 5, body["limit"])
	draft := body["profile"].(map[string]any)
	require.Equal(t, "write", draft["max_tier"])
	require.Equal(t, []any{"github", "notion"}, draft["servers"], "the draft starts from the saved profile")
	require.Equal(t, map[string]any{"deny": []any{"github:x", "github:y"}, "classify": map[string]any{"github:search_code": "read"}}, draft["tools"])
	require.Equal(t, []any{}, draft["switchable_to"])

	// A draft can be tried before it exists.
	rec = newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/profiles/newdraft": refuse(http.StatusNotFound, `{"success":false,"error":"profile not found"}`),
		"POST /api/v1/profiles/try":     okResp(tryFixture),
	})
	_, _, err = runCLI(t, GetProfileCommand, "table", "try", "newdraft", "--query", "issue", "--set", "servers=github")
	require.NoError(t, err)
	body = rec.all()[1].jsonBody(t)
	require.NotContains(t, body, "limit")
	require.Equal(t, map[string]any{"name": "newdraft", "servers": []any{"github"}}, body["profile"])
}

func TestProfileTry_UnknownSetKeyExit1(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles/work-ro2": okResp(mustJSON(t, readonlyView()))})
	_, _, err := runCLI(t, GetProfileCommand, "table", "try", "work-ro2", "--query", "x", "--set", "colour=red")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), `unknown draft field "colour"; valid: servers, title`)
	for _, r := range rec.all() {
		require.NotEqual(t, http.MethodPost, r.Method, "nothing is tried with a bad draft")
	}
}

func TestProfileTry_JSONIsData(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/profiles/work-ro2": okResp(mustJSON(t, readonlyView())),
		"POST /api/v1/profiles/try":     okResp(tryFixture),
	})
	out, _, err := runCLI(t, GetProfileCommand, "json", "try", "work-ro2", "--query", "issue")
	require.NoError(t, err)
	require.Equal(t, indented(t, tryFixture), out, "two REST calls: the last call's data is printed")
}

// --- anonymous -----------------------------------------------------------------

func TestProfileAnonymous_GetSetClear(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/profiles": okResp(`{"profiles":[],"anonymous_profile":"work-ro2"}`),
		"PATCH /api/v1/config": okResp(`{"success":true,"applied_immediately":true,"requires_restart":false,"changed_fields":["anonymous_profile"]}`),
	})
	out, _, err := runCLI(t, GetProfileCommand, "table", "anonymous")
	require.NoError(t, err)
	require.Equal(t, "Anonymous callers: work-ro2\n", out)

	out, _, err = runCLI(t, GetProfileCommand, "table", "anonymous", "work-full")
	require.NoError(t, err)
	require.Equal(t, "Anonymous callers: work-full\n", out)
	out, _, err = runCLI(t, GetProfileCommand, "table", "anonymous", "--clear")
	require.NoError(t, err)
	require.Contains(t, out, "unconfined")
	reqs := rec.all()
	require.Equal(t, map[string]any{"anonymous_profile": "work-full"}, reqs[1].jsonBody(t))
	require.Equal(t, map[string]any{"anonymous_profile": ""}, reqs[2].jsonBody(t))

	before := len(rec.all())
	_, _, err = runCLI(t, GetProfileCommand, "table", "anonymous", "x", "--clear")
	require.Error(t, err)
	require.Len(t, rec.all(), before, "name + --clear sends nothing")
}

func TestProfileAnonymous_UnconfinedWhenUnset(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles": okResp(`{"profiles":[]}`)})
	out, _, err := runCLI(t, GetProfileCommand, "table", "anonymous")
	require.NoError(t, err)
	require.Equal(t, "Anonymous callers: unconfined (all servers when require_mcp_auth is off)\n", out)
}

// --- refusals -------------------------------------------------------------------

func TestProfileMutations_GuardRefusalExit1WithBothFixes(t *testing.T) {
	stored := mustJSON(t, readonlyView())
	guard := refuse(http.StatusConflict, guardBody(t))
	routes := map[string]cannedResponse{
		"GET /api/v1/profiles/work-ro2":         okResp(stored),
		"POST /api/v1/profiles":                 guard,
		"PUT /api/v1/profiles/work-ro2":         guard,
		"POST /api/v1/profiles/work-ro2/rename": guard,
		"DELETE /api/v1/profiles/work-ro2":      guard,
		"PATCH /api/v1/config":                  guard,
	}
	for name, args := range map[string][]string{
		"create":    {"create", "new", "--servers", "github"},
		"update":    {"update", "work-ro2", "--add-server", "linear"},
		"rename":    {"rename", "work-ro2", "work-ro3"},
		"delete":    {"delete", "work-ro2", "--reassign-to", "work-full"},
		"classify":  {"classify", "work-ro2", "github:x", "read"},
		"anonymous": {"anonymous", "work-ro2"},
	} {
		t.Run(name, func(t *testing.T) {
			newRESTRecorder(t, routes)
			out, _, err := runCLI(t, GetProfileCommand, "table", args...)
			requireBothGuardFixes(t, err)
			require.NotContains(t, out, "Created", "a refused write prints no success line")
		})
	}
}

func TestProfileValidator400_PrintsTextAndField(t *testing.T) {
	text := `profile "work-ro2": tools.allow "github:" is not a valid server:tool pattern`
	newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/profiles": refuse(http.StatusBadRequest, `{"success":false,"error":`+mustJSON(t, text)+`,"field":"tools.allow"}`)})
	_, _, err := runCLI(t, GetProfileCommand, "table", "create", "work-ro2", "--servers", "github", "--allow", "github:")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), text)
	require.Contains(t, err.Error(), "field: tools.allow")
}

func TestProfileCommands_NoDaemonExit1(t *testing.T) {
	withNoDaemon(t)
	for _, args := range [][]string{{"list"}, {"show", "x"}, {"create", "x", "--servers", "a"}, {"delete", "x"}, {"anonymous"}} {
		_, _, err := runCLI(t, GetProfileCommand, "table", args...)
		require.Error(t, err, args)
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
		require.Contains(t, err.Error(), "profile requires running daemon. Start with: mcpproxy serve")
	}
}

func TestProfileGroup_LongShowsTheGoalFlow(t *testing.T) {
	long := GetProfileCommand().Long
	for _, line := range []string{
		"mcpproxy profile create work-readonly --servers github,notion --max-tier read",
		"mcpproxy client set-profile cursor work-readonly --lock",
		"mcpproxy token create --name ci --profile work-readonly",
		"mcpproxy profile show work-readonly --effective",
	} {
		require.Contains(t, long, line)
	}
}

func TestRootCommand_BuildsWithoutPanic(t *testing.T) {
	// A duplicate flag registration panics at Cobra parse time; build and walk
	// every 108-g command so one is caught here, not in a release.
	root := &cobra.Command{Use: "mcpproxy"}
	root.AddCommand(GetProfileCommand(), GetClientCommand(), GetAccessCommand(), GetTokenCommand())
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.LocalFlags()
		c.InheritedFlags()
		for _, s := range c.Commands() {
			walk(s)
		}
	}
	require.NotPanics(t, func() { walk(root) })
}

// UX-07: the preview is policy-only. The header keeps "allowed by profile"
// apart from "callable", the ACCESS column calls a gated tool "held", and the
// held tools come with the exact access-explain command.
func TestProfileShow_EffectiveSeparatesAllowedCallableAndHeld(t *testing.T) {
	const fixture = `{"profile":"qa-read","tools":[
 {"server":"lib","tool":"ok","intrinsic_tier":"read","profile_tier":"read","access":{"visible":true,"callable":true,"reason":""},"classification_stale":false},
 {"server":"lib","tool":"pending","intrinsic_tier":"read","profile_tier":"read","access":{"visible":true,"callable":false,"reason":"tool_approval"},"classification_stale":false}],
 "counts":{"visible":2,"hidden":1,"callable":1,"by_reason":{"tool_approval":1,"above_tier_cap":1}}}`
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles/qa-read/effective-tools": okResp(fixture)})
	out, _, err := runCLI(t, GetProfileCommand, "table", "show", "qa-read", "--effective")
	require.NoError(t, err)
	require.Contains(t, out, "Profile: qa-read (allowed by profile 2, hidden 1, callable 1, held 1)")
	require.Regexp(t, `pending\s+read\s+read\s+held\s+tool_approval`, out)
	require.Contains(t, out, "1 tool held: allowed by the profile but not callable yet. Run: mcpproxy access explain --profile qa-read --tool <server:tool>")
}

func TestProfileShow_EffectiveNonAdminHasNoHeldClaims(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/profiles/p/effective-tools": okResp(`{"profile":"p","tools":[],"counts":{"visible":2,"hidden":1}}`)})
	out, _, err := runCLI(t, GetProfileCommand, "table", "show", "p", "--effective")
	require.NoError(t, err)
	require.Contains(t, out, "Profile: p (allowed by profile 2, hidden 1)")
	require.NotContains(t, out, "held")
}
