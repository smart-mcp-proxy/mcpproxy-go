//go:build !server

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const bindingAdminKey = "binding-admin-key"

type bindingController struct {
	baseController
	cfg *config.Config
}

func (c *bindingController) GetCurrentConfig() *config.Config   { return c.cfg }
func (c *bindingController) GetConfig() (*config.Config, error) { return c.cfg, nil }

type bindingHarness struct {
	t   *testing.T
	srv *Server
	sm  *storage.Manager
	svc *internalRuntime.ClientsService
	key []byte
	cfg *config.Config
}

type refusingGuard struct {
	err *internalRuntime.BindingGuardError
}

func (g refusingGuard) BindingGuardDelta(_, _ internalRuntime.GuardState) []internalRuntime.BindingRef {
	return g.err.Bindings
}
func (g refusingGuard) BindingGuardFixes(internalRuntime.GuardState, []internalRuntime.BindingRef) []internalRuntime.GuardFix {
	return g.err.Fixes
}
func (g refusingGuard) BindingGuardActiveBindings() []internalRuntime.BindingRef { return nil }

func newBindingHarness(t *testing.T, guard internalRuntime.BindingGuard) *bindingHarness {
	t.Helper()
	dir := t.TempDir()
	sm, err := storage.NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sm.Close() })
	key, err := auth.GetOrCreateHMACKey(dir)
	require.NoError(t, err)

	cfg := &config.Config{APIKey: bindingAdminKey, RequireMCPAuth: true, DataDir: dir,
		Profiles: []config.ProfileConfig{{Name: "ro", Servers: []string{"a"}}, {Name: "full", Servers: []string{"a", "b"}}}}
	svc := internalRuntime.NewClientsService(internalRuntime.ClientsServiceDeps{
		Store:    sm,
		HMACKey:  func() ([]byte, error) { return key, nil },
		Config:   func() *config.Config { return cfg },
		Guard:    func() internalRuntime.BindingGuard { return guard },
		Activity: sm.SaveActivity,
	})
	srv := NewServer(&bindingController{cfg: cfg}, zap.NewNop().Sugar(), nil)
	srv.SetTokenStore(sm, dir)
	srv.SetClientsService(svc)
	return &bindingHarness{t: t, srv: srv, sm: sm, svc: svc, key: key, cfg: cfg}
}

