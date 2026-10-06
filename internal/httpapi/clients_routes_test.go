//go:build !server

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 108-f T068/T070/T074b: the clients REST surface end to end over a real
// clients service, a real token store and a real connect service on a seeded
// Cursor config.

const adminKeyEntry = `{"mcpServers":{"mcpproxy":{"type":"sse","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"` + bindingAdminKey + `"}}}}`

func (h *connectHarness) get(path string) map[string]interface{} {
	h.t.Helper()
	w := h.do(http.MethodGet, path, "", nil, bindingAdminKey)
	require.Equal(h.t, http.StatusOK, w.Code, w.Body.String())
	return decodeBody(h.t, w)["data"].(map[string]interface{})
}

func rowByID(t *testing.T, data map[string]interface{}, id string) map[string]interface{} {
	t.Helper()
	for _, r := range data["clients"].([]interface{}) {
		row := r.(map[string]interface{})
		if row["id"] == id {
			return row
		}
	}
	return nil
}

func (h *connectHarness) writeCursor(content string) {
	h.t.Helper()
	require.NoError(h.t, os.WriteFile(connect.ConfigPath("cursor", h.home), []byte(content), 0o644))
}

func warningCodes(data map[string]interface{}) map[string]map[string]interface{} {
	out := map[string]map[string]interface{}{}
	for _, w := range data["warnings"].([]interface{}) {
		m := w.(map[string]interface{})
		out[m["code"].(string)] = m
	}
	return out
}

func TestClientsRoutes_EveryNewRouteIsAdminOnly(t *testing.T) {
	ctrl := &clientPresenceController{}
	srv, token := agentTokenServer(t, ctrl)
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/clients", `{"id":"x"}`},
		{http.MethodPost, "/api/v1/clients/bulk-assign", `{"from_profile":"a","to_profile":"b"}`},
		{http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", `{}`},
		{http.MethodPost, "/api/v1/clients/cursor/rotate", `{}`},
		{http.MethodPost, "/api/v1/clients/cursor/rotate/finalize", `{}`},
		{http.MethodDelete, "/api/v1/clients/cursor", ``},
		{http.MethodPut, "/api/v1/clients/cursor/binding", `{"profile":""}`},
	} {
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
		req.Header.Set("X-API-Key", token)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s: %s", rt.method, rt.path, rec.Body.String())
	}
}

func TestClientsRoutes_CreateCustomClientReturnsTheSecretOnceWithAHeaderSnippet(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	w := h.do(http.MethodPost, "/api/v1/clients", `{"id":"dev-laptop","display_name":"Dev laptop","profile":"ro"}`,
		map[string]string{XMCPProxySurfaceHeader: "cli"}, bindingAdminKey)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})

	secret := data["credential"].(string)
	assert.Regexp(t, `^mcp_cli_[0-9a-f]{64}$`, secret)
	snippet := data["snippet"].(map[string]interface{})
	assert.Equal(t, "X-API-Key", snippet["header_name"])
	generic := snippet["generic_http"].(string)
	assert.Contains(t, generic, `"X-API-Key":"`+secret+`"`)
	assert.Contains(t, generic, "http://127.0.0.1:8080/mcp")
	assert.NotContains(t, generic, "apikey=", "a header, never a query parameter")

	client := data["client"].(map[string]interface{})
	assert.Equal(t, "custom", client["kind"])
	assert.Equal(t, "dev-laptop", client["id"])
	assert.Equal(t, "Dev laptop", client["display_name"])
	assert.Equal(t, "ro", client["profile"])
	assert.Equal(t, "locked", client["profile_mode"])
	assert.Equal(t, "pin", client["profile_source"])
	assert.Equal(t, "client", client["credential_state"])
	assert.Equal(t, false, client["installed"])
	assert.Equal(t, true, client["connected"])
	assert.NotEmpty(t, client["expires_at"])
	assert.NotContains(t, w.Body.String()[strings.Index(w.Body.String(), `"client"`):strings.Index(w.Body.String(), `"credential"`)], secret, "the row carries no secret")

	// It authenticates, and the secret never reaches a record or a later read.
	_, err := h.sm.ValidateAgentToken(secret, h.key)
	require.NoError(t, err)
	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "cli", recs[0].Metadata["surface"])
	assert.NotContains(t, toJSON(t, recs[0].Metadata), secret)
	list := h.do(http.MethodGet, "/api/v1/clients", "", nil, bindingAdminKey).Body.String()
	assert.NotContains(t, list, secret)
}

func toJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestClientsRoutes_CreateValidatesEveryInput(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	for _, tc := range []struct{ name, body, field string }{
		{"slash in id", `{"id":"Dev/Laptop"}`, "id"},
		{"upper case id", `{"id":"DevLaptop"}`, "id"},
		{"long id", `{"id":"` + strings.Repeat("a", 57) + `"}`, "id"},
		{"supported client id", `{"id":"cursor"}`, "id"},
		{"unknown profile", `{"id":"x1","profile":"ghost"}`, "profile"},
		{"bad mode", `{"id":"x2","profile":"ro","mode":"sticky"}`, "mode"},
		{"locked without profile", `{"id":"x3","mode":"locked"}`, "mode"},
		{"bad expiry", `{"id":"x4","expires_in":"soon"}`, "expires_in"},
		{"expiry over the cap", `{"id":"x5","expires_in":"400d"}`, "expires_in"},
		{"long display name", `{"id":"x6","display_name":"` + strings.Repeat("n", 65) + `"}`, "display_name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := h.do(http.MethodPost, "/api/v1/clients", tc.body, nil, bindingAdminKey)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Equal(t, tc.field, decodeBody(t, w)["field"])
		})
	}
	toks, err := h.sm.ListAgentTokens()
	require.NoError(t, err)
	assert.Empty(t, toks, "a refused add mints nothing")

	// A second add of the same id is refused; the default expiry is 365 days.
	require.Equal(t, http.StatusCreated, h.do(http.MethodPost, "/api/v1/clients", `{"id":"ok-1"}`, nil, bindingAdminKey).Code)
	assert.Equal(t, http.StatusBadRequest, h.do(http.MethodPost, "/api/v1/clients", `{"id":"ok-1"}`, nil, bindingAdminKey).Code)
	tok, err := h.sm.GetAgentTokenByName("client-ok-1")
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(auth.MaxTokenExpiry), tok.ExpiresAt, time.Minute)
}

