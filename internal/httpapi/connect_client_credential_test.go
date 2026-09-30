//go:build !server

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

type connectHarness struct {
	*bindingHarness
	home string
	conn *connect.Service
}

// newConnectHarness wires a real connect.Service (with the clients-service
// minter) behind the REST handlers, over a seeded Cursor config.
func newConnectHarness(t *testing.T, guard internalRuntime.BindingGuard) *connectHarness {
	t.Helper()
	h := newBindingHarness(t, guard)
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	conn := connect.NewServiceWithHome("127.0.0.1:8080", bindingAdminKey, home).
		WithRequireMCPAuth(true).
		WithCredentialMinter(h.svc.ConnectMinter())
	h.srv.SetConnectService(conn)
	cfgPath := connect.ConfigPath("cursor", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte("{}\n"), 0o644))
	return &connectHarness{bindingHarness: h, home: home, conn: conn}
}

func (h *connectHarness) do(method, path, body string, headers map[string]string, key string) *httptest.ResponseRecorder {
	h.t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, http.NoBody)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("X-API-Key", key)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, r)
	return w
}

func (h *connectHarness) cursorConfig() string {
	raw, err := os.ReadFile(connect.ConfigPath("cursor", h.home))
	require.NoError(h.t, err)
	return string(raw)
}

func TestConnectREST_MintsAClientCredentialNotTheAdminKey(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})

	w := h.do(http.MethodPost, "/api/v1/connect/cursor", `{"profile":"ro","mode":"locked"}`,
		map[string]string{XMCPProxySurfaceHeader: "web"}, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})
	require.Equal(t, "mcp_cli_••••", data["credential"])
	require.Equal(t, "client-cursor", data["token_name"])
	require.Equal(t, "ro", data["profile"])
	require.Equal(t, "locked", data["mode"])
	require.NotContains(t, w.Body.String(), "mcp_cli_"+strings.Repeat("0", 4), "no real secret in the response")

	cfg := h.cursorConfig()
	require.NotContains(t, cfg, bindingAdminKey, "the admin API key is never written")
	require.Contains(t, cfg, "mcp_cli_")

	view, err := h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, "ro", view.Profile)
	require.Equal(t, "locked", view.Mode)

	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, "web", recs[0].Metadata["surface"])
	require.Equal(t, "", recs[0].Metadata["previous_profile"])

	// the credential in the config authenticates as the client
	secret := extractSecret(t, cfg)
	tok, err := h.sm.ValidateAgentToken(secret, h.key)
	require.NoError(t, err)
	require.Equal(t, "client-cursor", tok.Name)
}

func extractSecret(t *testing.T, cfg string) string {
	t.Helper()
	i := strings.Index(cfg, "mcp_cli_")
	require.GreaterOrEqual(t, i, 0)
	return cfg[i : i+72]
}

func TestConnectREST_ReconnectKeepsTheBindingUnlessAProfileIsGiven(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/v1/connect/cursor", `{"profile":"ro"}`, nil, bindingAdminKey).Code)
	old := extractSecret(t, h.cursorConfig())

	// A plain reconnect (the Web UI sends no profile) rotates the secret but
	// never silently widens a locked client.
	w := h.do(http.MethodPost, "/api/v1/connect/cursor", `{"force":true}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})
	require.Equal(t, "finalized", data["rotation"])
	require.Equal(t, "ro", data["profile"])
	require.Equal(t, "locked", data["mode"])
	fresh := extractSecret(t, h.cursorConfig())
	require.NotEqual(t, old, fresh)
	_, err := h.sm.ValidateAgentToken(old, h.key)
	require.Error(t, err, "the old secret stops authenticating once the new one is written")
	_, err = h.sm.ValidateAgentToken(fresh, h.key)
	require.NoError(t, err)

	// An explicit profile changes the binding.
	w = h.do(http.MethodPost, "/api/v1/connect/cursor", `{"force":true,"profile":"full","mode":"switchable"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	view, err := h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, "full", view.Profile)
	require.Equal(t, "switchable", view.Mode)
}

func TestConnectREST_RefusalsWriteNothing(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	before := h.cursorConfig()

	cases := []struct {
		name, body, field string
		status            int
	}{
		{"unknown profile", `{"profile":"ghost"}`, "profile", http.StatusBadRequest},
		{"locked without a profile", `{"profile":"","mode":"locked"}`, "mode", http.StatusBadRequest},
		{"bad mode", `{"profile":"ro","mode":"sticky"}`, "mode", http.StatusBadRequest},
		{"keyless with auth on", `{"keyless":true}`, "keyless", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := h.do(http.MethodPost, "/api/v1/connect/cursor", c.body, nil, bindingAdminKey)
			require.Equal(t, c.status, w.Code, w.Body.String())
			require.Equal(t, c.field, decodeBody(t, w)["field"])
			require.Equal(t, before, h.cursorConfig())
		})
	}
	toks, err := h.sm.ListAgentTokens()
	require.NoError(t, err)
	require.Empty(t, toks, "nothing minted for a refused connect")
}

