package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	cursorRow = `{"id":"cursor","display_name":"Cursor","kind":"supported","state":"connected_seen","connected":true,"config_path":"/Users/test/.cursor/mcp.json","display_path":"~/.cursor/mcp.json","last_seen":"2026-09-25T06:10:00Z","active_sessions":1,"calls_24h":38,"blocked_24h":3,"reload_hint":"Restart Cursor","credential_state":"client","token_name":"client-cursor","profile":"work-ro2","profile_title":"Work Read-only","profile_mode":"locked","profile_source":"pin","expires_at":"2027-09-25T06:10:00Z","sessions":[{"id":"S1","work_session_id":"ws-1","started_at":"2026-09-25T06:00:00Z","last_activity":"2026-09-25T06:10:00Z","profile":"work-ro2","profile_source":"pin"}]}`
	codexRow  = `{"id":"codex","display_name":"Codex","kind":"supported","state":"not_connected","connected":false,"display_path":"~/.codex/config.toml","last_seen":null,"active_sessions":0,"calls_24h":0,"blocked_24h":0,"credential_state":"none"}`
)

// --- client list / show --------------------------------------------------------

const client109ListGolden = `CLIENT       STATE           LAST SEEN             SESSIONS  CALLS 24H  CONFIG PATH
Claude Code  connected_seen  2026-09-25T06:10:00Z  1         38         ~/.claude.json
`

// Spec 109's columns keep their bytes: the new columns are only appended.
func TestClientList_AppendedColumnsKeep109Columns(t *testing.T) {
	legacy := `{"clients":[{"id":"claude-code","display_name":"Claude Code","state":"connected_seen","last_seen":"2026-09-25T06:10:00Z","active_sessions":1,"calls_24h":38,"display_path":"~/.claude.json"}],"routing":{"routing_mode":"retrieve_tools"}}`
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/clients": okResp(legacy)})
	out, _, err := runCLI(t, GetClientCommand, "table", "list")
	require.NoError(t, err)

	want := strings.Split(strings.TrimRight(client109ListGolden, "\n"), "\n")
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, got, len(want))
	cut := strings.Index(want[0], "CONFIG PATH")
	for i := range want {
		require.Equal(t, want[i][:cut], got[i][:cut], "the first six columns are byte-identical (line %d)", i)
		require.True(t, strings.HasPrefix(got[i][cut:], strings.TrimSpace(want[i][cut:])), "CONFIG PATH cell kept (line %d)", i)
	}
	require.Regexp(t, `CONFIG PATH +CREDENTIAL +PROFILE +MODE +SOURCE +BLOCKED 24H$`, got[0])
}

func TestClientList_BindingColumnsGolden(t *testing.T) {
	data := `{"clients":[` + cursorRow + `,` + codexRow + `],"routing":{"routing_mode":"retrieve_tools"},"warnings":[]}`
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/clients": okResp(data)})
	out, _, err := runCLI(t, GetClientCommand, "table", "list")
	require.NoError(t, err)
	assertGolden108(t, "client-list.golden", out)
	require.Contains(t, out, "pin", "SOURCE = profile_source")

	out, _, err = runCLI(t, GetClientCommand, "json", "list")
	require.NoError(t, err)
	require.Equal(t, indented(t, data), out)
}

func TestClientList_ProfileAndClientFilters(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		query url.Values
	}{
		{[]string{"--profile", "work-readonly"}, url.Values{"profile": {"work-readonly"}}},
		{[]string{"--profile", "-"}, url.Values{"profile": {"-"}}},
		{[]string{"--client", "cursor"}, url.Values{"client": {"cursor"}}},
		{nil, url.Values{}},
	} {
		rec := newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/clients": okResp(`{"clients":[],"routing":{},"warnings":[]}`)})
		_, _, err := runCLI(t, GetClientCommand, "table", append([]string{"list"}, tc.args...)...)
		require.NoError(t, err)
		q, err := url.ParseQuery(rec.only(t).RawQuery)
		require.NoError(t, err)
		require.Equal(t, tc.query, q)
	}
}