func (h *bindingHarness) mint(clientID, prof string) string {
	h.t.Helper()
	m := h.svc.ConnectMinter()
	p := prof
	intent := connect.CredentialIntent{Profile: &p, ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue(clientID, intent)
	require.NoError(h.t, err)
	require.NoError(h.t, m.Commit(clientID, intent, issued))
	return issued.Secret
}

func (h *bindingHarness) put(clientID, body string, headers map[string]string, apiKey string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/clients/"+clientID+"/binding", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), w.Body.String())
	return out
}

func TestPutClientBinding_HappyPathShapeAndRecord(t *testing.T) {
	h := newBindingHarness(t, internalRuntime.ConservativeBindingGuard{})
	secret := h.mint("cursor", "ro")

	w := h.put("cursor", `{"profile":"full"}`, map[string]string{XMCPProxySurfaceHeader: "web"}, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)
	require.Equal(t, true, body["success"])
	data := body["data"].(map[string]interface{})
	require.Equal(t, map[string]interface{}{
		"id": "cursor", "token_name": "client-cursor", "profile": "full", "mode": "locked", "credential_state": "client",
	}, data["client"], "mode omitted keeps locked (US2-2)")
	require.Equal(t, []interface{}{}, data["warnings"])

	_, err := h.sm.ValidateAgentToken(secret, h.key)
	require.NoError(t, err, "the client's secret and config are untouched")

	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	require.Len(t, recs, 2, "the mint and this assign")
	var assign map[string]interface{}
	for _, r := range recs {
		if r.Metadata["previous_profile"] == "ro" {
			assign = r.Metadata
		}
	}
	require.NotNil(t, assign)
	require.Equal(t, "assign", assign["change"])
	require.Equal(t, "web", assign["surface"])
	require.Equal(t, "api_key", assign["actor_kind"])
	require.Equal(t, "cursor", assign["client_id"])
	require.Equal(t, "client-cursor", assign["token_name"])
}

func TestPutClientBinding_ModeSemantics(t *testing.T) {
	h := newBindingHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.mint("cursor", "ro")

	w := h.put("cursor", `{"profile":""}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	client := decodeBody(t, w)["data"].(map[string]interface{})["client"].(map[string]interface{})
	require.Equal(t, "switchable", client["mode"], `profile "" with mode omitted is All servers, switchable`)

	w = h.put("cursor", `{"profile":"","mode":"locked"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusBadRequest, w.Code)
	body := decodeBody(t, w)
	require.Equal(t, "mode", body["field"])
	require.NotEmpty(t, body["error"])

	w = h.put("cursor", `{"profile":"full","mode":"locked"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code)
	w = h.put("cursor", `{"profile":"full"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code)
	client = decodeBody(t, w)["data"].(map[string]interface{})["client"].(map[string]interface{})
	require.Equal(t, "locked", client["mode"], "omitted mode keeps the current mode")
}

func TestPutClientBinding_BadInput(t *testing.T) {
	h := newBindingHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.mint("cursor", "ro")
	cases := []struct{ name, id, body, field string }{
		{"unknown profile", "cursor", `{"profile":"ghost"}`, "profile"},
		{"missing profile", "cursor", `{"mode":"locked"}`, "profile"},
		{"empty body", "cursor", ``, "profile"},
		{"unknown field", "cursor", `{"profile":"ro","extra":1}`, "profile"},
		{"invalid id", "Bad_ID", `{"profile":"ro"}`, "id"},
		{"unknown mode", "cursor", `{"profile":"ro","mode":"sticky"}`, "mode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := h.put(c.id, c.body, nil, bindingAdminKey)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Equal(t, c.field, decodeBody(t, w)["field"])
		})
	}
	view, err := h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, "ro", view.Profile, "refused writes change nothing")
}

func TestPutClientBinding_NoActiveCredentialIs409(t *testing.T) {
	h := newBindingHarness(t, internalRuntime.ConservativeBindingGuard{})
	// a supported client that was never connected, and a custom id
	for _, id := range []string{"cursor", "acme-bot"} {
		w := h.put(id, `{"profile":"ro"}`, nil, bindingAdminKey)
		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		body := decodeBody(t, w)
		require.Equal(t, "no_client_credential", body["code"])
		require.Equal(t, "client "+id+" has no active client credential; connect it with a profile first", body["error"])
	}
	toks, err := h.sm.ListAgentTokens()
	require.NoError(t, err)
	require.Empty(t, toks, "no credential is ever minted implicitly")
	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}})
	require.NoError(t, err)
	require.Empty(t, recs)

	// revoked
	h.mint("windsurf", "ro")
	_, err = h.svc.Forget(t.Context(), internalRuntime.Actor{Kind: "api_key", Surface: "api"}, "windsurf", true)
	require.NoError(t, err)
	w := h.put("windsurf", `{"profile":"full"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Equal(t, "no_client_credential", decodeBody(t, w)["code"])
}

func TestPutClientBinding_GuardRefusalIs409WithFixes(t *testing.T) {
	refusal := &internalRuntime.BindingGuardError{
		Bindings: []internalRuntime.BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "full", Mode: "locked"}},
		Fixes:    []internalRuntime.GuardFix{{Kind: "require_mcp_auth"}, {Kind: "set_anonymous_profile", Target: "full"}},
	}
	h := newBindingHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.mint("cursor", "ro")
	h.svc = internalRuntime.NewClientsService(internalRuntime.ClientsServiceDeps{
		Store: h.sm, HMACKey: func() ([]byte, error) { return h.key, nil },
		Config: func() *config.Config { return h.cfg }, Guard: func() internalRuntime.BindingGuard { return refusingGuard{err: refusal} },
		Activity: h.sm.SaveActivity,
	})
	h.srv.SetClientsService(h.svc)

	w := h.put("cursor", `{"profile":"full"}`, nil, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	body := decodeBody(t, w)
	require.Equal(t, "binding_bypassable_without_auth", body["code"])
	require.Len(t, body["bindings"], 1)
	require.Len(t, body["fixes"], 2)
	view, err := h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, "ro", view.Profile)
}

func TestPutClientBinding_AdminGate(t *testing.T) {
	h := newBindingHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.mint("cursor", "ro")

	// a regular agent token is not an administrator
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, h.sm.CreateAgentToken(auth.AgentToken{
		Name: "bot", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}, raw, h.key))
	w := h.put("cursor", `{"profile":"full"}`, nil, raw)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())

	// a client credential is refused by prefix on every REST route
	secret := h.mint("windsurf", "ro")
	w = h.put("cursor", `{"profile":"full"}`, nil, secret)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.True(t, strings.Contains(w.Body.String(), "client credentials are valid on MCP endpoints only"))

	view, err := h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, "ro", view.Profile)
}

func TestPutClientBinding_ClientsServiceMissingIs503(t *testing.T) {
	srv := NewServer(&bindingController{cfg: &config.Config{APIKey: bindingAdminKey}}, zap.NewNop().Sugar(), nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/clients/cursor/binding", bytes.NewBufferString(`{"profile":""}`))
	req.Header.Set("X-API-Key", bindingAdminKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