func TestClientsRoutes_CreateRefusedByTheGuardAndByAConflictingToken(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.cfg.RequireMCPAuth = false // the conservative guard refuses any named binding

	w := h.do(http.MethodPost, "/api/v1/clients", `{"id":"dev","profile":"ro"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "binding_bypassable_without_auth", decodeBody(t, w)["code"])
	toks, _ := h.sm.ListAgentTokens()
	assert.Empty(t, toks)

	h.cfg.RequireMCPAuth = true
	raw, err := json.Marshal(auth.AgentToken{Name: "client-dev", TokenHash: "deadbeef", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()})
	require.NoError(t, err)
	require.NoError(t, h.sm.GetDB().Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(storage.AgentTokensBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte("deadbeef"), raw)
	}))
	w = h.do(http.MethodPost, "/api/v1/clients", `{"id":"dev"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "client-dev", decodeBody(t, w)["conflicting_token"])

	// The list names the conflict as a warning with an edit_token action.
	codes := warningCodes(h.get("/api/v1/clients"))
	require.Contains(t, codes, "client_token_name_conflict")
	assert.Equal(t, "client-dev", codes["client_token_name_conflict"]["action"].(map[string]interface{})["target"])
}

func TestClientsRoutes_CustomRotateFinalizeAndTheOverlap(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	first := decodeBody(t, h.do(http.MethodPost, "/api/v1/clients", `{"id":"dev-laptop"}`, nil, bindingAdminKey))["data"].(map[string]interface{})["credential"].(string)

	w := h.do(http.MethodPost, "/api/v1/clients/dev-laptop/rotate", `{}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})
	second := data["credential"].(string)
	assert.NotEqual(t, first, second)
	assert.Equal(t, "pending", data["rotation"].(map[string]interface{})["state"])
	assert.Contains(t, data["snippet"].(map[string]interface{})["generic_http"], second)
	assert.Equal(t, true, data["client"].(map[string]interface{})["rotation_pending"])
	for _, secret := range []string{first, second} {
		_, err := h.sm.ValidateAgentToken(secret, h.key)
		require.NoError(t, err, "both secrets authenticate during the overlap")
	}

	w = h.do(http.MethodPost, "/api/v1/clients/dev-laptop/rotate/finalize", `{}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "finalized", decodeBody(t, w)["data"].(map[string]interface{})["rotation"].(map[string]interface{})["state"])
	_, err := h.sm.ValidateAgentToken(first, h.key)
	require.Error(t, err, "the old secret stops authenticating")
	_, err = h.sm.ValidateAgentToken(second, h.key)
	require.NoError(t, err)

	// A second finalize is a no-op success; both writes left exactly one rotate record.
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/v1/clients/dev-laptop/rotate/finalize", `{}`, nil, bindingAdminKey).Code)
	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	rotates := 0
	for _, r := range recs {
		if r.Metadata["change"] == "rotate" {
			rotates++
			assert.NotContains(t, toJSON(t, r.Metadata), "mcp_cli_"+strings.Repeat("a", 8)[:0]+first[8:16])
		}
	}
	assert.Equal(t, 1, rotates)

	// No credential: 409 no_client_credential, for rotate and finalize.
	for _, path := range []string{"/api/v1/clients/ghost/rotate", "/api/v1/clients/ghost/rotate/finalize"} {
		w = h.do(http.MethodPost, path, `{}`, nil, bindingAdminKey)
		require.Equal(t, http.StatusConflict, w.Code, path)
		assert.Equal(t, "no_client_credential", decodeBody(t, w)["code"])
	}
}

func TestClientsRoutes_SupportedRotateUsesTheConnectPath(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/v1/connect/cursor", `{"profile":"ro"}`, nil, bindingAdminKey).Code)
	old := extractSecret(t, h.cursorConfig())

	// A stale precondition token is the connect 409.
	w := h.do(http.MethodPost, "/api/v1/clients/cursor/rotate", `{"precondition_token":"bogus"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "precondition_failed", decodeBody(t, w)["action"])
	assert.Equal(t, old, extractSecret(t, h.cursorConfig()), "a refused rotation writes nothing")

	w = h.do(http.MethodPost, "/api/v1/clients/cursor/rotate", `{}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})
	assert.Equal(t, "finalized", data["rotation"].(map[string]interface{})["state"])
	assert.Equal(t, "updated", data["connect"].(map[string]interface{})["action"])
	assert.NotContains(t, data, "credential", "a supported client's secret goes into its config, never the response")
	fresh := extractSecret(t, h.cursorConfig())
	assert.NotEqual(t, old, fresh)
	_, err := h.sm.ValidateAgentToken(fresh, h.key)
	require.NoError(t, err)
	_, err = h.sm.ValidateAgentToken(old, h.key)
	require.Error(t, err)
	client := data["client"].(map[string]interface{})
	assert.Equal(t, "ro", client["profile"], "the binding is kept")
	assert.NotContains(t, w.Body.String(), fresh)
}

func TestClientsRoutes_ForgetRevokesEvenWhenTheDisconnectFails(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/v1/connect/cursor", `{"profile":"ro"}`, nil, bindingAdminKey).Code)
	secret := extractSecret(t, h.cursorConfig())

	w := h.do(http.MethodDelete, "/api/v1/clients/cursor?disconnect=true", ``, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})
	assert.Equal(t, "client-cursor", data["revoked"])
	assert.Equal(t, true, data["disconnected"])
	assert.NotContains(t, h.cursorConfig(), "mcp_cli_", "the entry was removed")
	_, err := h.sm.ValidateAgentToken(secret, h.key)
	require.Error(t, err, "the credential is revoked")
	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	var forget map[string]interface{}
	for _, r := range recs {
		if r.Metadata["change"] == "forget" {
			forget = r.Metadata
		}
	}
	require.NotNil(t, forget)
	assert.Equal(t, "client-cursor", forget["token_name"])
	assert.Equal(t, true, forget["diff"].(map[string]interface{})["disconnected"])

	// A client without a credential record: 409.
	w = h.do(http.MethodDelete, "/api/v1/clients/ghost", ``, nil, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "no_client_credential", decodeBody(t, w)["code"])

	// disconnect=true on a custom client is ignored.
	require.Equal(t, http.StatusCreated, h.do(http.MethodPost, "/api/v1/clients", `{"id":"dev"}`, nil, bindingAdminKey).Code)
	w = h.do(http.MethodDelete, "/api/v1/clients/dev?disconnect=true", ``, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, false, decodeBody(t, w)["data"].(map[string]interface{})["disconnected"])
}

