//go:build !server

package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-f T070a (FR-008a, zcode round 2): every mutating route under
// /profiles, /clients, /connect and /config is either GUARDED - driven here with
// a refusing guard and required to answer the 409 and write nothing - or
// EXEMPT with a written reason. A route in neither list fails, so a new write
// path cannot ship without someone deciding which it is.
//
// The profile routes are driven against a service whose write path returns the
// guard's refusal (proving the route reaches the service and maps the refusal);
// that the real guard is the one in that service, and that nothing is written,
// is proven end to end in internal/server (TestProfileV3REST_GuardedWritesAre
// RefusedAndWriteNothing) and in internal/runtime (TestProfilesService_Guard
// RefusalChangesNothing).

// guardedRoutes: each is driven by the fixture named beside it.
var guardedRoutes = map[string]string{
	"POST /api/v1/profiles":                          "profiles service Create",
	"PUT /api/v1/profiles/{name}":                    "profiles service Update",
	"POST /api/v1/profiles/{name}/rename":            "profiles service Rename",
	"DELETE /api/v1/profiles/{name}":                 "profiles service Delete",
	"POST /api/v1/clients":                           "ClientsService.Add",
	"PUT /api/v1/clients/{client}/binding":           "ClientsService.SetBinding",
	"POST /api/v1/clients/bulk-assign":               "ClientsService.BulkAssign (per client, reported in skipped[])",
	"POST /api/v1/clients/upgrade-admin-key-holders": "whole-request guard",
	"POST /api/v1/connect/{client}":                  "connect minter Issue",
	"PATCH /api/v1/config":                           "config funnel",
	"POST /api/v1/config/apply":                      "config funnel",
	"PATCH /api/v1/config/docker-isolation":          "config funnel",
}

// exemptRoutes: why each cannot change a binding's reach.
var exemptRoutes = map[string]string{
	"POST /api/v1/clients/{client}/rotate":          "keeps the binding; only swaps the secret",
	"POST /api/v1/clients/{client}/rotate/finalize": "promotes a pending secret; the binding is untouched",
	"DELETE /api/v1/clients/{client}":               "revokes a credential: a binding can only disappear",
	"DELETE /api/v1/connect/{client}":               "removes a config entry; never grants",
	"POST /api/v1/connect/{client}/undo":            "restores the previous entry and only revokes a credential the restored config no longer holds",
	"POST /api/v1/profiles/try":                     "nothing is written: a draft is searched, not stored",
	"PUT /api/v1/profiles/active":                   "a UI default only; never enforcement (a live session's selection and every credential's binding are separate)",
	"POST /api/v1/config/validate":                  "validation only; nothing is written",
}

func inGuardedFamily(route string) bool {
	for _, prefix := range []string{"/api/v1/profiles", "/api/v1/clients", "/api/v1/connect", "/api/v1/config"} {
		if route == prefix || strings.HasPrefix(route, prefix+"/") {
			return true
		}
	}
	return false
}

