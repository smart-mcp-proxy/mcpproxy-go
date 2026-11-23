//go:build !server

package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

const oauthTestOrigin = "http://127.0.0.1:8080"
const oauthTestRedirect = "http://127.0.0.1:54321/callback"

type oauthHarness struct {
	*bindingHarness
	svc *internalRuntime.OAuthLoginService
}

func newOAuthHarness(t *testing.T, on bool) *oauthHarness {
	t.Helper()
	h := newBindingHarness(t, internalRuntime.ConservativeBindingGuard{})
	h.cfg.MCPOAuthEnabled = on
	svc := internalRuntime.NewOAuthLoginService(internalRuntime.OAuthLoginDeps{
		Store:    h.sm,
		HMACKey:  func() ([]byte, error) { return h.key, nil },
		Config:   func() *config.Config { return h.cfg },
		Guard:    func() internalRuntime.BindingGuard { return internalRuntime.ConservativeBindingGuard{} },
		Activity: h.sm.SaveActivity,
	})
	h.srv.SetOAuthLoginService(svc)
	h.svc.SetLoginCanceler(func(id string) { svc.CancelForClient(id) })
	return &oauthHarness{bindingHarness: h, svc: svc}
}

func (h *oauthHarness) call(method, path, body, key string) *httptest.ResponseRecorder {
	h.t.Helper()
	var rdr *bytes.Buffer
	if body != "" {
		rdr = bytes.NewBufferString(body)
	} else {
		rdr = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, req)
	return w
}

// pending registers a client and starts an authorization for the per-client
// address of client ("" = the generic address); it returns the request id plus
// what the token exchange needs.
func (h *oauthHarness) pending(client string) (id, regID, verifier, resource string) {
	h.t.Helper()
	reg, rerr, err := h.svc.Register(mcplogin.RegistrationRequest{RedirectURIs: []string{oauthTestRedirect}, ClientName: "Cursor IDE"})
	require.NoError(h.t, err)
	require.Nil(h.t, rerr)
	regID, _ = reg["client_id"].(string)
	verifier = strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	resource = oauthTestOrigin + mcplogin.ResourcePath(client)
	id, aerr := h.svc.BeginAuthorization(internalRuntime.AuthorizeInput{
		Origin: oauthTestOrigin, ClientID: regID, RedirectURI: oauthTestRedirect, State: "st",
		CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]), Method: "S256", Resource: resource, ResponseType: "code",
	})
	require.Nil(h.t, aerr)
	return id, regID, verifier, resource
}

func dataOf(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	d, _ := decodeBody(t, w)["data"].(map[string]interface{})
	return d
}

func codeOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	u, err := url.Parse(dataOf(t, w)["redirect_url"].(string))
	require.NoError(t, err)
	require.NotEmpty(t, u.Query().Get("code"))
	return u.Query().Get("code")
}

// T040: administrators only; agent tokens and client credentials are 403 before
// anything reaches the service.
func TestOAuthLoginREST_AdministratorOnly(t *testing.T) {
	h := newOAuthHarness(t, true)
	secret := h.mint("cursor", "ro")
	id, _, _, _ := h.pending("cursor")
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/oauth/authorizations", ""},
		{http.MethodGet, "/api/v1/oauth/authorizations/" + id, ""},
		{http.MethodPost, "/api/v1/oauth/authorizations/" + id + "/preview", `{"client":{"id":"cursor"}}`},
		{http.MethodPost, "/api/v1/oauth/authorizations/" + id + "/approve", `{"client":{"id":"cursor"}}`},
		{http.MethodPost, "/api/v1/oauth/authorizations/" + id + "/deny", ``},
		{http.MethodDelete, "/api/v1/clients/cursor/logins/whatever", ``},
		{http.MethodDelete, "/api/v1/clients/cursor/api-key", ``},
	} {
		w := h.call(rt.method, rt.path, rt.body, secret)
		assert.Equal(t, http.StatusForbidden, w.Code, rt.method+" "+rt.path)
	}
	// Nothing was decided.
	assert.Len(t, h.svc.ListPending(), 1)
}

