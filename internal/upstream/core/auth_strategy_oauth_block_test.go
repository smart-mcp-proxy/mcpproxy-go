package core

import (
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// GH #1271: an upstream that answers initialize/tools/list anonymously but
// requires a token for tools/call can never be authenticated when no-auth sits
// before OAuth in the ladder — no-auth "succeeds" and the ladder stops. A
// configured oauth block is the documented signal that OAuth is required
// (config.ServerConfig.OAuth: "keep even when empty to signal OAuth
// requirement"), so it must route the connection to the OAuth strategy and
// never let an anonymous probe win.
func TestAuthStrategies_OAuthBlockDropsNoAuth(t *testing.T) {
	cases := []struct {
		name    string
		oauth   *config.OAuthConfig
		headers map[string]string
		want    []string
	}{
		{"no oauth block keeps the historical chain", nil, nil, []string{"headers", "no-auth", "OAuth"}},
		{"no oauth block, headers: historical chain", nil, map[string]string{"X-Tenant": "t"}, []string{"headers", "no-auth", "OAuth"}},
		{"empty oauth block signals OAuth", &config.OAuthConfig{}, nil, []string{"OAuth"}},
		{"populated oauth block signals OAuth", &config.OAuthConfig{ClientID: "id", Scopes: []string{"s"}}, nil, []string{"OAuth"}},
		// The headers strategy makes the same "anonymous initialize succeeded"
		// inference as no-auth, so no static header — not even a (possibly
		// stale) Authorization — may win ahead of OAuth; static headers ride on
		// the OAuth transport instead, where the token store owns Authorization.
		{"oauth block + non-auth header: OAuth only", &config.OAuthConfig{}, map[string]string{"X-Goog-User-Project": "p"}, []string{"OAuth"}},
		{"oauth block + static Authorization: OAuth only", &config.OAuthConfig{}, map[string]string{"Authorization": "Bearer stale"}, []string{"OAuth"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{config: &config.ServerConfig{URL: "https://upstream.example/mcp", OAuth: tc.oauth, Headers: tc.headers}}
			if got := strategyNames(c.httpAuthStrategies()); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("HTTP strategies = %v, want %v", got, tc.want)
			}
			if got := strategyNames(c.sseAuthStrategies()); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SSE strategies = %v, want %v", got, tc.want)
			}
		})
	}
}

// MCPPROXY_DISABLE_OAUTH (test fixtures) must keep the historical chain even
// with an oauth block: the ladder never consulted oauth.ShouldUseOAuth, so the
// env gate has to be honoured here or fixtures would run DCR under -race.
func TestAuthStrategies_DisableOAuthEnvKeepsHistoricalChain(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	c := &Client{config: &config.ServerConfig{URL: "https://upstream.example/mcp", OAuth: &config.OAuthConfig{}}}
	want := []string{"headers", "no-auth", "OAuth"}
	if got := strategyNames(c.httpAuthStrategies()); !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTP strategies = %v, want %v", got, want)
	}
	if got := strategyNames(c.sseAuthStrategies()); !reflect.DeepEqual(got, want) {
		t.Fatalf("SSE strategies = %v, want %v", got, want)
	}
}

// Spec 074 fail-closed must win over the oauth block: a brokered connection
// stays headers-only even when the server config carries an oauth block.
func TestAuthStrategies_BrokeredWinsOverOAuthBlock(t *testing.T) {
	c := &Client{config: &config.ServerConfig{URL: "https://upstream.example/mcp", OAuth: &config.OAuthConfig{}}}
	c.SetBrokeredAuth(&transport.BrokeredAuth{Header: "Authorization", Format: "Bearer {token}", Token: "u"})
	want := []string{"headers"}
	if got := strategyNames(c.httpAuthStrategies()); !reflect.DeepEqual(got, want) {
		t.Fatalf("brokered HTTP strategies = %v, want %v", got, want)
	}
	if got := strategyNames(c.sseAuthStrategies()); !reflect.DeepEqual(got, want) {
		t.Fatalf("brokered SSE strategies = %v, want %v", got, want)
	}
}

// GH #1271, manual-login side: the three login paths (getAuthorizationURLQuick,
// forceHTTPOAuthFlowWithResult, forceSSEOAuthFlowWithResult) keyed "no
// authentication required" on a successful initialize. For a per-method-auth
// upstream that answer is wrong, and it made `mcpproxy auth login` refuse the
// one remedy that would have worked. When the operator declared OAuth, the
// login must carry on with the transport's own handler.
func TestForcedAuthorizationRequired(t *testing.T) {
	newOAuthClient := func(t *testing.T) *client.Client {
		t.Helper()
		c, err := transport.CreateHTTPClient(&transport.HTTPTransportConfig{
			URL:         "https://upstream.example/mcp",
			UseOAuth:    true,
			OAuthConfig: &client.OAuthConfig{ClientID: "id", RedirectURI: "http://127.0.0.1:1/cb"},
		})
		require.NoError(t, err)
		return c
	}

	t.Run("oauth block + OAuth transport yields a handler-bearing error", func(t *testing.T) {
		c := &Client{config: &config.ServerConfig{URL: "https://upstream.example/mcp", OAuth: &config.OAuthConfig{}}, logger: zap.NewNop()}
		c.client = newOAuthClient(t)
		err := c.forcedAuthorizationRequired()
		require.Error(t, err)
		assert.True(t, client.IsOAuthAuthorizationRequiredError(err))
		assert.NotNil(t, client.GetOAuthHandler(err), "the login paths extract the handler from this error")
	})

	t.Run("no oauth block keeps the historical no-auth conclusion", func(t *testing.T) {
		c := &Client{config: &config.ServerConfig{URL: "https://upstream.example/mcp"}, logger: zap.NewNop()}
		c.client = newOAuthClient(t)
		assert.NoError(t, c.forcedAuthorizationRequired())
	})

	t.Run("oauth block but a transport without a handler cannot force a login", func(t *testing.T) {
		c := &Client{config: &config.ServerConfig{URL: "https://upstream.example/mcp", OAuth: &config.OAuthConfig{}}, logger: zap.NewNop()}
		plain, err := transport.CreateHTTPClient(&transport.HTTPTransportConfig{URL: "https://upstream.example/mcp"})
		require.NoError(t, err)
		c.client = plain
		assert.NoError(t, c.forcedAuthorizationRequired())
	})
}