func TestConnectREST_KeylessWithProfileIs400WhileAuthIsOff(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.conn.WithRequireMCPAuth(false)
	w := h.do(http.MethodPost, "/api/v1/connect/cursor", `{"keyless":true,"profile":"ro"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Equal(t, "keyless", decodeBody(t, w)["field"])

	w = h.do(http.MethodPost, "/api/v1/connect/cursor", `{"keyless":true}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})
	require.Equal(t, true, data["keyless"])
	require.NotContains(t, h.cursorConfig(), "mcp_cli_")
}

func TestConnectREST_TokenNameConflictIs409(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	// a grandfathered regular token that already holds the name client-cursor
	tok := auth.AgentToken{
		Name: "client-cursor", TokenHash: "deadbeef", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	raw, err := json.Marshal(tok)
	require.NoError(t, err)
	require.NoError(t, h.sm.GetDB().Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(storage.AgentTokensBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte("deadbeef"), raw)
	}))
	before := h.cursorConfig()

	w := h.do(http.MethodPost, "/api/v1/connect/cursor", `{"profile":"ro"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	body := decodeBody(t, w)
	require.Equal(t, "token name client-cursor is held by a regular agent token", body["error"])
	require.Equal(t, "client-cursor", body["conflicting_token"])
	require.Equal(t, "revoke or delete token client-cursor, then connect again", body["remediation"])
	require.Equal(t, before, h.cursorConfig(), "no config write")
	got, err := h.sm.GetAgentTokenByName("client-cursor")
	require.NoError(t, err)
	require.Equal(t, "deadbeef", got.TokenHash, "that token is untouched")
}

func TestConnectREST_GuardRefusalIs409AndWritesNothing(t *testing.T) {
	refusal := &internalRuntime.BindingGuardError{
		Bindings: []internalRuntime.BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "ro", Mode: "locked"}},
		Fixes:    []internalRuntime.GuardFix{{Kind: "require_mcp_auth"}, {Kind: "set_anonymous_profile", Target: "ro"}},
	}
	h := newConnectHarness(t, refusingGuard{err: refusal})
	before := h.cursorConfig()

	w := h.do(http.MethodPost, "/api/v1/connect/cursor", `{"profile":"ro"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	body := decodeBody(t, w)
	require.Equal(t, "binding_bypassable_without_auth", body["code"])
	require.Len(t, body["fixes"], 2)
	require.Equal(t, before, h.cursorConfig())
	toks, err := h.sm.ListAgentTokens()
	require.NoError(t, err)
	require.Empty(t, toks, "nothing minted")
}

func TestConnectREST_PreviewMasksAndEchoesIntent(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	w := h.do(http.MethodGet, "/api/v1/connect/cursor/preview?profile=ro&mode=locked", "", nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]interface{})
	require.Equal(t, "mcp_cli_••••", data["credential"])
	require.Equal(t, false, data["contains_api_key"])
	require.Equal(t, "ro", data["profile"])
	require.Equal(t, "locked", data["mode"])
	require.NotContains(t, w.Body.String(), bindingAdminKey)
	toks, err := h.sm.ListAgentTokens()
	require.NoError(t, err)
	require.Empty(t, toks, "a preview mints nothing")

	w = h.do(http.MethodGet, "/api/v1/connect/cursor/preview?keyless=true", "", nil, bindingAdminKey)
	require.Equal(t, http.StatusBadRequest, w.Code, "keyless preview with auth on")
	require.Equal(t, "keyless", decodeBody(t, w)["field"])
}