func TestOAuthLoginREST_FeatureDisabled404(t *testing.T) {
	h := newOAuthHarness(t, false)
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/oauth/authorizations", ""},
		{http.MethodGet, "/api/v1/oauth/authorizations/x", ""},
		{http.MethodPost, "/api/v1/oauth/authorizations/x/preview", `{}`},
		{http.MethodPost, "/api/v1/oauth/authorizations/x/approve", `{}`},
		{http.MethodPost, "/api/v1/oauth/authorizations/x/deny", ``},
	} {
		w := h.call(rt.method, rt.path, rt.body, bindingAdminKey)
		assert.Equal(t, http.StatusNotFound, w.Code, rt.path)
		assert.Equal(t, "feature_disabled", decodeBody(t, w)["code"], rt.path)
	}
}

// T040: list, read, preview, approve, deny with the contract's payloads and codes.
func TestOAuthLoginREST_RequestLifecycle(t *testing.T) {
	h := newOAuthHarness(t, true)
	id, regID, verifier, resource := h.pending("cursor")

	// list
	w := h.call(http.MethodGet, "/api/v1/oauth/authorizations", "", bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	list := decodeBody(t, w)["data"].([]interface{})
	require.Len(t, list, 1)
	row := list[0].(map[string]interface{})
	assert.Equal(t, id, row["id"])
	assert.NotEmpty(t, row["client_label"])
	assert.NotEmpty(t, row["expires_at"])

	// read
	w = h.call(http.MethodGet, "/api/v1/oauth/authorizations/"+id, "", bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := dataOf(t, w)
	assert.Equal(t, "client", d["address_kind"])
	assert.Equal(t, resource, d["resource"])
	assert.Equal(t, oauthTestRedirect, d["redirect_uri"])
	assert.Equal(t, true, d["profile_required"], "profiles exist and cursor has no binding")
	assert.Equal(t, "cursor", d["client"].(map[string]interface{})["id"])

	// uniform 404
	w = h.call(http.MethodGet, "/api/v1/oauth/authorizations/nope", "", bindingAdminKey)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// preview: incomplete without a profile, complete with one; writes nothing
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/preview", `{"client":{"id":"cursor"}}`, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, false, dataOf(t, w)["complete"])
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/preview", `{"client":{"id":"cursor"},"profile":"ro","mode":"locked"}`, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, true, dataOf(t, w)["complete"])
	assert.Len(t, h.svc.ListPending(), 1, "a preview decides nothing")

	// approve refusals
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `{"client":{"id":"cursor"}}`, bindingAdminKey)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "profile_required", decodeBody(t, w)["code"])
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `{"client":{"id":"windsurf"},"profile":"ro"}`, bindingAdminKey)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "client_fixed", decodeBody(t, w)["code"])
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `{"client":{"id":"cursor"},"profile":"nope"}`, bindingAdminKey)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "profile_not_found", decodeBody(t, w)["code"])
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `not json`, bindingAdminKey)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// approve
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `{"client":{"id":"cursor"},"profile":"ro","mode":"locked"}`, bindingAdminKey)
	code := codeOf(t, w)
	u, _ := url.Parse(dataOf(t, w)["redirect_url"].(string))
	assert.Equal(t, "st", u.Query().Get("state"))
	assert.True(t, strings.HasPrefix(u.String(), oauthTestRedirect))

	// decided once
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `{"client":{"id":"cursor"},"profile":"ro"}`, bindingAdminKey)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "already_decided", decodeBody(t, w)["code"])
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/deny", ``, bindingAdminKey)
	assert.Equal(t, http.StatusConflict, w.Code)

	// the approval stores nothing until the exchange: the login does not exist yet
	grants, err := h.svc.ListGrants("cursor")
	require.NoError(t, err)
	assert.Empty(t, grants)
	_, terr := h.svc.Exchange(internalRuntime.TokenInput{Origin: oauthTestOrigin, Code: code, RedirectURI: oauthTestRedirect, ClientID: regID, CodeVerifier: verifier})
	require.Nil(t, terr)

	// deny a second request
	id2, _, _, _ := h.pending("cursor")
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id2+"/deny", ``, bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	u, _ = url.Parse(dataOf(t, w)["redirect_url"].(string))
	assert.Equal(t, "access_denied", u.Query().Get("error"))
	assert.Equal(t, "st", u.Query().Get("state"))
	assert.Empty(t, h.svc.ListPending())
}

// T040: a named pin with require_mcp_auth off is refused with the guard's body.
func TestOAuthLoginREST_ApproveGuardRefusal(t *testing.T) {
	h := newOAuthHarness(t, true)
	h.cfg.RequireMCPAuth = false
	id, _, _, _ := h.pending("cursor")
	w := h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `{"client":{"id":"cursor"},"profile":"ro","mode":"locked"}`, bindingAdminKey)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, "binding_bypassable_without_auth", body["code"])
	assert.NotEmpty(t, body["fixes"])
	// Still pending: a refused approval decides nothing.
	assert.Len(t, h.svc.ListPending(), 1)
}