func TestClientList_WarningsToStderrOnlyInTable(t *testing.T) {
	data := `{"clients":[` + cursorRow + `],"routing":{},"warnings":[{"code":"client_holds_admin_key","severity":"warn","client_id":"codex","message":"codex holds the admin API key; upgrade it to a client credential","action":{"kind":"upgrade_admin_key_holders"}},{"code":"client_rotation_pending","severity":"info","client_id":"cursor","message":"a credential rotation of cursor has not finished; reconnect it","action":{"kind":"reconnect_client","target":"cursor"}}]}`
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/clients": okResp(data)})

	out, errOut, err := runCLI(t, GetClientCommand, "table", "list")
	require.NoError(t, err)
	require.NotContains(t, out, "admin API key")
	require.Contains(t, errOut, "warning [client_holds_admin_key]: codex holds the admin API key")
	require.Contains(t, errOut, "fix: mcpproxy client upgrade-admin-key-holders")
	require.Contains(t, errOut, "fix: mcpproxy client rotate cursor --finalize")

	out, errOut, err = runCLI(t, GetClientCommand, "json", "list")
	require.NoError(t, err)
	require.Empty(t, errOut, "in json mode the warnings are inside data")
	require.Contains(t, out, `"warnings"`)
}

func TestClientShow_BindingLinesAndSessionColumns(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/clients/cursor": okResp(cursorRow)})
	out, _, err := runCLI(t, GetClientCommand, "table", "show", "cursor")
	require.NoError(t, err)
	assertGolden108(t, "client-show.golden", out)
	require.Contains(t, out, "Profile: Work Read-only (work-ro2), locked, source pin")
}

// --- set-profile ---------------------------------------------------------------

func TestClientSetProfile_ModeOmittedWithoutFlag(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"PUT /api/v1/clients/cursor/binding": okResp(`{"client":` + cursorRow + `,"warnings":[]}`)})
	out, _, err := runCLI(t, GetClientCommand, "table", "set-profile", "cursor", "work-ro2")
	require.NoError(t, err)
	req := rec.only(t)
	require.Equal(t, "cli", req.Surface)
	require.Equal(t, map[string]any{"profile": "work-ro2"}, req.jsonBody(t), "no mode key without --lock/--switchable")
	require.Contains(t, out, "Client cursor: Work Read-only (work-ro2), locked, source pin")

	rec = newRESTRecorder(t, map[string]cannedResponse{"PUT /api/v1/clients/cursor/binding": okResp(`{"client":` + cursorRow + `,"warnings":[]}`)})
	_, _, err = runCLI(t, GetClientCommand, "table", "set-profile", "cursor", "work-ro2", "--lock")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"profile": "work-ro2", "mode": "locked"}, rec.only(t).jsonBody(t))

	rec = newRESTRecorder(t, map[string]cannedResponse{"PUT /api/v1/clients/cursor/binding": okResp(`{"client":` + cursorRow + `,"warnings":[]}`)})
	_, _, err = runCLI(t, GetClientCommand, "table", "set-profile", "cursor", "work-ro2", "--switchable")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"profile": "work-ro2", "mode": "switchable"}, rec.only(t).jsonBody(t))

	_, _, err = runCLI(t, GetClientCommand, "table", "set-profile", "cursor", "work-ro2", "--lock", "--switchable")
	require.Error(t, err)
}

func TestClientSetProfile_AllSendsEmpty(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"PUT /api/v1/clients/cursor/binding": okResp(`{"client":` + cursorRow + `,"warnings":[]}`)})
	_, _, err := runCLI(t, GetClientCommand, "table", "set-profile", "cursor", "all")
	require.NoError(t, err)
	body := rec.only(t).jsonBody(t)
	require.Equal(t, "", body["profile"], "the literal all is never sent as a profile name")
}

func TestClientSetProfile_NoCredentialExit1WithHint(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"PUT /api/v1/clients/codex/binding": refuse(http.StatusConflict,
		`{"success":false,"error":"client codex has no active client credential; connect it with a profile first","code":"no_client_credential"}`)})
	_, _, err := runCLI(t, GetClientCommand, "table", "set-profile", "codex", "work-ro2")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), "has no active client credential")
	require.Contains(t, err.Error(), "hint: mcpproxy connect codex --profile work-ro2")
}

func TestClientSetProfile_GuardExit1BothFixes(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"PUT /api/v1/clients/cursor/binding": refuse(http.StatusConflict, guardBody(t))})
	out, _, err := runCLI(t, GetClientCommand, "table", "set-profile", "cursor", "work-ro2", "--lock")
	requireBothGuardFixes(t, err)
	require.Empty(t, out)
}

