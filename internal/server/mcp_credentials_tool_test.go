package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 115 Phase 4: the `credentials` admin MCP tool.

type credentialsToolFixture struct {
	*profilesToolFixture
}

func newCredentialsToolFixture(t *testing.T, configure func(*config.Config)) *credentialsToolFixture {
	t.Helper()
	f := newProfilesToolFixture(t, configure)
	f.api.SetCredentialsService(f.rt.CredentialsService())
	return &credentialsToolFixture{f}
}

func credentialsCall(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = credentialsToolName
	req.Params.Arguments = args
	return req
}

// ccall runs one credentials operation: decoded JSON, isError, raw text.
func (f *credentialsToolFixture) ccall(ctx context.Context, args map[string]any) (map[string]any, bool, string) {
	f.t.Helper()
	res, err := f.proxy.handleCredentials(ctx, credentialsCall(args))
	require.NoError(f.t, err)
	text := resultText(f.t, res)
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	return out, res.IsError, text
}

func (f *credentialsToolFixture) cok(args map[string]any) map[string]any {
	f.t.Helper()
	out, isErr, text := f.ccall(apiKeyCtx(), args)
	require.False(f.t, isErr, text)
	return out
}

func (f *credentialsToolFixture) crefused(args map[string]any) map[string]any {
	f.t.Helper()
	out, isErr, text := f.ccall(apiKeyCtx(), args)
	require.True(f.t, isErr, text)
	require.NotNil(f.t, out, text)
	return out
}

