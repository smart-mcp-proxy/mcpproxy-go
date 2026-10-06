package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// T138 / FR-024, FR-031: a real mcp-go OAuthHandler built from the config
// createOAuthConfigInternal returns honors the endpoint overrides end to end,
// even though the advertised metadata (served on a non-standard path) points
// at different endpoints.
func TestOAuthEndpointOverrides_EndToEndWithMCPGoHandler(t *testing.T) {
	resetDiscoveryStateForTest(t)

	var mu sync.Mutex
	var seen []string
	var tokenForms []url.Values
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/real/token" {
			form, _ := url.ParseQuery(string(body))
			tokenForms = append(tokenForms, form)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/real/register":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"client_id":"dyn-client","client_secret":"dyn-secret"}`))
		case "/real/token":
			w.WriteHeader(http.StatusCreated) // some providers answer 201; the wrapper normalizes it
			_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","refresh_token":"rt2","expires_in":3600}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer idp.Close()

	// Metadata server: a non-standard path, advertising endpoints that must NOT be used.
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, "META "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/custom/discovery.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"http://` + r.Host + `","authorization_endpoint":"http://` + r.Host + `/std/authorize","token_endpoint":"http://` + r.Host + `/std/token","registration_endpoint":"http://` + r.Host + `/std/register","response_types_supported":["code"],"code_challenge_methods_supported":["S256"]}`))
	}))
	defer meta.Close()

	name := "e2e-override-srv"
	stopCallbackServer(t, name)
	sc := &config.ServerConfig{
		Name: name, URL: meta.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			AuthServerMetadataURL: meta.URL + "/custom/discovery.json",
			AuthorizationEndpoint: idp.URL + "/real/authorize",
			TokenEndpoint:         idp.URL + "/real/token",
			RegistrationEndpoint:  idp.URL + "/real/register",
		},
	}
	cfg, err := createOAuthConfigInternal(sc, nil, map[string]string{"resource": "https://mcp.example.com/mcp"}, zap.NewNop())
	require.NoError(t, err)

	h := transport.NewOAuthHandler(*cfg)
	ctx := context.Background()

	require.NoError(t, h.RegisterClient(ctx, "mcpproxy-go"))
	assert.Equal(t, "dyn-client", h.GetClientID())

	h.SetExpectedState("st")
	authURL, err := h.GetAuthorizationURL(ctx, "st", "challenge")
	require.NoError(t, err)
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	idpURL, _ := url.Parse(idp.URL)
	assert.Equal(t, idpURL.Host, u.Host, "FR-031: authorization URL host equals the authorization_endpoint override")
	assert.Equal(t, "/real/authorize", u.Path)

	require.NoError(t, h.ProcessAuthorizationResponse(ctx, "code123", "st", "verifier"))
	tok, err := cfg.TokenStore.GetToken(ctx)
	require.NoError(t, err)
	assert.Equal(t, "at", tok.AccessToken)

	_, err = h.RefreshToken(ctx, "rt1")
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		assert.NotContains(t, s, "/std/", "mcp-go must never hit the advertised/default endpoints: %s", s)
	}
	assert.Contains(t, seen, "POST /real/register")
	require.Len(t, tokenForms, 2, "exchange and refresh both hit the token override")
	assert.Equal(t, "authorization_code", tokenForms[0].Get("grant_type"))
	assert.Equal(t, "refresh_token", tokenForms[1].Get("grant_type"))
	for _, f := range tokenForms {
		assert.Equal(t, "https://mcp.example.com/mcp", f.Get("resource"), "extra_params injected on the override token endpoint")
	}
}

// FR-024: overrides cover everything and the standard metadata is unreachable:
// mcp-go still never touches its /authorize, /token, /register defaults.
func TestOAuthEndpointOverrides_UnreachableMetadataNeverHitsDefaults(t *testing.T) {
	resetDiscoveryStateForTest(t)
	var mu sync.Mutex
	var seen []string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/o2/register":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"client_id":"c1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer idp.Close()

	name := "e2e-synth-srv"
	stopCallbackServer(t, name)
	sc := &config.ServerConfig{
		Name: name, URL: idp.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			AuthorizationEndpoint: idp.URL + "/o2/authorize",
			TokenEndpoint:         idp.URL + "/o2/token",
			RegistrationEndpoint:  idp.URL + "/o2/register",
		},
	}
	cfg, err := createOAuthConfigInternal(sc, nil, nil, zap.NewNop())
	require.NoError(t, err)
	h := transport.NewOAuthHandler(*cfg)
	require.NoError(t, h.RegisterClient(context.Background(), "mcpproxy-go"))
	h.SetExpectedState("s")
	authURL, err := h.GetAuthorizationURL(context.Background(), "s", "c")
	require.NoError(t, err)
	assert.Contains(t, authURL, "/o2/authorize")

	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		assert.NotRegexp(t, `^(POST|GET) /(authorize|token|register)$`, s)
	}
	assert.Contains(t, seen, "POST /o2/register")
}