func TestClientSetProfile_BulkUsesFromToProfile(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/clients/bulk-assign": okResp(`{"moved":["cursor"],"skipped":[{"client_id":"codex","code":"no_client_credential","error":"client codex has no active client credential"},{"client_id":"zed","code":"binding_bypassable_without_auth","error":"guard"}]}`)})
	out, errOut, err := runCLI(t, GetClientCommand, "table", "set-profile", "--from-profile", "work-full", "--to-profile", "all", "--switchable")
	require.NoError(t, err, "skipped clients still exit 0")
	require.Equal(t, map[string]any{"from_profile": "work-full", "to_profile": "", "mode": "switchable"}, rec.only(t).jsonBody(t))
	require.Contains(t, out, "Moved: cursor")
	require.Contains(t, errOut, "skipped codex: no_client_credential")
	require.Contains(t, errOut, "skipped zed: binding_bypassable_without_auth")

	rec = newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/clients/bulk-assign": okResp(`{"moved":[],"skipped":[]}`)})
	_, _, err = runCLI(t, GetClientCommand, "table", "set-profile", "--from-profile", "a", "--to-profile", "b")
	require.NoError(t, err)
	require.NotContains(t, rec.only(t).jsonBody(t), "mode", "mode is unchanged unless given")
}

func TestClientSetProfile_FromToFlagsNotRegistered(t *testing.T) {
	for _, c := range GetClientCommand().Commands() {
		if c.Name() != "set-profile" {
			continue
		}
		require.Nil(t, c.Flags().Lookup("from"), "--from is Spec 109-k's time-range alias, not registered here")
		require.Nil(t, c.Flags().Lookup("to"))
		require.NotNil(t, c.Flags().Lookup("from-profile"))
		require.NotNil(t, c.Flags().Lookup("to-profile"))
		return
	}
	t.Fatal("set-profile not registered")
}

func TestClientSetProfile_PositionalAndBulkExclusive(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{})
	for _, args := range [][]string{
		{"set-profile", "cursor", "work-ro2", "--from-profile", "a", "--to-profile", "b"},
		{"set-profile", "--from-profile", "a"},
		{"set-profile", "--to-profile", "b"},
		{"set-profile", "cursor"},
		{"set-profile"},
	} {
		_, _, err := runCLI(t, GetClientCommand, "table", args...)
		require.Error(t, err, args)
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
	}
	require.Empty(t, rec.all())
}

// --- lock / unlock -------------------------------------------------------------

func TestClientLockUnlock_GetThenPut(t *testing.T) {
	switchable := strings.Replace(cursorRow, `"profile_mode":"locked"`, `"profile_mode":"switchable"`, 1)
	for _, tc := range []struct {
		cmd, mode, row string
	}{{"lock", "locked", cursorRow}, {"unlock", "switchable", switchable}} {
		rec := newRESTRecorder(t, map[string]cannedResponse{
			"GET /api/v1/clients/cursor":         okResp(cursorRow),
			"PUT /api/v1/clients/cursor/binding": okResp(`{"client":` + tc.row + `,"warnings":[]}`),
		})
		out, _, err := runCLI(t, GetClientCommand, "table", tc.cmd, "cursor")
		require.NoError(t, err)
		reqs := rec.all()
		require.Len(t, reqs, 2)
		require.Equal(t, http.MethodGet, reqs[0].Method)
		require.Equal(t, map[string]any{"profile": "work-ro2", "mode": tc.mode}, reqs[1].jsonBody(t))
		require.Contains(t, out, "Client cursor:")
	}
}

func TestClientLock_AllServersPrintsHint(t *testing.T) {
	allRow := strings.Replace(cursorRow, `"profile":"work-ro2"`, `"profile":""`, 1)
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/clients/cursor":         okResp(allRow),
		"PUT /api/v1/clients/cursor/binding": refuse(http.StatusBadRequest, `{"success":false,"error":"All servers cannot be locked; it is always switchable","field":"mode"}`),
	})
	_, _, err := runCLI(t, GetClientCommand, "table", "lock", "cursor")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), "cannot be locked")
	require.Contains(t, err.Error(), "field: mode")
	require.Contains(t, err.Error(), "hint: mcpproxy client set-profile cursor <profile> --lock")
}

func TestClientLock_NoCredentialPrintsConnectHint(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/clients/codex":         okResp(codexRow),
		"PUT /api/v1/clients/codex/binding": refuse(http.StatusConflict, `{"success":false,"error":"client codex has no active client credential; connect it with a profile first","code":"no_client_credential"}`),
	})
	_, _, err := runCLI(t, GetClientCommand, "table", "lock", "codex")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no active client credential")
	require.Contains(t, err.Error(), "mcpproxy connect codex --profile")
}