func TestClientsRoutes_ForgetRevokesWhenTheConfigIsNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not stop a write here")
	}
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/v1/connect/cursor", `{"profile":"ro"}`, nil, bindingAdminKey).Code)
	secret := extractSecret(t, h.cursorConfig())
	dir := filepath.Dir(connect.ConfigPath("cursor", h.home))
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	w := h.do(http.MethodDelete, "/api/v1/clients/cursor?disconnect=true", ``, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})
	assert.Equal(t, false, data["disconnected"])
	assert.NotEmpty(t, data["disconnect_error"])
	assert.Equal(t, "client-cursor", data["revoked"], "revocation never waits for the file write")
	_, err := h.sm.ValidateAgentToken(secret, h.key)
	require.Error(t, err, "the credential is cut off although the entry is still in the file")
}

func TestClientsRoutes_BulkAssign(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.mint("cursor", "ro")
	h.mint("windsurf", "ro")
	h.mint("vscode", "full")

	for _, tc := range []struct{ body, field string }{
		{`{"to_profile":"full"}`, "from_profile"},
		{`{"from_profile":"ro"}`, "to_profile"},
		{`{"from_profile":"ro","to_profile":"ghost"}`, "to_profile"},
	} {
		w := h.do(http.MethodPost, "/api/v1/clients/bulk-assign", tc.body, nil, bindingAdminKey)
		require.Equal(t, http.StatusBadRequest, w.Code, tc.body)
		assert.Equal(t, tc.field, decodeBody(t, w)["field"])
	}

	w := h.do(http.MethodPost, "/api/v1/clients/bulk-assign", `{"from_profile":"ro","to_profile":"full"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})
	assert.Equal(t, []interface{}{"cursor", "windsurf"}, data["moved"])
	assert.Equal(t, []interface{}{}, data["skipped"])
	for _, id := range []string{"cursor", "windsurf"} {
		v, err := h.svc.Get(id)
		require.NoError(t, err)
		assert.Equal(t, "full", v.Profile)
	}
	// Nothing left on ro: an empty move is a success with empty arrays.
	w = h.do(http.MethodPost, "/api/v1/clients/bulk-assign", `{"from_profile":"ro","to_profile":"full"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []interface{}{}, decodeBody(t, w)["data"].(map[string]interface{})["moved"])
}

func TestClientsRoutes_BulkAssignReportsGuardRefusalsPerClient(t *testing.T) {
	guard := refusingGuard{err: &internalRuntime.BindingGuardError{
		Bindings: []internalRuntime.BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "full", Mode: "locked"}},
		Fixes:    []internalRuntime.GuardFix{{Kind: "require_mcp_auth"}},
	}}
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.mint("cursor", "ro")
	h.svc = internalRuntime.NewClientsService(internalRuntime.ClientsServiceDeps{
		Store:    h.sm,
		HMACKey:  func() ([]byte, error) { return h.key, nil },
		Config:   func() *config.Config { return h.cfg },
		Guard:    func() internalRuntime.BindingGuard { return guard },
		Activity: h.sm.SaveActivity,
	})
	h.srv.SetClientsService(h.svc)
	w := h.do(http.MethodPost, "/api/v1/clients/bulk-assign", `{"from_profile":"ro","to_profile":"full"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})
	assert.Equal(t, []interface{}{}, data["moved"])
	skipped := data["skipped"].([]interface{})
	require.Len(t, skipped, 1)
	assert.Equal(t, "binding_bypassable_without_auth", skipped[0].(map[string]interface{})["code"])
}

func TestClientsRoutes_ListShowsCustomRowsFiltersAndKeepsWarningsUnfiltered(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.mint("cursor", "ro")
	h.mint("windsurf", "full")
	require.Equal(t, http.StatusCreated, h.do(http.MethodPost, "/api/v1/clients", `{"id":"dev-laptop","profile":"ro"}`, nil, bindingAdminKey).Code)
	require.Equal(t, http.StatusCreated, h.do(http.MethodPost, "/api/v1/clients", `{"id":"open-one"}`, nil, bindingAdminKey).Code)
	require.Equal(t, http.StatusCreated, h.do(http.MethodPost, "/api/v1/clients", `{"id":"gone-one"}`, nil, bindingAdminKey).Code)
	require.Equal(t, http.StatusOK, h.do(http.MethodDelete, "/api/v1/clients/gone-one", ``, nil, bindingAdminKey).Code)
	h.cfg.Profiles = h.cfg.Profiles[1:] // "ro" is deleted from the config: a dangling binding

	all := h.get("/api/v1/clients")
	ids := map[string]bool{}
	for _, r := range all["clients"].([]interface{}) {
		ids[r.(map[string]interface{})["id"].(string)] = true
	}
	assert.True(t, ids["dev-laptop"])
	assert.True(t, ids["open-one"])
	assert.False(t, ids["gone-one"], "a revoked custom row is omitted from the list")
	dev := rowByID(t, all, "dev-laptop")
	assert.Equal(t, "custom", dev["kind"])
	assert.Equal(t, true, dev["profile_missing"])
	assert.Equal(t, "connected_never_seen", dev["state"])

	// profile= matches the CURRENT binding of ACTIVE credentials.
	ids = map[string]bool{}
	for _, r := range h.get("/api/v1/clients?profile=ro")["clients"].([]interface{}) {
		ids[r.(map[string]interface{})["id"].(string)] = true
	}
	assert.Equal(t, map[string]bool{"cursor": true, "dev-laptop": true}, ids, "a dangling pin is still a match")
	filtered := h.get("/api/v1/clients?profile=-")["clients"].([]interface{})
	require.Len(t, filtered, 1, "profile=- selects the All-servers bindings")
	assert.Equal(t, "open-one", filtered[0].(map[string]interface{})["id"])
	assert.Empty(t, h.get("/api/v1/clients?profile=ghost")["clients"], "an unknown value is not an error")
	one := h.get("/api/v1/clients?client=cursor")["clients"].([]interface{})
	require.Len(t, one, 1)

	// Warnings come from the UNFILTERED set: filtering to one client keeps them.
	unfiltered := warningCodes(all)
	filteredWarnings := warningCodes(h.get("/api/v1/clients?client=open-one"))
	assert.Equal(t, len(all["warnings"].([]interface{})), len(h.get("/api/v1/clients?client=open-one")["warnings"].([]interface{})))
	require.Contains(t, unfiltered, "profile_missing")
	assert.Contains(t, filteredWarnings, "profile_missing")
	assert.Equal(t, "move_client", unfiltered["profile_missing"]["action"].(map[string]interface{})["kind"])
	assert.Equal(t, "warn", unfiltered["profile_missing"]["severity"])

	// The detail route honours no scope filter.
	w := h.do(http.MethodGet, "/api/v1/clients/cursor?profile=ro", "", nil, bindingAdminKey)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientsRoutes_Spec109PresenceFieldsAreUnchanged(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.mint("cursor", "ro")
	row := rowByID(t, h.get("/api/v1/clients"), "cursor")
	for _, key := range []string{"id", "display_name", "kind", "icon", "state", "installed", "connected", "last_seen", "active_sessions", "calls_24h", "reload_hint"} {
		assert.Contains(t, row, key, "Spec 109 field %s must stay", key)
	}
	assert.Equal(t, "supported", row["kind"])
	assert.Equal(t, "Cursor", row["display_name"])
	assert.Equal(t, "client", row["credential_state"])
	assert.Equal(t, "ro", row["profile"])
	assert.Equal(t, "pin", row["profile_source"])
	assert.Equal(t, "client-cursor", row["token_name"])
	for _, other := range h.get("/api/v1/clients")["clients"].([]interface{}) {
		o := other.(map[string]interface{})
		assert.Contains(t, []string{"supported", "other", "custom"}, o["kind"])
		assert.NotNil(t, o["credential_state"])
	}
}

// F11/FR-025: the LIST never reads a config; it still reports who holds the
// admin key, from the last on-demand observation, across a restart.
func TestClientsRoutes_ListNeverReadsConfigsButRemembersAdminKeyHolders(t *testing.T) {
	h := newBindingHarness(t, internalRuntime.ConservativeBindingGuard{})
	// Build the "restarted" server before LOCALAPPDATA points into a TempDir:
	// NewServer opens http.log under LOCALAPPDATA, and on Windows an open log
	// file makes the TempDir cleanup fail.
	srv2 := NewServer(h.ctrl, zap.NewNop().Sugar(), nil)
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	cursorPath := connect.ConfigPath("cursor", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cursorPath), 0o755))
	require.NoError(t, os.WriteFile(cursorPath, []byte(adminKeyEntry), 0o644))

	var reads atomic.Int64
	counting := func(path string) ([]byte, error) {
		if path == cursorPath {
			reads.Add(1)
		}
		return os.ReadFile(path)
	}
	conn := connect.NewServiceWithReader("127.0.0.1:8080", bindingAdminKey, home, counting).WithCredentialMinter(h.svc.ConnectMinter())
	h.srv.SetConnectService(conn)
	h.ctrl.state = &storage.OnboardingState{ClientConnectedAt: map[string]time.Time{"cursor": time.Now()}}

	get := func(srv *Server, path string) map[string]interface{} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-API-Key", bindingAdminKey)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		return decodeBody(t, w)["data"].(map[string]interface{})
	}

	// Never classified: unknown, and no config read.
	list := get(h.srv, "/api/v1/clients")
	assert.Equal(t, int64(0), reads.Load(), "the list performs zero config reads")
	row := rowByID(t, list, "cursor")
	assert.Equal(t, "unknown", row["credential_state"])
	assert.NotContains(t, row, "credential_checked_at")
	assert.NotContains(t, warningCodes(list), "client_holds_admin_key")

	// The detail read classifies on demand and remembers it.
	detail := get(h.srv, "/api/v1/clients/cursor")
	assert.Equal(t, "admin_key", detail["credential_state"])
	assert.NotEmpty(t, detail["credential_checked_at"])
	assert.Greater(t, reads.Load(), int64(0))
	reads.Store(0)

	// "Restart": a new REST server over the same persisted state.
	srv2.SetTokenStore(h.sm, t.TempDir())
	srv2.SetClientsService(h.svc)
	srv2.SetConnectService(conn)
	list = get(srv2, "/api/v1/clients")
	assert.Equal(t, int64(0), reads.Load(), "still no config read")
	row = rowByID(t, list, "cursor")
	assert.Equal(t, "admin_key", row["credential_state"])
	assert.NotEmpty(t, row["credential_checked_at"])
	warning := warningCodes(list)["client_holds_admin_key"]
	require.NotNil(t, warning)
	assert.Equal(t, "cursor", warning["client_id"])
	assert.Equal(t, "upgrade_admin_key_holders", warning["action"].(map[string]interface{})["kind"])
	assert.NotContains(t, warning["action"], "target")

	// A disconnect forgets the observation.
	h.ctrl.mu.Lock()
	applyClientDisconnected(h.ctrl.state, "cursor", time.Now())
	h.ctrl.mu.Unlock()
	assert.NotContains(t, h.ctrl.state.ClientCredentialObserved, "cursor")
}

func TestClientsRoutes_DetailCapsSessionsAndCarriesTheirProfile(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.mint("cursor", "ro")
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 30; i++ {
		h.ctrl.sessions = append(h.ctrl.sessions, &contracts.MCPSession{
			ID: "s" + string(rune('a'+i%26)) + string(rune('a'+i/26)), ClientName: "cursor", Status: "active",
			StartTime: base, LastActivity: base.Add(time.Duration(30-i) * time.Minute), Profile: "ro", ProfileSource: "pin", ClientID: "cursor",
		})
	}
	h.ctrl.state = &storage.OnboardingState{ClientConnectedAt: map[string]time.Time{"cursor": base.Add(-time.Hour)}}
	detail := h.get("/api/v1/clients/cursor")
	sessions := detail["sessions"].([]interface{})
	require.Len(t, sessions, 20, "the detail read caps sessions at the latest 20")
	first := sessions[0].(map[string]interface{})
	assert.Equal(t, "ro", first["profile"])
	assert.Equal(t, "pin", first["profile_source"])
	assert.Equal(t, float64(30), detail["active_sessions"], "the count is not capped")
}

func TestClientsRoutes_UpgradeAdminKeyHoldersPreviewAndApply(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.writeCursor(adminKeyEntry)

	// Preview: masked, nothing written or minted.
	before := h.cursorConfig()
	w := h.do(http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", `{}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	prev := decodeBody(t, w)["data"].(map[string]interface{})
	rows := prev["preview"].([]interface{})
	require.Len(t, rows, 1)
	row := rows[0].(map[string]interface{})
	assert.Equal(t, "cursor", row["client_id"])
	assert.Equal(t, "mcp_cli_••••", row["credential"])
	assert.NotEmpty(t, row["precondition_token"])
	assert.NotEmpty(t, prev["precondition_token"])
	assert.NotContains(t, w.Body.String(), bindingAdminKey)
	assert.Equal(t, before, h.cursorConfig())
	toks, _ := h.sm.ListAgentTokens()
	assert.Empty(t, toks)
	assert.Equal(t, "admin_key", rowByID(t, h.get("/api/v1/clients"), "cursor")["credential_state"], "the preview's on-demand read was remembered")

	// A stale token is refused.
	w = h.do(http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", `{"apply":true,"precondition_token":"stale"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "precondition_failed", decodeBody(t, w)["code"])

	// Apply with the preview's token.
	body, _ := json.Marshal(map[string]interface{}{"apply": true, "precondition_token": prev["precondition_token"]})
	w = h.do(http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", string(body), nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	res := decodeBody(t, w)["data"].(map[string]interface{})
	assert.Equal(t, []interface{}{"cursor"}, res["upgraded"])
	assert.Equal(t, "rotate_admin_api_key", res["next_step"])
	cfg := h.cursorConfig()
	assert.Contains(t, cfg, "mcp_cli_")
	assert.NotContains(t, cfg, bindingAdminKey)
	assert.Regexp(t, regexp.MustCompile(`mcp_cli_[0-9a-f]{64}`), cfg)
	assert.Equal(t, "client", rowByID(t, h.get("/api/v1/clients"), "cursor")["credential_state"])
}

// FR-008a over the whole request: with require_mcp_auth off, naming a profile is
// refused, nothing is minted and the file is byte-identical.
func TestClientsRoutes_UpgradeWithANamedProfileIsGuarded(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.cfg.RequireMCPAuth = false
	h.writeCursor(adminKeyEntry)
	before := h.cursorConfig()

	w := h.do(http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", `{"profile":"ro"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	prev := decodeBody(t, w)["data"].(map[string]interface{})
	guard := prev["guard"].(map[string]interface{})
	assert.Equal(t, "binding_bypassable_without_auth", guard["code"])
	assert.NotEmpty(t, guard["fixes"])

	w = h.do(http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", `{"profile":"ro","apply":true}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "binding_bypassable_without_auth", decodeBody(t, w)["code"])
	assert.Equal(t, before, h.cursorConfig(), "no file written")
	toks, _ := h.sm.ListAgentTokens()
	assert.Empty(t, toks, "nothing minted")
	recs, _, _ := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	assert.Empty(t, recs, "no record")

	// Without a profile the guard never refuses.
	w = h.do(http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", `{"apply":true}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}