// T040: the generic address lets the administrator choose (existing or new).
func TestOAuthLoginREST_GenericAddress(t *testing.T) {
	h := newOAuthHarness(t, true)
	id, _, _, _ := h.pending("")
	w := h.call(http.MethodGet, "/api/v1/oauth/authorizations/"+id, "", bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := dataOf(t, w)
	assert.Equal(t, "generic", d["address_kind"])
	assert.NotContains(t, d, "client", "nothing is pre-selected")
	assert.NotNil(t, d["suggested_new_client"])

	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `{}`, bindingAdminKey)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "client_required", decodeBody(t, w)["code"])
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `{"client":{"new":{"id":"cursor"}},"profile":"ro"}`, bindingAdminKey)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "reserved_identity", decodeBody(t, w)["code"], "a supported client's id cannot be taken by a new client")
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `{"client":{"new":{"id":"acme-agent","display_name":"Acme Agent"}},"profile":"ro","mode":"locked"}`, bindingAdminKey)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// T042: client payloads and the per-credential revokes.
func TestOAuthLoginREST_ClientCredentialsPayloadAndRevokes(t *testing.T) {
	h := newOAuthHarness(t, true)
	h.mint("cursor", "ro")

	// Never used: the payload is exactly today's, no new fields.
	w := h.call(http.MethodGet, "/api/v1/clients?client=cursor", "", bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	raw := w.Body.String()
	assert.NotContains(t, raw, "browser_login_disabled")
	assert.NotContains(t, raw, `"credentials"`)

	// A login.
	id, regID, verifier, _ := h.pending("cursor")
	// cursor already has an API-key binding: the approval carries its revision.
	w = h.call(http.MethodGet, "/api/v1/oauth/authorizations/"+id, "", bindingAdminKey)
	rev := dataOf(t, w)["client"].(map[string]interface{})["binding"].(map[string]interface{})["revision"].(string)
	w = h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve", `{"client":{"id":"cursor"},"binding_revision":"`+rev+`"}`, bindingAdminKey)
	code := codeOf(t, w)
	_, terr := h.svc.Exchange(internalRuntime.TokenInput{Origin: oauthTestOrigin, Code: code, RedirectURI: oauthTestRedirect, ClientID: regID, CodeVerifier: verifier})
	require.Nil(t, terr)

	findRow := func(w *httptest.ResponseRecorder) map[string]interface{} {
		for _, c := range dataOf(t, w)["clients"].([]interface{}) {
			if m := c.(map[string]interface{}); m["id"] == "cursor" {
				return m
			}
		}
		t.Fatal("cursor row missing")
		return nil
	}
	w = h.call(http.MethodGet, "/api/v1/clients", "", bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	row := findRow(w)
	creds := row["credentials"].([]interface{})
	require.Len(t, creds, 2)
	kinds := map[string]map[string]interface{}{}
	for _, c := range creds {
		m := c.(map[string]interface{})
		kinds[m["kind"].(string)] = m
	}
	require.Contains(t, kinds, "api_key")
	login := kinds["browser_login"]
	require.NotNil(t, login)
	assert.Equal(t, "active", login["state"])
	assert.Equal(t, "127.0.0.1", login["redirect_host"])
	assert.Equal(t, "cursor ide", login["reported_name"])
	assert.Contains(t, login, "name_mismatch")
	assert.Equal(t, false, row["browser_login_disabled"])
	// Once the feature was used EVERY row carries both fields, an empty list as [].
	for _, c := range dataOf(t, w)["clients"].([]interface{}) {
		m := c.(map[string]interface{})
		_, has := m["credentials"]
		assert.True(t, has, "row %v lacks credentials", m["id"])
		assert.Contains(t, m, "browser_login_disabled", "row %v", m["id"])
	}
	assert.Equal(t, []interface{}{}, findRowByID(t, w, "windsurf")["credentials"])
	assert.Len(t, login["prefix"], 12, "only the display prefix of a token appears in a client payload")
	grantID := login["grant_id"].(string)

	// Off after use: logins are still listed, disabled is true, and a login can still be revoked.
	h.cfg.MCPOAuthEnabled = false
	w = h.call(http.MethodGet, "/api/v1/clients", "", bindingAdminKey)
	row = findRow(w)
	assert.Equal(t, true, row["browser_login_disabled"])
	assert.Len(t, row["credentials"].([]interface{}), 2)
	assert.Equal(t, true, findRowByID(t, w, "windsurf")["browser_login_disabled"], "the flag is install-wide")

	w = h.call(http.MethodDelete, "/api/v1/clients/cursor/logins/"+grantID, "", bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, true, dataOf(t, w)["changed"])
	w = h.call(http.MethodDelete, "/api/v1/clients/cursor/logins/"+grantID, "", bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, false, dataOf(t, w)["changed"], "already revoked")
	w = h.call(http.MethodDelete, "/api/v1/clients/cursor/logins/unknown", "", bindingAdminKey)
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = h.call(http.MethodDelete, "/api/v1/clients/windsurf/logins/"+grantID, "", bindingAdminKey)
	assert.Equal(t, http.StatusNotFound, w.Code, "a login is addressed through its own client")

	// The API key alone: the binding and the (revoked) login records stay.
	w = h.call(http.MethodDelete, "/api/v1/clients/cursor/api-key", "", bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = h.call(http.MethodDelete, "/api/v1/clients/cursor/api-key", "", bindingAdminKey)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "no_client_credential", decodeBody(t, w)["code"])
	w = h.call(http.MethodDelete, "/api/v1/clients/Bad!Id/api-key", "", bindingAdminKey)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// T042: a client that has only browser logins gets a row.
func TestOAuthLoginREST_OAuthOnlyClientRow(t *testing.T) {
	h := newOAuthHarness(t, true)
	id, regID, verifier, _ := h.pending("")
	w := h.call(http.MethodPost, "/api/v1/oauth/authorizations/"+id+"/approve",
		`{"client":{"new":{"id":"acme-agent","display_name":"Acme Agent"}},"profile":"ro","mode":"locked"}`, bindingAdminKey)
	code := codeOf(t, w)
	_, terr := h.svc.Exchange(internalRuntime.TokenInput{Origin: oauthTestOrigin, Code: code, RedirectURI: oauthTestRedirect, ClientID: regID, CodeVerifier: verifier})
	require.Nil(t, terr)

	w = h.call(http.MethodGet, "/api/v1/clients", "", bindingAdminKey)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var found map[string]interface{}
	for _, c := range dataOf(t, w)["clients"].([]interface{}) {
		if m := c.(map[string]interface{}); m["id"] == "acme-agent" {
			found = m
		}
	}
	require.NotNil(t, found, "an OAuth-only client appears in the Clients list")
	assert.Equal(t, "Acme Agent", found["display_name"])
	assert.Equal(t, "ro", found["profile"])
	assert.Equal(t, "locked", found["profile_mode"])
	assert.Len(t, found["credentials"].([]interface{}), 1)
}

func findRowByID(t *testing.T, w *httptest.ResponseRecorder, id string) map[string]interface{} {
	t.Helper()
	for _, c := range dataOf(t, w)["clients"].([]interface{}) {
		if m := c.(map[string]interface{}); m["id"] == id {
			return m
		}
	}
	t.Fatalf("%s row missing", id)
	return nil
}

// An mcp_oat_ access token is valid on its MCP address only: on the REST API it
// gets the client-credential 403 once the feature is on or was used, and the
// previous answer (unknown token, 401) while it is off and never used.
func TestOAuthLoginREST_AccessTokenRefusedOnREST(t *testing.T) {
	const oat = "mcp_oat_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	status := func(h *oauthHarness, hdr, val string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", http.NoBody)
		req.Header.Set(hdr, val)
		w := httptest.NewRecorder()
		h.srv.ServeHTTP(w, req)
		return w.Code
	}
	on := newOAuthHarness(t, true)
	assert.Equal(t, http.StatusForbidden, status(on, "Authorization", "Bearer "+oat))
	assert.Equal(t, http.StatusForbidden, status(on, "Authorization", "Bearer  "+oat), "extra whitespace is the same token")
	assert.Equal(t, http.StatusForbidden, status(on, "X-API-Key", oat))
	off := newOAuthHarness(t, false)
	assert.Equal(t, http.StatusUnauthorized, status(off, "Authorization", "Bearer "+oat), "off and never used: unchanged")
	assert.Equal(t, http.StatusUnauthorized, status(off, "X-API-Key", oat))
}