// --- add -----------------------------------------------------------------------

const addedSecret = "mcp_cli_0123456789abcdef0123456789abcdef"

func TestClientAdd_PrintsCredentialOnceAndHeaderSnippet(t *testing.T) {
	dev := `{"id":"dev-laptop","display_name":"Dev laptop","kind":"custom","state":"never_connected","credential_state":"client","token_name":"client-dev-laptop","profile":"work-ro2","profile_title":"Work Read-only","profile_mode":"locked","profile_source":"pin"}`
	resp := `{"client":` + dev + `,"credential":"` + addedSecret + `","snippet":{"generic_http":"{\"mcpServers\":{\"mcpproxy\":{\"url\":\"http://127.0.0.1:8080/mcp\",\"headers\":{\"X-API-Key\":\"` + addedSecret + `\"}}}}","header_name":"X-API-Key"}}`
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/clients": createdResp(resp)})
	out, errOut, err := runCLI(t, GetClientCommand, "table", "add", "dev-laptop", "--display-name", "Dev laptop", "--profile", "work-ro2", "--lock", "--expires-in", "90d")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"id": "dev-laptop", "display_name": "Dev laptop", "profile": "work-ro2", "mode": "locked", "expires_in": "90d"}, rec.only(t).jsonBody(t))
	require.Equal(t, 2, strings.Count(out, addedSecret), "the credential appears in the Credential line and once inside the snippet")
	require.Contains(t, out, "Credential: "+addedSecret)
	require.Contains(t, out, "Store it now; it is not shown again.")
	require.Contains(t, out, "Config snippet (header X-API-Key):")
	require.NotContains(t, out, "apikey=")
	require.NotContains(t, errOut, "mcp_cli_", "the secret goes to stdout only")
}

func TestClientAdd_400FieldIDPassesThrough(t *testing.T) {
	text := `client id "Bad Id" is invalid: use lower-case letters, digits, '-' or '_' (at most 56 characters)`
	newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/clients": refuse(http.StatusBadRequest, `{"success":false,"error":`+mustJSON(t, text)+`,"field":"id"}`)})
	_, _, err := runCLI(t, GetClientCommand, "table", "add", "Bad Id")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), text)
	require.Contains(t, err.Error(), "field: id")
}

func TestClientAdd_AllProfileSendsEmptyAndBodyOmitsUnsetFields(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/clients": createdResp(`{"client":` + codexRow + `,"credential":"` + addedSecret + `","snippet":{"generic_http":"{}","header_name":"X-API-Key"}}`)})
	_, _, err := runCLI(t, GetClientCommand, "table", "add", "ci", "--profile", "all")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"id": "ci", "profile": ""}, rec.only(t).jsonBody(t))
}

// --- rotate --------------------------------------------------------------------

const previewFixture = `{"client":"cursor","config_path":"/Users/test/.cursor/mcp.json","display_path":"~/.cursor/mcp.json","format":"json","entry_text":"{\"mcpproxy\":{\"headers\":{\"X-API-Key\":\"mcp_cli_••••\"}}}","credential":"mcp_cli_••••","profile":"","mode":"","precondition_token":"tok-1","contains_api_key":false}`

func TestClientRotate_SupportedPreviewConfirmThenRotate(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/clients/cursor":         okResp(cursorRow),
		"GET /api/v1/connect/cursor/preview": okResp(previewFixture),
		"POST /api/v1/clients/cursor/rotate": okResp(`{"client":` + cursorRow + `,"connect":{"success":true,"client":"cursor","action":"updated","credential":"mcp_cli_••••","reload_hint":"Restart Cursor"},"rotation":{"state":"finalized"}}`),
	})
	out, _, err := runCLI(t, GetClientCommand, "table", "rotate", "cursor", "--yes")
	require.NoError(t, err)
	reqs := rec.all()
	require.Len(t, reqs, 3)
	require.Equal(t, "/api/v1/connect/cursor/preview", reqs[1].Path)
	require.Equal(t, map[string]any{"precondition_token": "tok-1"}, reqs[2].jsonBody(t))
	require.Contains(t, out, "~/.cursor/mcp.json")
	require.Contains(t, out, "Rotation: finalized")
	require.Contains(t, out, "Credential: mcp_cli_••••")
}

