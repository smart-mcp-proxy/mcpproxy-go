//go:build server

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #1148, applied to the server edition's per-user door.
//
// `GET /api/v1/user/servers`, `GET /api/v1/user/servers/{name}` and
// `POST /api/v1/user/servers/{name}/enable` all render a ServerResponse, which
// EMBEDS the raw *config.ServerConfig. For a SHARED server that config is the
// admin's, not the caller's — so every authenticated user of the deployment
// was handed the shared upstream's Authorization header, its env, its URL
// query credentials, `oauth.client_secret` and `auth_broker.client_secret` in
// the clear. Same defect class as the MCP/REST/SSE doors #1148 closed, on a
// door that sweep did not reach.

const (
	sharedURLToken     = "urlsecret_ZjQ2NTQ0NGY2NTc0NzQ2NQ"
	sharedHeaderToken  = "hdrsecret_MzM0NDU1NjY3Nzg4OTlhYQ"
	sharedAPIKeyValue  = "apikeysecret_YWJjZGVmMTIzNDU2Nzg5"
	sharedEnvToken     = "ghp_envsecret1234567890abcdefghijkl"
	sharedArgvToken    = "argvsecret_OTg3NjU0MzIxMGZlZGNiYQ"
	sharedOAuthSecret  = "oauthsecret_MTIzNDU2Nzg5MGFiY2RlZg"
	sharedBrokerSecret = "brokersecret_ZmVkY2JhMDk4NzY1NDMy"
	sharedIsolationArg = "isosecret_Nzg5MGFiY2RlZjEyMzQ1Ng"
)

// sharedSecrets lists every credential the fixture below plants, so a new field
// cannot be added to the fixture without the assertions covering it.
func sharedSecrets() []string {
	return []string{
		sharedURLToken,
		sharedHeaderToken,
		sharedAPIKeyValue,
		sharedEnvToken,
		sharedArgvToken,
		sharedOAuthSecret,
		sharedBrokerSecret,
		sharedIsolationArg,
	}
}

// secretBearingSharedServer is one admin-configured SHARED server carrying a
// credential in every secret-bearing shape the config struct has.
func secretBearingSharedServer() *config.ServerConfig {
	return &config.ServerConfig{
		Name:     "shared-secrets",
		URL:      "https://api.example.com/mcp?access_token=" + sharedURLToken,
		Protocol: "http",
		Command:  "npx",
		Args:     []string{"server", "--api-key", sharedArgvToken},
		Env:      map[string]string{"GITHUB_TOKEN": sharedEnvToken},
		Headers: map[string]string{
			"Authorization": "Bearer " + sharedHeaderToken,
			"X-Api-Key":     sharedAPIKeyValue,
		},
		OAuth: &config.OAuthConfig{
			ClientID:     "public-client-id",
			ClientSecret: sharedOAuthSecret,
		},
		AuthBroker: &config.AuthBrokerConfig{
			Mode:          config.AuthBrokerModeTokenExchange,
			TokenEndpoint: "https://idp.example.com/token",
			ClientID:      "broker-client-id",
			ClientSecret:  sharedBrokerSecret,
		},
		Isolation: &config.IsolationConfig{
			ExtraArgs: []string{"-e", "API_KEY=" + sharedIsolationArg},
		},
		Enabled: true,
		Shared:  true,
		// A FIXED timestamp: the pristine-copy assertions below compare two
		// independently built fixtures.
		Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// assertNoSharedSecrets fails naming the exact credential that escaped.
func assertNoSharedSecrets(t *testing.T, body []byte) {
	t.Helper()
	for _, secret := range sharedSecrets() {
		assert.NotContains(t, string(body), secret,
			"a shared server's credential reached an ordinary user in the clear")
	}
}

func TestListUserServers_MasksSharedServerSecrets(t *testing.T) {
	shared := []*config.ServerConfig{secretBearingSharedServer()}
	pristine := secretBearingSharedServer()
	handlers, _ := testSetup(t, shared)
	router := testRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assertNoSharedSecrets(t, w.Body.Bytes())

	// The response must still be USEFUL: the server is named and identifiable.
	var resp ServerListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Shared, 1)
	assert.Equal(t, "shared-secrets", resp.Shared[0].Name)
	assert.Equal(t, "shared", resp.Shared[0].Ownership)

	// h.sharedServers is the LIVE admin config; rendering a read response must
	// never mutate it (the #1142/#1146 corruption shape).
	assert.True(t, reflect.DeepEqual(pristine, shared[0]),
		"masking a read payload mutated the live shared config")
}

func TestGetServer_MasksSharedServerSecrets(t *testing.T) {
	shared := []*config.ServerConfig{secretBearingSharedServer()}
	pristine := secretBearingSharedServer()
	handlers, _ := testSetup(t, shared)
	router := testRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers/shared-secrets", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assertNoSharedSecrets(t, w.Body.Bytes())

	var resp ServerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "shared", resp.Ownership)
	assert.Equal(t, "shared-secrets", resp.Name)

	assert.True(t, reflect.DeepEqual(pristine, shared[0]),
		"masking a read payload mutated the live shared config")
}