func (f *credentialsToolFixture) tokenNames() []string {
	f.t.Helper()
	all, err := f.rt.StorageManager().ListAgentTokens()
	require.NoError(f.t, err)
	names := make([]string, 0, len(all))
	for _, t := range all {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}

func (f *credentialsToolFixture) changes() []*storage.ActivityRecord {
	f.t.Helper()
	recs, _, err := f.rt.StorageManager().ListActivities(storage.ActivityFilter{Types: []string{string(storage.ActivityTypeProfileChange)}, Limit: 500})
	require.NoError(f.t, err)
	return recs
}

// internalCalls waits until at least n credentials internal_tool_call records exist.
func (f *credentialsToolFixture) internalCalls(n int) []*storage.ActivityRecord {
	f.t.Helper()
	var out []*storage.ActivityRecord
	require.Eventually(f.t, func() bool {
		recs, _, err := f.rt.StorageManager().ListActivities(storage.ActivityFilter{Types: []string{string(storage.ActivityTypeInternalToolCall)}, Limit: 1000})
		if err != nil {
			return false
		}
		out = out[:0]
		for _, r := range recs {
			if r.ToolName == credentialsToolName || (r.Metadata != nil && r.Metadata["internal_tool_name"] == credentialsToolName) {
				out = append(out, r)
			}
		}
		return len(out) >= n
	}, 5*time.Second, 20*time.Millisecond)
	return out
}

// --- schema and surface (T045, T049) --------------------------------------------

func TestCredentialsTool_SchemaAndAnnotations(t *testing.T) {
	tool := buildCredentialsTool()
	want := []string{"client", "display_name", "expires_in", "kind", "mode", "name", "operation", "profile", "purpose", "state", "token"}
	assert.Equal(t, want, toolPropertyNames(t, tool))
	assert.Equal(t, []string{"operation"}, tool.InputSchema.Required)
	assert.Equal(t, false, tool.InputSchema.AdditionalProperties)
	op := tool.InputSchema.Properties["operation"].(map[string]any)
	assert.Equal(t, []string{"list", "get", "create_client", "create_token", "revoke"}, op["enum"])
	require.NotNil(t, tool.Annotations.DestructiveHint)
	assert.True(t, *tool.Annotations.DestructiveHint)
	assert.False(t, *tool.Annotations.ReadOnlyHint)
	assert.Equal(t, "Issue and revoke worker credentials", tool.Annotations.Title)
}

// TestToolsList_CredentialsBesideProfiles: registered wherever profiles is,
// kept under read_only_mode, never on the direct server, and the golden entry
// matches the definition.
func TestToolsList_CredentialsBesideProfiles(t *testing.T) {
	proxy := newToolsListProxy(t, func(cfg *config.Config) { cfg.ReadOnlyMode = true })
	assert.Contains(t, toolMapNames(proxy.server.ListTools()), credentialsToolName)
	assert.Contains(t, registeredToolNames(proxy.buildCallToolModeTools()), credentialsToolName)
	assert.Contains(t, registeredToolNames(proxy.buildCodeExecModeTools()), credentialsToolName)
	assert.NotContains(t, toolMapNames(proxy.directServer.ListTools()), credentialsToolName)
	regular := decodeToolsListGolden(t, toolsListGoldenPath("default_server"))
	raw, err := json.Marshal(buildCredentialsTool())
	require.NoError(t, err)
	assert.JSONEq(t, string(regular[credentialsToolName]), string(raw))
}

// --- visibility (T032, T034) ----------------------------------------------------

func TestCredentialsTool_VisibilityMatchesProfiles(t *testing.T) {
	for _, row := range profilesVisibilityRows() {
		t.Run(row.name, func(t *testing.T) {
			f := newCredentialsToolFixture(t, row.configure)
			ctx := row.caller(f.proxy)
			tools := []mcp.Tool{{Name: "profiles"}, {Name: credentialsToolName}, {Name: "call_tool_read"}}
			names := profileV3ToolNames(f.proxy.filterProfileV3Tools(ctx, tools))
			assert.Equal(t, containsName(names, "profiles"), containsName(names, credentialsToolName), "same predicate as profiles")
			assert.Equal(t, row.want, containsName(names, credentialsToolName))

			before := f.tokenNames()
			nChanges := len(f.changes())
			_, isErr, text := f.ccall(ctx, map[string]any{"operation": "create_token", "name": "forged", "profile": "work-readonly", "expires_in": "1h"})
			if row.want {
				require.False(t, isErr, text)
				return
			}
			require.True(t, isErr)
			assert.Equal(t, "unknown tool: credentials", text)
			assert.Equal(t, before, f.tokenNames(), "a forged call changes nothing")
			assert.Len(t, f.changes(), nChanges, "and writes no credential change record")
		})
	}
}

// After set_profile("") an administrator sees the tool again.
func TestCredentialsTool_SetProfileBackToNoneRestoresIt(t *testing.T) {
	f := newCredentialsToolFixture(t, nil)
	sid := fmt.Sprintf("cred-%d", time.Now().UnixNano())
	f.proxy.sessionStore.SetActiveProfile(sid, "work-readonly")
	ctx := sessionCtx(apiKeyCtx(), sid)
	_, isErr, text := f.ccall(ctx, map[string]any{"operation": "list"})
	require.True(t, isErr)
	assert.Equal(t, "unknown tool: credentials", text)
	f.proxy.sessionStore.SetActiveProfile(sid, "")
	_, isErr, text = f.ccall(ctx, map[string]any{"operation": "list"})
	require.False(t, isErr, text)
}

// --- gates (T035) ----------------------------------------------------------------

func TestCredentialsTool_WriteGates(t *testing.T) {
	f := newCredentialsToolFixture(t, nil)
	f.cok(map[string]any{"operation": "create_token", "name": "pre", "profile": "work-readonly", "expires_in": "1h"})
	live := f.proxy.currentConfig()
	for _, gate := range []struct {
		set  func(bool)
		code string
		text string
	}{
		{func(v bool) { live.ReadOnlyMode = v }, "read_only_mode", "Operation not allowed in read-only mode"},
		{func(v bool) { live.DisableManagement = v }, "management_disabled", "Server management is disabled for security"},
	} {
		gate.set(true)
		before := f.tokenNames()
		for _, args := range []map[string]any{
			{"operation": "create_client", "client": "gated-c", "profile": "work-readonly", "expires_in": "1h"},
			{"operation": "create_token", "name": "gated-t", "profile": "work-readonly", "expires_in": "1h"},
			{"operation": "revoke", "token": "pre"},
		} {
			body := f.crefused(args)
			assert.Equal(t, gate.code, body["code"])
			assert.Equal(t, gate.text, body["error"])
		}
		f.cok(map[string]any{"operation": "list"})
		f.cok(map[string]any{"operation": "get", "token": "pre"})
		assert.Equal(t, before, f.tokenNames())
		gate.set(false)
	}
}

// --- argument decoding (T038) -----------------------------------------------------

func TestCredentialsTool_ArgumentDecoding(t *testing.T) {
	f := newCredentialsToolFixture(t, nil)
	cases := []struct {
		args  map[string]any
		code  string
		field string
	}{
		{map[string]any{}, "missing_argument", "operation"},
		{map[string]any{"operation": "explode"}, "unknown_operation", "operation"},
		{map[string]any{"operation": 7.0}, "invalid_argument", "operation"},
		{map[string]any{"operation": "create_token", "name": "x", "profile": "work-readonly", "expires_in": "1h", "allowed_servers": "*"}, "invalid_argument", "(unknown argument)"},
		{map[string]any{"operation": "create_token", "name": "x", "profile": "work-readonly", "expires_in": "1h", "client": "c"}, "invalid_argument", "client"},
		{map[string]any{"operation": "create_token", "name": 3.0, "profile": "work-readonly", "expires_in": "1h"}, "invalid_argument", "name"},
		{map[string]any{"operation": "get"}, "invalid_argument", "client"},
		{map[string]any{"operation": "revoke", "client": "a", "token": "b"}, "invalid_argument", "client"},
		{map[string]any{"operation": "create_token", "name": "x", "expires_in": "1h"}, "missing_argument", "profile"},
		{map[string]any{"operation": "create_token", "name": "x", "profile": "", "expires_in": "1h"}, "profile_required", "profile"},
		{map[string]any{"operation": "create_token", "name": "x", "profile": "ghost", "expires_in": "1h"}, "unknown_profile", "profile"},
		{map[string]any{"operation": "create_token", "name": "x", "profile": "work-readonly"}, "missing_argument", "expires_in"},
		{map[string]any{"operation": "create_token", "name": "x", "profile": "work-readonly", "expires_in": "106752d"}, "invalid_expiry", "expires_in"},
		{map[string]any{"operation": "create_token", "name": "x", "profile": "work-readonly", "expires_in": "9999999999d"}, "invalid_expiry", "expires_in"},
		{map[string]any{"operation": "create_token", "name": "client-x", "profile": "work-readonly", "expires_in": "1h"}, "reserved_identity", "name"},
		{map[string]any{"operation": "create_client", "client": "cursor", "profile": "work-readonly", "expires_in": "1h"}, "reserved_identity", "client"},
		{map[string]any{"operation": "get", "token": "nope"}, "identity_not_found", "token"},
		{map[string]any{"operation": "revoke", "client": "nope"}, "identity_not_found", "client"},
	}
	before := f.tokenNames()
	for _, c := range cases {
		if !clientsEdition && clientAddressed(c.args) && (c.code == "reserved_identity" || c.code == "identity_not_found") {
			// The server edition has no client credentials: the edition switch
			// answers before the identity rules (A32).
			c.code = profile.CredentialErrorCodeUnsupportedEdition
		}
		body := f.crefused(c.args)
		assert.Equal(t, c.code, body["code"], "%v", c.args)
		assert.Equal(t, c.field, body["field"], "%v", c.args)
		raw, _ := json.Marshal(body)
		for _, v := range []string{"explode", "allowed_servers", "106752d", "9999999999d"} {
			assert.NotContains(t, string(raw), v, "no caller value or unknown key is echoed: %v", c.args)
		}
	}
	assert.Equal(t, before, f.tokenNames(), "no refusal leaves a credential")
}

// clientAddressed reports whether a credentials call names a client credential.
func clientAddressed(args map[string]any) bool {
	if args["operation"] == "create_client" {
		return true
	}
	c, ok := args["client"].(string)
	return ok && c != ""
}

// TestCredentialsTool_ServerEditionRefusesClientOps: in the server edition every
// client operation answers unsupported_edition and changes nothing, while
// token operations keep working (A32). In the personal edition the same calls
// succeed (covered by the delivery test below).
func TestCredentialsTool_ServerEditionRefusesClientOps(t *testing.T) {
	if clientsEdition {
		t.Skip("personal edition serves client credentials")
	}
	f := newCredentialsToolFixture(t, nil)
	f.cok(map[string]any{"operation": "create_token", "name": "edition-t", "profile": "work-readonly", "expires_in": "1h"})
	before := f.tokenNames()
	nChanges := len(f.changes())
	for _, args := range []map[string]any{
		{"operation": "create_client", "client": "edition-c", "profile": "work-readonly", "expires_in": "1h"},
		{"operation": "get", "client": "edition-c"},
		{"operation": "revoke", "client": "edition-c"},
	} {
		body := f.crefused(args)
		assert.Equal(t, profile.CredentialErrorCodeUnsupportedEdition, body["code"], "%v", args)
		assert.Equal(t, "client", body["field"], "%v", args)
	}
	assert.Equal(t, before, f.tokenNames(), "storage unchanged")
	assert.Len(t, f.changes(), nChanges, "nothing audited")
	list := f.cok(map[string]any{"operation": "list", "kind": "all"})
	assert.EqualValues(t, 1, list["total"], "list holds the token only")
}

// --- the whole lifecycle at unit level, delivery and activity (T041, T042) --------

func TestCredentialsTool_DeliveryAndActivityBody(t *testing.T) {
	f := newCredentialsToolFixture(t, nil)
	var secret string
	nKinds := 1 // credentials issued (and revoked) by this test
	if clientsEdition {
		nKinds = 2
		secret = deliverClient(t, f)
	}

	tok := f.cok(map[string]any{"operation": "create_token", "name": "research-task-42", "profile": "work-readonly", "expires_in": "30m"})
	tsecret := tok["credential"].(string)
	assert.Regexp(t, `^mcp_agt_[0-9a-f]{64}$`, tsecret)
	assert.Equal(t, "pinned", tok["token"].(map[string]any)["binding"])
	assert.Contains(t, tok["links"].(map[string]any)["identity"], "/ui/clients?tab=tokens&token=research-task-42")
	tdelivery := tok["delivery"].(map[string]any)
	assert.Equal(t, true, tdelivery["shown_once"])

	// list/get/revoke carry no secret.
	list := f.cok(map[string]any{"operation": "list"})
	assert.EqualValues(t, nKinds, list["total"])
	got := f.cok(map[string]any{"operation": "get", "token": "research-task-42"})
	rev := f.cok(map[string]any{"operation": "revoke", "token": "research-task-42"})
	assert.Equal(t, true, rev["changed"])
	again := f.cok(map[string]any{"operation": "revoke", "token": "research-task-42"})
	assert.Equal(t, false, again["changed"])
	bodies := []map[string]any{list, got, rev, again}
	calls := 5 // create_token, list, get, revoke, revoke
	if clientsEdition {
		cgot := f.cok(map[string]any{"operation": "get", "client": "delegated-worker"})
		crev := f.cok(map[string]any{"operation": "revoke", "client": "delegated-worker"})
		assert.Equal(t, true, crev["client_config_untouched"])
		bodies = append(bodies, cgot, crev)
		calls += 3 // create_client, get, revoke
	}
	for _, body := range bodies {
		raw, _ := json.Marshal(body)
		if secret != "" {
			assert.NotContains(t, string(raw), secret)
		}
		assert.NotContains(t, string(raw), tsecret)
	}

	// The activity records carry the server-built summary, never the secret.
	recs := f.internalCalls(calls)
	sawRedacted := 0
	for _, r := range recs {
		raw, _ := json.Marshal(r)
		if secret != "" {
			assert.NotContains(t, string(raw), secret)
		}
		assert.NotContains(t, string(raw), tsecret)
		if strings.Contains(r.Response, credentialRedactedMarker) {
			sawRedacted++
		}
	}
	assert.Equal(t, nKinds, sawRedacted, "every create stores the redacted delivery")
	for _, r := range f.changes() {
		raw, _ := json.Marshal(r)
		if secret != "" {
			assert.NotContains(t, string(raw), secret)
		}
		assert.NotContains(t, string(raw), tsecret)
		assert.NotContains(t, string(raw), "Summarise today", "the purpose text never reaches profile_change")
	}
	kinds := map[string]int{}
	for _, r := range f.changes() {
		kinds[fmt.Sprint(r.Metadata["change"])]++
	}
	assert.Equal(t, nKinds, kinds["issue"])
	assert.Equal(t, nKinds, kinds["revoke"], "an idempotent revoke writes no record")
}

// deliverClient issues the personal-edition client of the delivery test and
// checks its one-time delivery; it returns the raw secret.
func deliverClient(t *testing.T, f *credentialsToolFixture) string {
	t.Helper()
	out := f.cok(map[string]any{"operation": "create_client", "client": "delegated-worker", "profile": "work-readonly",
		"expires_in": "1h", "purpose": "Summarise today's issues; assumes no writes"})
	secret := out["credential"].(string)
	assert.Regexp(t, `^mcp_cli_[0-9a-f]{64}$`, secret)
	view := out["client"].(map[string]any)
	assert.Equal(t, "locked", view["binding"])
	assert.Equal(t, true, view["lease"])
	assert.Equal(t, "active", view["state"])
	snippet := out["snippet"].(map[string]any)
	assert.Equal(t, "X-API-Key", snippet["header_name"])
	var generic map[string]any
	require.NoError(t, json.Unmarshal([]byte(snippet["generic_http"].(string)), &generic))
	headers := generic["mcpServers"].(map[string]any)["mcpproxy"].(map[string]any)["headers"].(map[string]any)
	assert.Equal(t, secret, headers["X-API-Key"])
	delivery := out["delivery"].(map[string]any)
	assert.Equal(t, true, delivery["shown_once"])
	assert.Equal(t, "X-API-Key", delivery["header_name"])
	assert.Equal(t, "Authorization: Bearer <credential>", delivery["alternate_header"])
	assert.Contains(t, delivery["install_note"], "did not install")
	links := out["links"].(map[string]any)
	rawLinks, _ := json.Marshal(links)
	for _, bad := range []string{"apikey", "api_key", secret, "mcp_cli_"} {
		assert.NotContains(t, strings.ToLower(string(rawLinks)), strings.ToLower(bad))
	}
	assert.Contains(t, links["identity"], "/ui/clients?client=delegated-worker")
	assert.Contains(t, links["effective_tools"], "/ui/profiles/work-readonly?tab=tools&reason=callable")
	u, err := url.Parse(links["activity"].(string))
	require.NoError(t, err)
	assert.Equal(t, "delegated-worker", u.Query().Get("client"))
	return secret
}

// --- the secret-shaped input screen (T038a, T038c) ---------------------------------

func TestCredentialsTool_SecretInputScreen(t *testing.T) {
	f := newCredentialsToolFixture(t, nil)
	issued := f.cok(map[string]any{"operation": "create_token", "name": "seed", "profile": "work-readonly", "expires_in": "1h"})
	agentSecret := issued["credential"].(string)
	// The server edition issues no client credential (A32); a client-shaped
	// secret is still refused by its prefix, so the cases run unchanged.
	clientSecret := "mcp_cli_" + strings.Repeat("ab", 32)
	seedCalls := 1
	if clientsEdition {
		cl := f.cok(map[string]any{"operation": "create_client", "client": "seed-c", "profile": "work-readonly", "expires_in": "1h"})
		clientSecret = cl["credential"].(string)
		seedCalls = 2
	}
	apiKey := f.rt.Config().APIKey
	require.NotEmpty(t, apiKey)
	aws := "AKIA" + "IOSFODNN7REALKEY"
	secrets := []string{agentSecret, clientSecret, apiKey, aws}
	before := f.tokenNames()
	nChanges := len(f.changes())

	cases := []struct {
		name    string
		args    map[string]any
		field   string
		fields  []any
		unknown int
	}{
		{"purpose", map[string]any{"operation": "create_token", "name": "x", "profile": "work-readonly", "expires_in": "1h", "purpose": "brief " + agentSecret}, "purpose", []any{"purpose"}, 0},
		{"two secrets", map[string]any{"operation": "create_client", "client": "x", "profile": "work-readonly", "expires_in": "1h", "purpose": agentSecret, "display_name": apiKey}, "display_name", []any{"display_name", "purpose"}, 0},
		{"nested", map[string]any{"operation": "create_token", "purpose": map[string]any{"x": []any{clientSecret}}}, "purpose", []any{"purpose"}, 0},
		{"unknown key value", map[string]any{"operation": "list", "zzz": aws}, "(unknown argument)", []any{}, 1},
		{"secret as key", map[string]any{"operation": "list", clientSecret: "x"}, "(unknown argument)", []any{}, 1},
		{"nested key", map[string]any{"operation": "list", "kind": map[string]any{agentSecret: 1}}, "kind", []any{"kind"}, 0},
		{"wins over unknown op", map[string]any{"operation": "explode", "name": agentSecret}, "name", []any{"name"}, 0},
		{"in operation", map[string]any{"operation": clientSecret}, "operation", []any{"operation"}, 0},
		{"in expires_in", map[string]any{"operation": "create_token", "name": "x", "profile": "work-readonly", "expires_in": apiKey + "d"}, "expires_in", []any{"expires_in"}, 0},
	}
	for _, c := range cases {
		body := f.crefused(c.args)
		assert.Equal(t, "secret_in_argument", body["code"], c.name)
		assert.Equal(t, c.field, body["field"], c.name)
		assert.Equal(t, c.fields, body["offending_fields"], c.name)
		assert.EqualValues(t, c.unknown, body["unknown_offending_count"], c.name)
		raw, _ := json.Marshal(body)
		for _, s := range secrets {
			assert.NotContains(t, string(raw), s, c.name)
		}
		assert.NotContains(t, string(raw), "zzz", c.name)
	}

	// Detection settings cannot weaken the screen (A17), and a payload over the
	// cap is refused without being stored.
	live := f.proxy.currentConfig()
	disabled := &config.SensitiveDataDetectionConfig{Enabled: false, ScanRequests: false, MaxPayloadSizeKB: 1, Categories: map[string]bool{"cloud_credentials": false, "api_token": false, "high_entropy": false}}
	live.SensitiveDataDetection = disabled
	filler := strings.Repeat("a ", 550)
	body := f.crefused(map[string]any{"operation": "create_token", "name": "x", "profile": "work-readonly", "expires_in": "1h", "purpose": filler + aws})
	assert.Equal(t, "secret_in_argument", body["code"])
	body = f.crefused(map[string]any{"operation": "list", "other": filler + aws})
	assert.Equal(t, "secret_in_argument", body["code"])
	big := strings.Repeat("b ", 9000) + aws
	body = f.crefused(map[string]any{"operation": "create_token", "purpose": big})
	assert.Equal(t, "arguments_too_large", body["code"])
	assert.EqualValues(t, 16384, body["limit_bytes"])

	assert.Equal(t, before, f.tokenNames(), "nothing minted")
	assert.Len(t, f.changes(), nChanges, "nothing audited")

	// The stored records hold only the server summary.
	recs := f.internalCalls(len(cases) + 3 + seedCalls)
	sawScreened, sawOversized := 0, 0
	for _, r := range recs {
		raw, _ := json.Marshal(r)
		for _, s := range secrets {
			assert.NotContains(t, string(raw), s)
		}
		assert.NotContains(t, string(raw), "zzz")
		assert.NotContains(t, string(raw), "b b b b")
		if r.Arguments["_screened"] == "secret-shaped input; arguments not stored" {
			sawScreened++
			for k := range r.Arguments {
				assert.Contains(t, []string{"_screened", "operation", "offending_fields", "unknown_offending_count"}, k)
			}
		}
		if r.Arguments["_screened"] == "oversized input; arguments not stored" {
			sawOversized++
		}
	}
	assert.Equal(t, len(cases)+2, sawScreened)
	assert.Equal(t, 1, sawOversized)

	// The raw BBolt file never holds a submitted secret except the issued
	// credential's HMAC (never the raw value).
	dbBytes, err := os.ReadFile(filepath.Join(f.rt.Config().DataDir, "config.db"))
	if err == nil {
		for _, s := range secrets {
			assert.NotContains(t, string(dbBytes), s)
		}
	}
}

// A passing call with an unknown non-secret key is refused and its name is not recorded.
func TestCredentialsTool_UnknownKeyNotRecorded(t *testing.T) {
	f := newCredentialsToolFixture(t, nil)
	body := f.crefused(map[string]any{"operation": "list", "weird_key_name": "v"})
	assert.Equal(t, "(unknown argument)", body["field"])
	recs := f.internalCalls(1)
	for _, r := range recs {
		raw, _ := json.Marshal(r)
		assert.NotContains(t, string(raw), "weird_key_name")
	}
}

// --- server edition parity of client operations is covered by the
// clients-supported switch; here: a dangling pin reports profile_state.
func TestCredentialsTool_DanglingPinReported(t *testing.T) {
	f := newCredentialsToolFixture(t, func(cfg *config.Config) {
		cfg.Profiles = append(cfg.Profiles, config.ProfileConfig{Name: "temp", Servers: []string{"github"}})
	})
	f.cok(map[string]any{"operation": "create_token", "name": "pinned-temp", "profile": "temp", "expires_in": "1h"})
	_ = f.ok(map[string]any{"operation": "delete", "name": "temp", "force": true})
	got := f.cok(map[string]any{"operation": "get", "token": "pinned-temp"})
	assert.Equal(t, "dangling", got["credential"].(map[string]any)["profile_state"])
	tok, err := f.rt.StorageManager().GetAgentTokenByName("pinned-temp")
	require.NoError(t, err)
	assert.True(t, tok.GuardBound)
	assert.Equal(t, auth.KindAgent, func() string {
		if tok.Kind == "" {
			return auth.KindAgent
		}
		return tok.Kind
	}())
	_ = profile.CredentialErrorCodeIdentityExists
}

// Review code-r1 (A24 revised): a short configured API key (here 6 characters,
// below the old 8-character floor) is screened over MCP like any other key:
// refused before minting, and absent from the stored activity and metadata.
func TestCredentialsTool_SecretInputScreen_ShortAPIKey(t *testing.T) {
	const shortKey = "k7Z2q9"
	f := newCredentialsToolFixture(t, func(cfg *config.Config) { cfg.APIKey = shortKey })
	before := f.tokenNames()
	cases := []map[string]any{
		{"operation": "create_token", "name": "x", "profile": "work-readonly", "expires_in": "1h", "purpose": shortKey},
		{"operation": "create_token", "name": "x", "profile": "work-readonly", "expires_in": "1h", "purpose": "brief " + shortKey + " end"},
		{"operation": "create_token", "name": "x", "profile": "work-readonly", "expires_in": shortKey + "d"},
		{"operation": "create_client", "client": "x", "profile": "work-readonly", "expires_in": "1h", "purpose": "w " + shortKey},
	}
	for _, args := range cases {
		body := f.crefused(args)
		assert.Equal(t, "secret_in_argument", body["code"], "%v", args)
		raw, _ := json.Marshal(body)
		assert.NotContains(t, string(raw), shortKey)
	}
	assert.Equal(t, before, f.tokenNames(), "nothing minted")
	for _, r := range f.internalCalls(len(cases)) {
		raw, _ := json.Marshal(r)
		assert.NotContains(t, string(raw), shortKey, "the key never reaches the activity record")
	}
	list := f.cok(map[string]any{"operation": "list"})
	raw, _ := json.Marshal(list)
	assert.NotContains(t, string(raw), shortKey)
}
