//go:build !server

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 115 REST coverage: POST/DELETE /api/v1/tokens and POST /api/v1/clients
// go through CredentialsService (T022), every issuance screens its raw values
// (T038b, T038d, T038f) and malformed bodies never echo caller input (T038e).

type credRESTHarness struct {
	*bindingHarness
	mu     sync.Mutex
	events []internalRuntime.Event
}

func newCredRESTHarness(t *testing.T) *credRESTHarness {
	t.Helper()
	h := &credRESTHarness{bindingHarness: newBindingHarness(t, internalRuntime.ConservativeBindingGuard{})}
	cs := internalRuntime.NewCredentialsService(internalRuntime.CredentialsServiceDeps{
		Tokens: h.sm, Clients: h.svc, HMACKey: func() ([]byte, error) { return h.key, nil },
		Config: func() *config.Config { return h.cfg }, Activity: h.sm.SaveActivity,
		Publish: func(e internalRuntime.Event) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.events = append(h.events, e)
		},
	})
	h.srv.SetCredentialsService(cs)
	return h
}

func (h *credRESTHarness) req(method, path, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-API-Key", bindingAdminKey)
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, r)
	return w
}

func (h *credRESTHarness) credEvents() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, e := range h.events {
		if e.Type == internalRuntime.EventTypeCredentialsChanged {
			n++
		}
	}
	return n
}

func (h *credRESTHarness) records(change string) int {
	h.t.Helper()
	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Types: []string{"profile_change"}, Limit: 500})
	require.NoError(h.t, err)
	n := 0
	for _, r := range recs {
		if r.Metadata["change"] == change {
			n++
		}
	}
	return n
}

func (h *credRESTHarness) tokenCount() int {
	h.t.Helper()
	all, err := h.sm.ListAgentTokens()
	require.NoError(h.t, err)
	return len(all)
}

// sinks returns every REST/store surface a secret must never reach.
func (h *credRESTHarness) sinks() string {
	h.t.Helper()
	var b strings.Builder
	b.WriteString(h.req(http.MethodGet, "/api/v1/tokens", "").Body.String())
	b.WriteString(h.req(http.MethodGet, "/api/v1/clients", "").Body.String())
	recs, _, err := h.sm.ListActivities(storage.ActivityFilter{Limit: 1000})
	require.NoError(h.t, err)
	raw, _ := json.Marshal(recs)
	b.Write(raw)
	h.mu.Lock()
	ev, _ := json.Marshal(h.events)
	h.mu.Unlock()
	b.Write(ev)
	if db, err := os.ReadFile(filepath.Join(h.cfg.DataDir, "config.db")); err == nil {
		b.Write(db)
	}
	return b.String()
}