func TestBindingGuardCoverage_EveryMutatingRouteIsGuardedOrExempt(t *testing.T) {
	h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
	found := map[string]bool{}
	err := chi.Walk(h.srv.Router(), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		switch method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return nil
		}
		route = strings.TrimSuffix(route, "/")
		if inGuardedFamily(route) {
			found[method+" "+route] = true
		}
		return nil
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(found), 18, "the walk found almost nothing: the route table changed shape")

	var unclassified, stale []string
	for key := range found {
		_, guarded := guardedRoutes[key]
		_, exempt := exemptRoutes[key]
		switch {
		case guarded && exempt:
			t.Errorf("%s is in both lists", key)
		case !guarded && !exempt:
			unclassified = append(unclassified, key)
		}
	}
	for key := range guardedRoutes {
		if !found[key] {
			stale = append(stale, key)
		}
	}
	for key := range exemptRoutes {
		if !found[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	assert.Empty(t, unclassified, "a mutating route with no FR-008a classification: add it to guardedRoutes (with a driver) or exemptRoutes (with a reason)")
	assert.Empty(t, stale, "a classified route that no longer exists")
	for key, reason := range exemptRoutes {
		assert.NotEmpty(t, strings.TrimSpace(reason), key)
	}
}

// guardErrProfiles is a ProfilesAPI whose every write is refused by the guard.
type guardErrProfiles struct{ *fakeProfiles }

func (g guardErrProfiles) Create(context.Context, internalRuntime.Actor, config.ProfileConfig) (*internalRuntime.WriteResult, error) {
	return nil, guardRefusal()
}
func (g guardErrProfiles) Update(context.Context, internalRuntime.Actor, string, config.ProfileConfig) (*internalRuntime.WriteResult, error) {
	return nil, guardRefusal()
}
func (g guardErrProfiles) Rename(context.Context, internalRuntime.Actor, string, string) (*internalRuntime.RenameResult, error) {
	return nil, guardRefusal()
}
func (g guardErrProfiles) Delete(context.Context, internalRuntime.Actor, string, string, bool) (*internalRuntime.DeleteResult, error) {
	return nil, guardRefusal()
}

func TestBindingGuardCoverage_GuardedRoutesAnswer409AndWriteNothing(t *testing.T) {
	refusal := &internalRuntime.BindingGuardError{
		Bindings: []internalRuntime.BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "ro", Mode: "locked"}},
		Fixes:    []internalRuntime.GuardFix{{Kind: "require_mcp_auth"}},
	}
	newRefusing := func(t *testing.T) *connectHarness {
		h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
		h.svc = internalRuntime.NewClientsService(internalRuntime.ClientsServiceDeps{
			Store: h.sm, HMACKey: func() ([]byte, error) { return h.key, nil },
			Config: func() *config.Config { return h.cfg }, Guard: func() internalRuntime.BindingGuard { return refusingGuard{err: refusal} },
			Activity: h.sm.SaveActivity,
		})
		h.srv.SetClientsService(h.svc)
		h.conn.WithCredentialMinter(h.svc.ConnectMinter())
		return h
	}
	assert409 := func(t *testing.T, w *httptest.ResponseRecorder) {
		t.Helper()
		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		body := decodeBody(t, w)
		assert.Equal(t, "binding_bypassable_without_auth", body["code"])
		assert.NotEmpty(t, body["bindings"])
		assert.NotEmpty(t, body["fixes"])
	}
	nothingMinted := func(t *testing.T, h *connectHarness) {
		t.Helper()
		toks, err := h.sm.ListAgentTokens()
		require.NoError(t, err)
		assert.Empty(t, toks, "a refused write mints nothing")
	}

	t.Run("profile routes map the refusal", func(t *testing.T) {
		cfg := profilesFixtureConfig()
		ctrl := &scopeController{cfg: cfg, servers: scopeFixtureServers(), withManagement: true}
		srv, _ := scopedAgentServer(t, ctrl, []string{"alpha"})
		srv.SetProfilesService(guardErrProfiles{&fakeProfiles{cfg: cfg}})
		g := &profilesRig{t: t, admin: srv, scoped: srv}
		for _, rt := range []struct{ method, path, body string }{
			{http.MethodPost, "/api/v1/profiles", `{"name":"x","servers":["alpha"]}`},
			{http.MethodPut, "/api/v1/profiles/research", `{"name":"research"}`},
			{http.MethodPost, "/api/v1/profiles/research/rename", `{"new_name":"r2"}`},
			{http.MethodDelete, "/api/v1/profiles/research?reassign_to=shared", ``},
		} {
			assert409(t, g.do(scopeAdminAPIKey, rt.method, rt.path, rt.body))
		}
	})

	t.Run("POST /clients", func(t *testing.T) {
		h := newRefusing(t)
		assert409(t, h.do(http.MethodPost, "/api/v1/clients", `{"id":"dev","profile":"ro"}`, nil, bindingAdminKey))
		nothingMinted(t, h)
	})

	t.Run("PUT /clients/{client}/binding", func(t *testing.T) {
		h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
		h.mint("cursor", "ro")
		h.svc = internalRuntime.NewClientsService(internalRuntime.ClientsServiceDeps{
			Store: h.sm, HMACKey: func() ([]byte, error) { return h.key, nil },
			Config: func() *config.Config { return h.cfg }, Guard: func() internalRuntime.BindingGuard { return refusingGuard{err: refusal} },
			Activity: h.sm.SaveActivity,
		})
		h.srv.SetClientsService(h.svc)
		assert409(t, h.do(http.MethodPut, "/api/v1/clients/cursor/binding", `{"profile":"full"}`, nil, bindingAdminKey))
		v, err := h.svc.Get("cursor")
		require.NoError(t, err)
		assert.Equal(t, "ro", v.Profile, "the binding did not move")
	})

	t.Run("POST /clients/bulk-assign reports every client skipped", func(t *testing.T) {
		h := newConnectHarness(t, internalRuntime.ConservativeBindingGuard{})
		h.mint("cursor", "ro")
		h.svc = internalRuntime.NewClientsService(internalRuntime.ClientsServiceDeps{
			Store: h.sm, HMACKey: func() ([]byte, error) { return h.key, nil },
			Config: func() *config.Config { return h.cfg }, Guard: func() internalRuntime.BindingGuard { return refusingGuard{err: refusal} },
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
		v, err := h.svc.Get("cursor")
		require.NoError(t, err)
		assert.Equal(t, "ro", v.Profile)
	})

	t.Run("POST /clients/upgrade-admin-key-holders with a named profile", func(t *testing.T) {
		h := newRefusing(t)
		h.cfg.RequireMCPAuth = false
		h.writeCursor(adminKeyEntry)
		before := h.cursorConfig()
		assert409(t, h.do(http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", `{"profile":"ro","apply":true}`, nil, bindingAdminKey))
		assert.Equal(t, before, h.cursorConfig())
		nothingMinted(t, h)
	})

	t.Run("POST /connect/{client}", func(t *testing.T) {
		h := newRefusing(t)
		before := h.cursorConfig()
		assert409(t, h.do(http.MethodPost, "/api/v1/connect/cursor", `{"profile":"ro"}`, nil, bindingAdminKey))
		assert.Equal(t, before, h.cursorConfig())
		nothingMinted(t, h)
	})

	for _, rt := range []struct{ name, method, path, body string }{
		{"PATCH /config", http.MethodPatch, "/api/v1/config", `{"require_mcp_auth":false}`},
		{"POST /config/apply", http.MethodPost, "/api/v1/config/apply", `{"listen":"127.0.0.1:8080","require_mcp_auth":false}`},
		{"PATCH /config/docker-isolation", http.MethodPatch, "/api/v1/config/docker-isolation", `{"enabled":true}`},
	} {
		t.Run(rt.name, func(t *testing.T) {
			srv := NewServer(&applyErrController{err: refusal}, zap.NewNop().Sugar(), nil)
			req := httptest.NewRequest(rt.method, rt.path, bytes.NewReader([]byte(rt.body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "k")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			assert409(t, w)
		})
	}
}