func TestEnableServer_MasksSharedServerSecrets(t *testing.T) {
	shared := []*config.ServerConfig{secretBearingSharedServer()}
	pristine := secretBearingSharedServer()
	handlers, _ := testSetup(t, shared)
	router := testRouter(handlers, defaultAuthContext())

	body, err := json.Marshal(EnableServerRequest{Enabled: false})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/servers/shared-secrets/enable", bytes.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assertNoSharedSecrets(t, w.Body.Bytes())

	var resp ServerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.UserEnabled)
	assert.False(t, *resp.UserEnabled, "the per-user preference must survive masking")

	assert.True(t, reflect.DeepEqual(pristine, shared[0]),
		"masking a read payload mutated the live shared config")
}

// The PERSONAL half is deliberately NOT masked, and that decision is pinned
// here rather than left implicit.
//
// A personal server's credentials are the caller's OWN — there is no
// cross-tenant disclosure to close. Masking them would be actively harmful
// without a key-bound unmask mirror on updateServer, which replaces Headers,
// Args and URL WHOLESALE from the request body: a Web UI that read the masked
// values back and PUT them would persist the masks over the user's real
// credentials. That is exactly the read-modify-write corruption of #1142/#1146.
// If personal servers are ever masked, updateServer needs
// oauth.UnmaskLiveHeaders / UnmaskLiveURL plus oauth.CheckArgvMaskEcho first.
func TestGetServer_PersonalSecretsAreNotMasked(t *testing.T) {
	handlers, store := testSetup(t, nil)
	router := testRouter(handlers, defaultAuthContext())

	personal := secretBearingSharedServer()
	personal.Name = "my-server"
	personal.Shared = false
	require.NoError(t, store.CreateUserServer(testUserID, personal))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers/my-server", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp ServerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "personal", resp.Ownership)
	assert.Equal(t, "Bearer "+sharedHeaderToken, resp.Headers["Authorization"],
		"a user's own credential must round-trip so a read-modify-write update cannot corrupt it")
}

// The fail-closed net: EVERY route this handler registers is exercised against
// a secret-bearing shared server, and none of them may echo a credential.
//
// It is driven by the router's OWN route table rather than by a list of the
// three handlers that leaked, because a hand-listed set of doors is exactly
// what let this defect survive the #1148 sweep — listServers, getServer and
// enableServer each built the same raw ServerResponse, and a fourth route that
// does it tomorrow would be just as invisible. A new route under
// /user/servers is covered by this test the moment it is registered.
func TestUserServerRoutes_NeverEchoASharedServerSecret(t *testing.T) {
	shared := []*config.ServerConfig{secretBearingSharedServer()}
	handlers, _ := testSetup(t, shared)
	router := testRouter(handlers, defaultAuthContext())

	// A body that decodes cleanly as every request type this handler accepts,
	// so a route is exercised for real rather than bouncing off a 400.
	body := []byte(`{"enabled":false}`)

	walked := 0
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.Contains(route, "/user/servers") {
			return nil
		}
		walked++
		target := strings.ReplaceAll(route, "{name}", "shared-secrets")
		// chi renders a trailing-slash subroute as "/*"; the bare path is the
		// one clients call.
		target = strings.TrimSuffix(target, "/*")

		req := httptest.NewRequest(method, target, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		for _, secret := range sharedSecrets() {
			assert.NotContains(t, w.Body.String(), secret,
				"%s %s echoed a shared server's credential (status %d)", method, route, w.Code)
		}
		return nil
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, walked, 6, "the walk must reach every /user/servers route")
}
