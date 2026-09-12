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

// GH #1271 follow-on: a declared-OAuth server now lets the operator re-login
// while a recent completion is on record. StartManualOAuthQuick's watcher
// owned the login context and used to return — cancelling it — after ~2 s
// whenever HasRecentOAuthCompletion was already true, so the browser callback
// that arrived a moment later found nobody waiting (HTTP 400). The watcher
// must only end the login when THIS flow's token lands or the deadline passes.
func TestStartManualOAuthQuick_RecentCompletionDoesNotCancelLogin(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "")
	t.Setenv("HEADLESS", "1")
	t.Setenv("CI", "")

	srv := oauthserver.Start(t, oauthserver.Options{MCPPerMethodAuth: true})
	t.Cleanup(func() { _ = srv.Shutdown() })
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const name = "relogin-recent-completion"
	t.Cleanup(func() {
		if mgr := oauth.GetGlobalCallbackManager(); mgr != nil {
			_ = mgr.StopCallbackServer(name)
		}
	})

	m := NewManager(zap.NewNop(), &config.Config{}, db, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })
	require.NoError(t, m.AddServerConfig(name, &config.ServerConfig{
		Name: name, URL: srv.MCPURL, Protocol: "http", Enabled: true,
		OAuth: &config.OAuthConfig{ClientID: srv.ClientID, ClientSecret: srv.ClientSecret, Scopes: []string{"read"}},
	}))

	// A sign-in completed "a moment ago" — the state that used to make the
	// watcher exit (and cancel) immediately.
	oauth.GetTokenStoreManager().MarkOAuthCompleted(name)

	result, err := m.StartManualOAuthQuick(name)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.AuthURL)

	// Let the watcher's first tick (2 s) pass: the old code cancelled here.
	time.Sleep(3 * time.Second)

	// Complete the sign-in the way a browser would: submit the test server's
	// login form and follow its redirect into mcpproxy's callback listener.
	authURL, err := url.Parse(result.AuthURL)
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
	assert.Equal(t, http.StatusOK, cbResp.StatusCode, "the callback must still find the login waiting")

	// And the flow's token must have been persisted.
	require.Eventually(t, func() bool {
		rec, err := db.GetOAuthToken(oauth.GenerateServerKey(name, srv.MCPURL))
		return err == nil && rec != nil && rec.AccessToken != ""
	}, 5*time.Second, 100*time.Millisecond, "token from the completed sign-in must be stored")
}
