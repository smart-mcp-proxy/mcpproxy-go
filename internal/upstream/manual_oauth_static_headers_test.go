package upstream

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// completeTestSignIn finishes a sign-in the way a browser would: it submits
// the test authorization server's login form and follows the redirect into
// mcpproxy's callback listener.
func completeTestSignIn(t *testing.T, srv *oauthserver.ServerResult, rawAuthURL string) {
	t.Helper()
	authURL, err := url.Parse(rawAuthURL)
	require.NoError(t, err)
	form := url.Values{}
	for k, v := range authURL.Query() {
		form.Set(k, v[0])
	}
	form.Set("username", "testuser")
	form.Set("password", "testpass")
	form.Set("consent", "on")
	form.Set("action", "approve")
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	authResp, err := noRedirect.PostForm(srv.AuthorizationEndpoint, form)
	require.NoError(t, err)
	_ = authResp.Body.Close()
	require.Equal(t, http.StatusFound, authResp.StatusCode)
	callback := authResp.Header.Get("Location")
	require.Contains(t, callback, "/oauth/callback?code=")

	cbReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, callback, nil)
	require.NoError(t, err)
	cbResp, err := http.DefaultClient.Do(cbReq)
	require.NoError(t, err)
	_ = cbResp.Body.Close()
	require.Equal(t, http.StatusOK, cbResp.StatusCode, "the callback must find the login waiting")
}

// GH #1922 end to end, through the manager entry point behind
// POST /api/v1/servers/{id}/login. The server carries a static Authorization
// header the upstream rejects and no oauth block; the connection path parks it
// awaiting sign-in. The sign-in must start, complete, and leave a token the
// next connection is established with.
func TestStartManualOAuthQuick_StaticHeaderServerCanSignIn(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "")
	t.Setenv("HEADLESS", "1")
	t.Setenv("CI", "")

	srv := oauthserver.Start(t, oauthserver.Options{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const name = "static-header-sign-in"
	t.Cleanup(func() {
		if mgr := oauth.GetGlobalCallbackManager(); mgr != nil {
			_ = mgr.StopCallbackServer(name)
		}
	})

	cfg := &config.ServerConfig{
		Name: name, URL: srv.MCPURL, Protocol: "http", Enabled: true,
		Headers: map[string]string{"Authorization": "Bearer rejected-static-credential"},
	}
	m := NewManager(zap.NewNop(), &config.Config{}, db, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })
	require.NoError(t, m.AddServerConfig(name, cfg))

	result, err := m.StartManualOAuthQuick(name)
	require.NoError(t, err, "the sign-in offered for a parked static-header server must start")
	require.NotNil(t, result)
	require.NotEmpty(t, result.AuthURL)

	completeTestSignIn(t, srv, result.AuthURL)
	require.Eventually(t, func() bool {
		rec, err := db.GetOAuthToken(oauth.GenerateServerKey(name, srv.MCPURL))
		return err == nil && rec != nil && rec.AccessToken != ""
	}, 5*time.Second, 100*time.Millisecond, "token from the completed sign-in must be stored")

	// The reconnect the sign-in triggers, on the manager's own client: the
	// header is still rejected, so the ladder must reach OAuth and connect with
	// the new token — the rejected static Authorization header must not
	// displace it. (A second client connecting here would race that reconnect
	// for the server's OAuth flow and be told to retry.)
	client, ok := m.GetClient(name)
	require.True(t, ok)
	t.Cleanup(func() { _ = m.DisconnectAll() }) // before the upstream shuts down (LIFO)
	require.Eventually(t, client.IsConnected, 30*time.Second, 100*time.Millisecond,
		"the server must reconnect once the sign-in completes")
	assert.True(t, client.ConnectedWithOAuth(), "the connection must be the OAuth strategy's")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	callResult, err := client.CallTool(ctx, "get_time", nil)
	require.NoError(t, err, "tool calls must carry the signed-in token")
	require.NotNil(t, callResult)
	assert.False(t, callResult.IsError)
}