func TestConnectREST_UndoRevokesTheMintedCredential(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	w := h.do(http.MethodPost, "/api/v1/connect/cursor", `{"profile":"ro"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	res := decodeBody(t, w)["data"].(map[string]interface{})
	secret := extractSecret(t, h.cursorConfig())

	body, _ := json.Marshal(UndoConnectRequest{BackupName: filepath.Base(res["backup_path"].(string))})
	w = h.do(http.MethodPost, "/api/v1/connect/cursor/undo", string(body), nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	undone := decodeBody(t, w)["data"].(map[string]interface{})
	require.Equal(t, "client-cursor", undone["credential_revoked"])
	_, err := h.sm.ValidateAgentToken(secret, h.key)
	require.Error(t, err, "the revoked secret no longer authenticates")

	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	var forgets int
	for _, r := range recs {
		if r.Metadata["change"] == "forget" {
			forgets++
			require.Equal(t, "undo", r.Metadata["diff"].(map[string]interface{})["reason"])
		}
	}
	require.Equal(t, 1, forgets)
}

// T030b: the connect reads are administrator-only, and classify credentials.
func TestConnectREST_AdminOnlyReadsAndCredentialState(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/v1/connect/cursor", `{"profile":"ro"}`, nil, bindingAdminKey).Code)

	regular := func(pin string) string {
		raw, err := auth.GenerateToken()
		require.NoError(t, err)
		require.NoError(t, h.sm.CreateAgentToken(auth.AgentToken{
			Name: "bot-" + pin, AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead}, ProfilePin: pin,
			ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		}, raw, h.key))
		return raw
	}
	for _, key := range []string{regular(""), regular("ro")} {
		for _, path := range []string{"/api/v1/connect", "/api/v1/connect/cursor", "/api/v1/connect/cursor/preview"} {
			w := h.do(http.MethodGet, path, "", nil, key)
			require.Equal(t, http.StatusForbidden, w.Code, path)
			require.NotContains(t, w.Body.String(), "credential_state")
			require.NotContains(t, w.Body.String(), "cursor")
		}
	}

	w := h.do(http.MethodGet, "/api/v1/connect", "", nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code)
	var rows []connect.ClientStatus
	var env struct {
		Data []connect.ClientStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	rows = env.Data
	var cursorRow *connect.ClientStatus
	for i := range rows {
		if rows[i].ID == "cursor" {
			cursorRow = &rows[i]
		}
	}
	require.NotNil(t, cursorRow)
	require.Equal(t, "unknown", cursorRow.CredentialState, "the stat-only listing never reads a config")

	w = h.do(http.MethodGet, "/api/v1/connect/cursor", "", nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "client", decodeBody(t, w)["data"].(map[string]interface{})["credential_state"])

	// a config that still holds the admin key classifies as admin_key
	cfgPath := connect.ConfigPath("cursor", h.home)
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"mcpServers":{"mcpproxy":{"type":"sse","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"`+bindingAdminKey+`"}}}}`), 0o644))
	w = h.do(http.MethodGet, "/api/v1/connect/cursor", "", nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "admin_key", decodeBody(t, w)["data"].(map[string]interface{})["credential_state"])
	require.NotContains(t, w.Body.String(), bindingAdminKey+`"}`, "the credential value is classified, never echoed")
}