func TestClientRotate_NonTTYWithoutYesExit1NoPost(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/clients/cursor":         okResp(cursorRow),
		"GET /api/v1/connect/cursor/preview": okResp(previewFixture),
	})
	_, _, err := runCLI(t, GetClientCommand, "table", "rotate", "cursor")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), "confirmation required; pass --yes")
	for _, r := range rec.all() {
		require.NotEqual(t, http.MethodPost, r.Method)
	}
}

func TestClientRotate_PreconditionFailedExit1(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/clients/cursor":         okResp(cursorRow),
		"GET /api/v1/connect/cursor/preview": okResp(previewFixture),
		"POST /api/v1/clients/cursor/rotate": refuse(http.StatusConflict, `{"success":false,"data":{"success":false,"action":"precondition_failed"},"error":"stale preview","action":"precondition_failed"}`),
	})
	_, _, err := runCLI(t, GetClientCommand, "table", "rotate", "cursor", "--yes")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Equal(t, "the client config changed since the preview; run the command again", err.Error())
}

func TestClientRotate_CustomPrintsPending(t *testing.T) {
	dev := `{"id":"dev-laptop","display_name":"Dev laptop","kind":"custom","credential_state":"client"}`
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/clients/dev-laptop":         okResp(dev),
		"POST /api/v1/clients/dev-laptop/rotate": okResp(`{"client":` + dev + `,"credential":"` + addedSecret + `","snippet":{"generic_http":"{\"k\":\"` + addedSecret + `\"}","header_name":"X-API-Key"},"rotation":{"state":"pending"}}`),
	})
	out, errOut, err := runCLI(t, GetClientCommand, "table", "rotate", "dev-laptop")
	require.NoError(t, err)
	reqs := rec.all()
	require.Len(t, reqs, 2, "a custom client has no config preview")
	require.Contains(t, out, "Credential: "+addedSecret)
	require.Contains(t, out, "Rotation pending: both secrets work until 'mcpproxy client rotate dev-laptop --finalize' or 24 h")
	require.NotContains(t, errOut, "mcp_cli_")
}

func TestClientRotate_Finalize(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/clients/dev-laptop/rotate/finalize": okResp(`{"client":{"id":"dev-laptop"},"rotation":{"state":"finalized"}}`)})
	out, _, err := runCLI(t, GetClientCommand, "table", "rotate", "dev-laptop", "--finalize")
	require.NoError(t, err)
	require.Equal(t, "Rotation: finalized\n", out)
	require.Len(t, rec.all(), 1)
}

// --- upgrade-admin-key-holders ---------------------------------------------------

const upgradeRow = `{"client_id":"cursor","display_name":"Cursor","display_path":"~/.cursor/mcp.json","diff":{},"credential":"mcp_cli_••••","profile":"work-ro2","mode":"locked","precondition_token":"row-1"}`

func TestClientUpgradeAdminKeyHolders_GuardPreviewExit1NoApply(t *testing.T) {
	preview := `{"preview":[` + upgradeRow + `],"precondition_token":"combined-1","guard":{"code":"binding_bypassable_without_auth","bindings":[{"client_id":"cursor","token_name":"client-cursor","profile":"work-ro2","mode":"locked"}],"fixes":[{"kind":"require_mcp_auth"},{"kind":"set_anonymous_profile","target":"work-ro2"}]}}`
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/clients/upgrade-admin-key-holders": okResp(preview)})
	out, _, err := runCLI(t, GetClientCommand, "table", "upgrade-admin-key-holders", "--profile", "work-ro2", "--yes")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), "could escape it by omitting its credential")
	require.Contains(t, err.Error(), "set require_mcp_auth: true")
	require.Contains(t, err.Error(), "mcpproxy profile anonymous work-ro2")
	require.Contains(t, out, "CLIENT")
	reqs := rec.all()
	require.Len(t, reqs, 1, "the guard refusal is final: no apply request")
	require.Equal(t, map[string]any{"profile": "work-ro2"}, reqs[0].jsonBody(t))
}

