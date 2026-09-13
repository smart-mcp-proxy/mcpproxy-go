package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// GH #1271 end-to-end: the repo's OAuth test server in per-method-auth mode
// mirrors Google's Gmail MCP endpoint (anonymous initialize/tools/list, 401 on
// tools/call). With an oauth block and a valid token already in the store,
// the connection must be established by the OAuth strategy so the token rides
// on every tools/call — before the fix no-auth won the ladder and every call
// died with "authorization required" while status said "authenticated".

// mintPerMethodAuthToken obtains a bearer the test server's /mcp accepts and
// persists it the way a completed `mcpproxy auth login` would.
func mintPerMethodAuthToken(t *testing.T, srv *oauthserver.ServerResult, db *storage.BoltDB, serverName string) {
	t.Helper()
	mintPerMethodAuthTokenFor(t, srv, db, serverName, srv.MCPURL)
}

func mintPerMethodAuthTokenFor(t *testing.T, srv *oauthserver.ServerResult, db *storage.BoltDB, serverName, serverURL string) {
	t.Helper()
	params := url.Values{}
	params.Set("grant_type", "client_credentials")
	params.Set("client_id", srv.ClientID)
	params.Set("client_secret", srv.ClientSecret)
	params.Set("scope", "read")
	resp, err := http.PostForm(srv.TokenEndpoint, params)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var tok oauthserver.TokenResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tok))
	require.NotEmpty(t, tok.AccessToken)

	require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName:  oauth.GenerateServerKey(serverName, serverURL),
		DisplayName: serverName,
		AccessToken: tok.AccessToken,
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(time.Hour),
		Scopes:      []string{"read"},
		Created:     time.Now(),
	}))
}

func newPerMethodAuthClient(t *testing.T, srv *oauthserver.ServerResult, db *storage.BoltDB, name string, oauthBlock *config.OAuthConfig) *Client {
	t.Helper()
	cfg := &config.ServerConfig{
		Name:     name,
		URL:      srv.MCPURL,
		Protocol: "http",
		Enabled:  true,
		OAuth:    oauthBlock,
	}
	c, err := NewClient(name, cfg, zap.NewNop(), nil, &config.Config{}, db, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Disconnect() })
	return c
}