func TestCredentialsREST_TokensLifecycleAuditAndFields(t *testing.T) {
	h := newCredRESTHarness(t)
	h.cfg.RequireMCPAuth = false // A9: REST still issues with auth off
	w := h.req(http.MethodPost, "/api/v1/tokens", `{"name":"ci","profile":"ro","expires_in":"2h","purpose":"nightly sync"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	secret := decodeBody(t, w)["data"].(map[string]any)["token"].(string)
	assert.Equal(t, 1, h.records("issue"))
	assert.Equal(t, 1, h.credEvents())

	got := decodeBody(t, h.req(http.MethodGet, "/api/v1/tokens/ci", ""))["data"].(map[string]any)
	assert.Equal(t, "nightly sync", got["purpose"])
	assert.Equal(t, "api", got["issuer"].(map[string]any)["surface"])
	assert.Equal(t, true, got["lease"], "2h is a task lease")
	assert.Equal(t, "ok", got["profile_state"])
	assert.Nil(t, got["revoked_at"])

	require.Equal(t, http.StatusNoContent, h.req(http.MethodDelete, "/api/v1/tokens/ci", "").Code)
	require.Equal(t, http.StatusNoContent, h.req(http.MethodDelete, "/api/v1/tokens/ci", "").Code, "idempotent")
	assert.Equal(t, 1, h.records("revoke"), "an idempotent revoke writes no second record")
	got = decodeBody(t, h.req(http.MethodGet, "/api/v1/tokens/ci", ""))["data"].(map[string]any)
	assert.NotEmpty(t, got["revoked_at"])
	assert.Equal(t, http.StatusNotFound, h.req(http.MethodDelete, "/api/v1/tokens/nope", "").Code)
	assert.NotContains(t, h.sinks(), secret)
}

func TestCredentialsREST_CustomClientRecordsIssue(t *testing.T) {
	h := newCredRESTHarness(t)
	w := h.req(http.MethodPost, "/api/v1/clients", `{"id":"dev-box","profile":"ro","expires_in":"12h","purpose":"ci runner"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	data := decodeBody(t, w)["data"].(map[string]any)
	row := data["client"].(map[string]any)
	assert.Equal(t, "ci runner", row["purpose"])
	assert.Equal(t, true, row["lease"])
	assert.Equal(t, "ok", row["profile_state"])
	assert.Equal(t, 1, h.records("issue"))
	assert.Equal(t, 0, h.records("assign"), "FR-010: custom add records issue, not assign")
	// An exact ?client= filter shows the revoked custom row (UI-001).
	require.Equal(t, http.StatusOK, h.req(http.MethodDelete, "/api/v1/clients/dev-box", "").Code)
	list := decodeBody(t, h.req(http.MethodGet, "/api/v1/clients?client=dev-box", ""))["data"].(map[string]any)["clients"].([]any)
	require.Len(t, list, 1)
	assert.Equal(t, "revoked", list[0].(map[string]any)["credential_state"])
	assert.NotEmpty(t, list[0].(map[string]any)["revoked_at"])
	all := decodeBody(t, h.req(http.MethodGet, "/api/v1/clients", ""))["data"].(map[string]any)["clients"].([]any)
	found := false
	for _, r := range all {
		row := r.(map[string]any)
		if row["id"] == "dev-box" {
			found = true
			assert.Equal(t, "revoked", row["credential_state"])
		}
	}
	assert.True(t, found, "an open, unfiltered Clients view keeps the revoked worker")
}

// T038b + T038d + T038f: secret-shaped values on the REST issuance routes.
func TestCredentialsREST_SecretScreen(t *testing.T) {
	h := newCredRESTHarness(t)
	w := h.req(http.MethodPost, "/api/v1/tokens", `{"name":"seed","profile":"ro"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	agentSecret := decodeBody(t, w)["data"].(map[string]any)["token"].(string)
	w = h.req(http.MethodPost, "/api/v1/clients", `{"id":"seed-c","profile":"ro"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	clientSecret := decodeBody(t, w)["data"].(map[string]any)["credential"].(string)
	secrets := []string{agentSecret, clientSecret, bindingAdminKey}

	tokens, issues, events := h.tokenCount(), h.records("issue"), h.credEvents()
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	for _, s := range secrets {
		for _, c := range []struct {
			path, body, field string
		}{
			{"/api/v1/tokens", `{"name":"x","purpose":` + q(s) + `}`, "purpose"},
			{"/api/v1/tokens", `{"name":` + q(s) + `}`, "name"},
			{"/api/v1/tokens", `{"name":"x","permissions":[` + q(s) + `]}`, "permissions"},
			{"/api/v1/tokens", `{"name":"x","permissions":["read",` + q(s) + `]}`, "permissions"},
			{"/api/v1/tokens", `{"name":"x","allowed_servers":[` + q(s) + `]}`, "allowed_servers"},
			{"/api/v1/tokens", `{"name":"x","allowed_servers":["*",` + q(s) + `]}`, "allowed_servers"},
			{"/api/v1/tokens", `{"name":"x","profile_pin":` + q(s) + `}`, "profile_pin"},
			{"/api/v1/tokens", `{"name":"x","profile":` + q(s) + `,"profile_pin":"ro"}`, "profile"},
			{"/api/v1/tokens", `{"name":"x","expires_in":` + q(s) + `}`, "expires_in"},
			{"/api/v1/tokens", `{"name":"x","expires_in":` + q(s+"d") + `}`, "expires_in"},
			{"/api/v1/clients", `{"id":"x","purpose":` + q(s) + `}`, "purpose"},
			{"/api/v1/clients", `{"id":"x","display_name":` + q(s) + `}`, "display_name"},
			{"/api/v1/clients", `{"id":` + q(s) + `}`, "id"},
			{"/api/v1/clients", `{"id":"x","expires_in":` + q(s) + `}`, "expires_in"},
		} {
			w := h.req(http.MethodPost, c.path, c.body)
			require.Equal(t, http.StatusBadRequest, w.Code, "%s %s: %s", c.path, c.field, w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, c.field, body["field"], "%s %s", c.path, c.body)
			assert.Equal(t, profile.CredentialErrorCodeSecretInArgument, body["code"])
			for _, sec := range secrets {
				assert.NotContains(t, w.Body.String(), sec)
			}
		}
	}
	assert.Equal(t, tokens, h.tokenCount(), "nothing minted")
	assert.Equal(t, issues, h.records("issue"), "nothing audited")
	assert.Equal(t, events, h.credEvents(), "nothing published")
	sinks := h.sinks()
	for _, s := range []string{agentSecret, clientSecret} {
		assert.NotContains(t, sinks, s)
	}
	assert.NotContains(t, sinks, bindingAdminKey)

	// Control rows keep today's texts.
	w = h.req(http.MethodPost, "/api/v1/tokens", `{"name":"c1","permissions":["read","bogus"]}`)
	assert.Contains(t, w.Body.String(), `invalid permission: \"bogus\"`)
	w = h.req(http.MethodPost, "/api/v1/tokens", `{"name":"c1","allowed_servers":["nosuchserver"]}`)
	assert.Contains(t, w.Body.String(), `unknown server: \"nosuchserver\"`)
	w = h.req(http.MethodPost, "/api/v1/tokens", `{"name":"c1","expires_in":"bogus"}`)
	assert.Contains(t, w.Body.String(), `invalid expiry duration: \"bogus\"`)
	w = h.req(http.MethodPost, "/api/v1/clients", `{"id":"c1","expires_in":"bogus"}`)
	assert.Contains(t, w.Body.String(), `invalid expiry duration: \"bogus\"`)
	w = h.req(http.MethodPost, "/api/v1/tokens", `{"name":"c1","profile":"ro","profile_pin":"full"}`)
	assert.Contains(t, w.Body.String(), `name different profiles`)
}

// T038e: malformed bodies on the issuance routes never echo a key or value.
func TestCredentialsREST_SanitizedDecodeErrors(t *testing.T) {
	h := newCredRESTHarness(t)
	secret := "mcp_agt_" + strings.Repeat("ab", 32)
	for _, c := range []struct{ path, body string }{
		{"/api/v1/clients", `{"id":"w1",` + fmt.Sprintf("%q", secret) + `:1}`},
		{"/api/v1/clients", `{"id":"w1","mode":{` + fmt.Sprintf("%q", secret) + `:1}}`},
		{"/api/v1/clients", `{"id":"w1","display_name":{` + fmt.Sprintf("%q", secret) + `:"x"}}`},
		{"/api/v1/clients", `{"id":12345678901234}`},
		{"/api/v1/tokens", `{"name":"x","permissions":{` + fmt.Sprintf("%q", secret) + `:1}}`},
		{"/api/v1/tokens", `{"name":12345678901234}`},
		{"/api/v1/tokens", `{"name":` + fmt.Sprintf("%q", secret) + ` oops}`},
		{"/api/v1/clients", ``},
	} {
		w := h.req(http.MethodPost, c.path, c.body)
		require.Equal(t, http.StatusBadRequest, w.Code, c.body)
		assert.NotContains(t, w.Body.String(), secret)
		assert.NotContains(t, w.Body.String(), "12345678901234")
		assert.NotContains(t, w.Body.String(), "json:")
	}
	// The tokens route keeps ignoring unknown keys; nothing of them is stored.
	w := h.req(http.MethodPost, "/api/v1/tokens", `{"name":"lenient",`+fmt.Sprintf("%q", "zz-unknown-key")+`:"zz-unknown-value"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.NotContains(t, h.sinks(), "zz-unknown")
	assert.Equal(t, 0, h.records("assign"))
	_ = context.Background()
}

func TestDecodeIssuanceBody_Taxonomy(t *testing.T) {
	type body struct {
		ID   string `json:"id"`
		Mode string `json:"mode"`
	}
	cases := []struct {
		raw    string
		strict bool
		kind   string
		field  string
	}{
		{`{"id":"a","nope":1}`, true, IssuanceDecodeUnknownField, "(unknown argument)"},
		{`{"id":1}`, false, IssuanceDecodeWrongType, "id"},
		{`{"mode":{"x":1}}`, false, IssuanceDecodeWrongType, "mode"},
		{`{"id":"a"`, false, IssuanceDecodeSyntax, ""},
		{``, false, IssuanceDecodeEmpty, ""},
		{`{"id":"a"} {"id":"b"}`, false, IssuanceDecodeOther, ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.raw))
		var v body
		err := decodeIssuanceBody(r, &v, c.strict)
		require.NotNil(t, err, c.raw)
		assert.Equal(t, c.kind, err.Kind, c.raw)
		assert.Equal(t, c.field, err.Field, c.raw)
		assert.NotContains(t, err.Error(), "nope")
	}
}