func TestClientUpgradeAdminKeyHolders_ApplySendsCombinedToken(t *testing.T) {
	preview := `{"preview":[` + upgradeRow + `],"precondition_token":"combined-1"}`
	applied := `{"upgraded":["cursor"],"failed":[],"next_step":"rotate_admin_api_key"}`
	// The same route answers the preview first, then the apply.
	rec := newSequencedRecorder(t, "POST /api/v1/clients/upgrade-admin-key-holders", []cannedResponse{okResp(preview), okResp(applied)})
	out, _, err := runCLI(t, GetClientCommand, "table", "upgrade-admin-key-holders", "--profile", "work-ro2", "--lock", "--yes")
	require.NoError(t, err)
	reqs := rec.all()
	require.Len(t, reqs, 2)
	require.Equal(t, map[string]any{"profile": "work-ro2", "mode": "locked"}, reqs[0].jsonBody(t))
	require.Equal(t, map[string]any{"profile": "work-ro2", "mode": "locked", "apply": true, "precondition_token": "combined-1"}, reqs[1].jsonBody(t))
	require.Contains(t, out, "Upgraded: cursor")
	require.Contains(t, out, "rotate the admin API key (set a new api_key and restart): https://docs.mcpproxy.app/configuration/config-file")
	require.NotContains(t, out, "1. mcpproxy client upgrade-admin-key-holders", "step 1 is what just ran")
}

func TestClientUpgradeAdminKeyHolders_EmptyPreviewPrintsRotateStep(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/clients/upgrade-admin-key-holders": okResp(`{"preview":[],"precondition_token":"","next_step":"rotate_admin_api_key"}`)})
	out, _, err := runCLI(t, GetClientCommand, "table", "upgrade-admin-key-holders")
	require.NoError(t, err)
	require.Contains(t, out, "No supported client holds the admin key.")
	require.Contains(t, out, "rotate the admin API key")
}

func TestClientUpgradeAdminKeyHolders_NonTTYWithoutYesExit1NoApply(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/clients/upgrade-admin-key-holders": okResp(`{"preview":[` + upgradeRow + `],"precondition_token":"combined-1"}`)})
	_, _, err := runCLI(t, GetClientCommand, "table", "upgrade-admin-key-holders")
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirmation required; pass --yes")
	require.Len(t, rec.all(), 1)
}

func TestClientUpgradeAdminKeyHolders_PartialFailureExit1(t *testing.T) {
	preview := `{"preview":[` + upgradeRow + `],"precondition_token":"c"}`
	seq := newSequencedRecorder(t, "POST /api/v1/clients/upgrade-admin-key-holders", []cannedResponse{
		okResp(preview), okResp(`{"upgraded":[],"failed":[{"client_id":"cursor","error":"permission denied"}]}`)})
	out, _, err := runCLI(t, GetClientCommand, "table", "upgrade-admin-key-holders", "--yes")
	require.Error(t, err)
	require.Contains(t, out, "Failed:")
	require.Contains(t, out, "cursor: permission denied")
	require.Len(t, seq.all(), 2)
}

// --- forget ----------------------------------------------------------------------

func TestClientForget_DisconnectAndError(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/clients/cursor": okResp(`{"revoked":"client-cursor","disconnected":true}`)})
	out, _, err := runCLI(t, GetClientCommand, "table", "forget", "cursor", "--disconnect")
	require.NoError(t, err)
	require.Equal(t, "disconnect=true", rec.only(t).RawQuery)
	require.Contains(t, out, "Revoked: client-cursor")
	require.Contains(t, out, "Config entry removed")

	newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/clients/cursor": okResp(`{"revoked":"client-cursor","disconnected":false,"disconnect_error":"permission denied"}`)})
	out, _, err = runCLI(t, GetClientCommand, "table", "forget", "cursor", "--disconnect")
	require.NoError(t, err, "revocation succeeded even though the config write failed")
	require.Contains(t, out, "Config entry kept (disconnect failed: permission denied)")

	rec = newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/clients/dev": okResp(`{"revoked":"client-dev","disconnected":false}`)})
	_, _, err = runCLI(t, GetClientCommand, "table", "forget", "dev")
	require.NoError(t, err)
	require.Empty(t, rec.only(t).RawQuery)

	newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/clients/none": refuse(http.StatusConflict, `{"success":false,"error":"client none has no active client credential","code":"no_client_credential"}`)})
	_, _, err = runCLI(t, GetClientCommand, "table", "forget", "none")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
}

func TestClientCommands_NoDaemonExit1(t *testing.T) {
	withNoDaemon(t)
	for _, args := range [][]string{{"list"}, {"show", "x"}, {"set-profile", "x", "p"}, {"lock", "x"}, {"add", "x"}, {"rotate", "x"}, {"upgrade-admin-key-holders"}, {"forget", "x"}} {
		_, _, err := runCLI(t, GetClientCommand, "table", args...)
		require.Error(t, err, args)
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
		require.Contains(t, err.Error(), "client requires running daemon. Start with: mcpproxy serve")
	}
}