func TestPerMethodAuthUpstream_OAuthBlockRoutesToOAuthStrategy(t *testing.T) {
	srv := oauthserver.Start(t, oauthserver.Options{MCPPerMethodAuth: true})
	t.Cleanup(func() { _ = srv.Shutdown() }) // after the client's Disconnect (LIFO), or the open SSE stream stalls Shutdown 5s

	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const name = "per-method-auth"
	mintPerMethodAuthToken(t, srv, db, name)

	c := newPerMethodAuthClient(t, srv, db, name, &config.OAuthConfig{
		ClientID:     srv.ClientID,
		ClientSecret: srv.ClientSecret,
		Scopes:       []string{"read"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	assert.Equal(t, AuthStrategyOAuth, c.AuthStrategy(), "the oauth block must route the connection to the OAuth strategy")

	tools, err := c.ListTools(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, tools)

	result, err := c.CallTool(ctx, "get_time", nil)
	require.NoError(t, err, "tools/call must carry the stored bearer")
	require.NotNil(t, result)
	assert.False(t, result.IsError)
}

// Boundary: without an oauth block there is no signal, the anonymous probe
// still wins and tools/call still 401s. This pins the contract the fix relies
// on — the oauth block is what an operator adds for such an upstream.
func TestPerMethodAuthUpstream_NoOAuthBlockStaysAnonymous(t *testing.T) {
	srv := oauthserver.Start(t, oauthserver.Options{MCPPerMethodAuth: true})
	t.Cleanup(func() { _ = srv.Shutdown() }) // after the client's Disconnect (LIFO), or the open SSE stream stalls Shutdown 5s

	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const name = "per-method-anon"
	mintPerMethodAuthToken(t, srv, db, name)
	c := newPerMethodAuthClient(t, srv, db, name, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	assert.Equal(t, "no-auth", c.AuthStrategy())

	_, err = c.CallTool(ctx, "get_time", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authorization required")
}

// SSE variant. mcp-go's SSE Start() consults the token store before opening
// the stream, so the declared-OAuth SSE ladder reaches trySSEOAuthAuth's
// Start()-error branch whenever no token is stored — a branch that used to
// dead-end in "OAuth authorization already in progress" instead of parking in
// PendingAuth like the streamable-HTTP path.
func newPerMethodAuthSSEClient(t *testing.T, srv *oauthserver.ServerResult, db *storage.BoltDB, name string) *Client {
	t.Helper()
	cfg := &config.ServerConfig{
		Name:     name,
		URL:      srv.SSEURL,
		Protocol: "sse",
		Enabled:  true,
		OAuth:    &config.OAuthConfig{ClientID: srv.ClientID, ClientSecret: srv.ClientSecret, Scopes: []string{"read"}},
	}
	c, err := NewClient(name, cfg, zap.NewNop(), nil, &config.Config{}, db, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Disconnect() })
	return c
}

func TestPerMethodAuthUpstream_SSE_OAuthBlockRoutesToOAuthStrategy(t *testing.T) {
	srv := oauthserver.Start(t, oauthserver.Options{MCPPerMethodAuth: true})
	t.Cleanup(func() { _ = srv.Shutdown() }) // after the client's Disconnect (LIFO), or the open SSE stream stalls Shutdown 5s
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const name = "per-method-auth-sse"
	// Token is keyed by the SSE URL this client connects with.
	mintPerMethodAuthTokenFor(t, srv, db, name, srv.SSEURL)
	c := newPerMethodAuthSSEClient(t, srv, db, name)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	assert.Equal(t, AuthStrategyOAuth, c.AuthStrategy())

	result, err := c.CallTool(ctx, "get_time", nil)
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestPerMethodAuthUpstream_SSE_NoTokenParksInPendingAuth(t *testing.T) {
	srv := oauthserver.Start(t, oauthserver.Options{MCPPerMethodAuth: true})
	t.Cleanup(func() { _ = srv.Shutdown() }) // after the client's Disconnect (LIFO), or the open SSE stream stalls Shutdown 5s
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	c := newPerMethodAuthSSEClient(t, srv, db, "per-method-auth-sse-notoken")

	// A plain (non-manual) context is what the daemon's automatic connect uses.
	// Short deadline on purpose: parking is immediate; a regressed guard would
	// sit in the callback wait until this expires instead of 30s.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	err = c.Connect(ctx)
	require.Error(t, err)
	assert.True(t, IsOAuthPending(err), "automatic connect must park in PendingAuth, got: %v", err)
	assert.NotContains(t, err.Error(), "already in progress")
	assert.Less(t, time.Since(start), 3*time.Second, "parking must not wait on a browser flow")
	assert.Contains(t, err.Error(), "oauth block declares OAuth", "the pending detail must name the cause")
}

// Two clients of the same server connecting at once (the daemon's managed
// client and a manual-login/preflight transient) share one coordinator flow.
// The waiter used to return nil from the OAuth strategy and be marked
// connected with no transport (a ghost connection). Invariant: a client that
// reports Connect success must be able to call a tool.
func TestPerMethodAuthUpstream_ConcurrentSameNameClients_NoGhostConnection(t *testing.T) {
	srv := oauthserver.Start(t, oauthserver.Options{MCPPerMethodAuth: true})
	t.Cleanup(func() { _ = srv.Shutdown() })
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const name = "per-method-auth-concurrent"
	mintPerMethodAuthToken(t, srv, db, name)
	block := &config.OAuthConfig{ClientID: srv.ClientID, ClientSecret: srv.ClientSecret, Scopes: []string{"read"}}

	const n = 6
	clients := make([]*Client, n)
	for i := range clients {
		clients[i] = newPerMethodAuthClient(t, srv, db, name, block)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range clients {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = clients[i].Connect(ctx)
		}(i)
	}
	wg.Wait()

	connected := 0
	for i, c := range clients {
		if errs[i] != nil {
			assert.False(t, c.IsConnected(), "client %d returned an error but reports connected", i)
			continue
		}
		connected++
		_, callErr := c.CallTool(ctx, "get_time", nil)
		assert.NoError(t, callErr, "client %d reported Connect success but cannot call a tool (ghost connection)", i)
	}
	assert.GreaterOrEqual(t, connected, 1, "at least the flow owner must connect")
}

// Manual-login half of GH #1271. With a VALID token stored, the OAuth transport
// attaches it and initialize succeeds, so the login paths used to conclude
// "server connected without OAuth - no authentication required" — the daemon's
// `auth login` 500 from the issue. (Without a token this passes trivially on
// any build: mcp-go fails initialize pre-send and the flow starts as usual.)
func TestPerMethodAuthUpstream_ManualLoginProceedsWithValidToken(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "")
	t.Setenv("HEADLESS", "1") // never open a browser from a test

	srv := oauthserver.Start(t, oauthserver.Options{MCPPerMethodAuth: true})
	t.Cleanup(func() { _ = srv.Shutdown() }) // after the client's Disconnect (LIFO), or the open SSE stream stalls Shutdown 5s
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const name = "per-method-auth-relogin"
	stopCallbackServerAfterTest(t, name)
	mintPerMethodAuthToken(t, srv, db, name)
	c := newPerMethodAuthClient(t, srv, db, name, &config.OAuthConfig{
		ClientID:     srv.ClientID,
		ClientSecret: srv.ClientSecret,
		Scopes:       []string{"read"},
	})

	// Cancelling the context ends the background callback waiter the quick
	// flow leaves behind.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	result, err := c.StartOAuthFlowQuick(ctx)
	require.NoError(t, err, "a declared-OAuth server must accept a manual login even when initialize succeeds")
	require.NotNil(t, result)
	assert.NotEmpty(t, result.AuthURL, "the login must hand back an authorize URL")
	assert.Contains(t, result.AuthURL, srv.AuthorizationEndpoint)
}
