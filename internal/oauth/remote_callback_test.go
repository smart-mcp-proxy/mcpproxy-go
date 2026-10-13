package oauth

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func remoteCallbackFixture(t *testing.T, name string) (*CallbackServerManager, *CallbackServer) {
	t.Helper()
	m := &CallbackServerManager{servers: make(map[string]*CallbackServer), logger: zap.NewNop()}
	c, err := m.StartCallbackServerOnHost(name, CallbackBinding{Logger: zap.NewNop()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.StopCallbackServer(name) })
	require.NoError(t, c.configureRemoteCallback("https://proxy.example/oauth/callback/"+url.PathEscape(name)))
	return m, c
}

func TestRemoteCallbackStateRouting(t *testing.T) {
	m, c := remoteCallbackFixture(t, "linear")
	ch := c.RegisterState("waiting-state")
	for _, query := range []string{"code=x", "code=x&state=unknown", "code=x&state=waiting-state&state=other", "state=waiting-state", "code=x&error=denied&state=waiting-state", "code=x&code=y&state=waiting-state"} {
		w := httptest.NewRecorder()
		m.RemoteCallbackHandler(w, httptest.NewRequest(http.MethodGet, "/oauth/callback/linear?"+query, nil))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.True(t, c.HasState("waiting-state"))
	}
	w := httptest.NewRecorder()
	m.RemoteCallbackHandler(w, httptest.NewRequest(http.MethodGet, "/oauth/callback/linear?code=real-code&state=waiting-state", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	require.Equal(t, "real-code", (<-ch)["code"])
	replay := httptest.NewRecorder()
	m.RemoteCallbackHandler(replay, httptest.NewRequest(http.MethodGet, "/oauth/callback/linear?code=real-code&state=waiting-state", nil))
	require.Equal(t, http.StatusBadRequest, replay.Code)
}

func TestRemoteCallbackDoesNotCrossServersOrExposeLocalFlows(t *testing.T) {
	m, c := remoteCallbackFixture(t, "linear")
	other, err := m.StartCallbackServerOnHost("notion", CallbackBinding{Logger: zap.NewNop()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.StopCallbackServer("notion") })
	other.RegisterState("notion-state")
	c.RegisterState("linear-state")
	for _, path := range []string{"/oauth/callback/notion?code=x&state=notion-state", "/oauth/callback/missing?code=x&state=linear-state"} {
		w := httptest.NewRecorder()
		m.RemoteCallbackHandler(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNotFound, w.Code)
	}
	w := httptest.NewRecorder()
	m.RemoteCallbackHandler(w, httptest.NewRequest(http.MethodGet, "/oauth/callback/linear?code=x&state=notion-state", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.True(t, other.HasState("notion-state"))
	require.True(t, c.HasState("linear-state"))
	post := httptest.NewRecorder()
	m.RemoteCallbackHandler(post, httptest.NewRequest(http.MethodPost, "/oauth/callback/linear", nil))
	require.Equal(t, http.StatusMethodNotAllowed, post.Code)
}

func TestRemoteCallbackCancellationAndExpiry(t *testing.T) {
	m, c := remoteCallbackFixture(t, "linear")
	ch := c.RegisterState("cancel-state")
	w := httptest.NewRecorder()
	m.RemoteCallbackHandler(w, httptest.NewRequest(http.MethodGet, "/oauth/callback/linear?error=access_denied&state=cancel-state", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "access_denied", (<-ch)["error"])
	c.RegisterState("expired")
	c.UnregisterState("expired")
	w = httptest.NewRecorder()
	m.RemoteCallbackHandler(w, httptest.NewRequest(http.MethodGet, "/oauth/callback/linear?code=x&state=expired", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
	c.RegisterState("live")
	require.Error(t, c.configureRemoteCallback("https://another.example/oauth/callback/linear"))
}

func TestRemoteCallbackPKCEExchangeThroughHTTPS(t *testing.T) {
	m, c := remoteCallbackFixture(t, "linear")
	proxy := httptest.NewTLSServer(http.HandlerFunc(m.RemoteCallbackHandler))
	defer proxy.Close()
	redirect := proxy.URL + "/oauth/callback/linear"
	require.NoError(t, c.configureRemoteCallback(redirect))
	provider := startTestProvider(t, redirect, "remote-client")
	verifier, challenge := pkcePair(strings.Repeat("v", 43))
	ch := c.RegisterState("remote-state")
	form := url.Values{"response_type": {"code"}, "client_id": {"remote-client"}, "redirect_uri": {redirect}, "scope": {"read"}, "state": {"remote-state"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "username": {testCallbackUser}, "password": {testCallbackPass}}
	form.Set("consent", "on")
	form.Set("action", "approve")
	response, err := proxy.Client().PostForm(provider.AuthorizationEndpoint, form)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	select {
	case params := <-ch:
		token := exchangeCode(t, provider, "remote-client", redirect, params["code"], verifier)
		require.NotEmpty(t, token["access_token"])
	case <-time.After(5 * time.Second):
		t.Fatal("HTTPS callback was not delivered")
	}
}

func TestCreateOAuthConfigRemoteRedirect(t *testing.T) {
	name := "remote-create-config"
	defer globalCallbackManager.StopCallbackServer(name)
	cfg, err := CreateOAuthConfigWithLogger(&config.ServerConfig{Name: name, URL: "http://127.0.0.1:1/mcp", Protocol: "http", OAuth: &config.OAuthConfig{ClientID: "test-client", Scopes: []string{"read"}, CallbackBaseURL: "https://proxy.example:8447"}}, nil, zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, "https://proxy.example:8447/oauth/callback/"+name, cfg.RedirectURI)
	callback, ok := GetCallbackServer(name)
	require.True(t, ok)
	require.Equal(t, cfg.RedirectURI, callback.EffectiveRedirectURI())
	require.Contains(t, callback.RedirectURI, "http://127.0.0.1:")
}

func TestRemoteCallbackMigratesDCRRegistrationOnce(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)
	name := "remote-dcr-upgrade"
	defer globalCallbackManager.StopCallbackServer(name)
	key := GenerateServerKey(name, upstream.URL)
	port := reserveLoopbackPort(t)
	require.NoError(t, store.UpdateOAuthClientCredentials(key, "old-client", "", port, "http://127.0.0.1:12345/oauth/callback"))
	s := &config.ServerConfig{Name: name, URL: upstream.URL, OAuth: &config.OAuthConfig{Scopes: []string{"read"}, CallbackBaseURL: "https://proxy.example"}}
	first, err := CreateOAuthConfigWithLogger(s, store, zap.NewNop())
	require.NoError(t, err)
	require.Empty(t, first.ClientID, "loopback DCR registrations must not be reused for HTTPS")
	callback, _ := GetCallbackServer(name)
	require.NoError(t, store.UpdateOAuthClientCredentials(key, "remote-client", "", callback.Port, first.RedirectURI))
	retry, err := CreateOAuthConfigWithLogger(s, store, zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, "remote-client", retry.ClientID, "unchanged HTTPS callbacks must retain DCR registrations")
	s.OAuth.CallbackBaseURL = "https://another.example"
	changed, err := CreateOAuthConfigWithLogger(s, store, zap.NewNop())
	require.NoError(t, err)
	require.Empty(t, changed.ClientID, "a changed HTTPS origin requires fresh registration")
}

func TestRemoteCallbackKeepsDCRWhenPrivatePortChanges(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)
	name := "remote-private-port-conflict"
	t.Cleanup(func() { _ = globalCallbackManager.StopCallbackServer(name) })
	redirect := "https://proxy.example/oauth/callback/" + name
	require.NoError(t, store.UpdateOAuthClientCredentials(GenerateServerKey(name, upstream.URL), "remote-client", "", port, redirect))
	cfg, err := CreateOAuthConfigWithLogger(&config.ServerConfig{Name: name, URL: upstream.URL, OAuth: &config.OAuthConfig{Scopes: []string{"read"}, CallbackBaseURL: "https://proxy.example"}}, store, zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, "remote-client", cfg.ClientID)
	require.Equal(t, redirect, cfg.RedirectURI)
	callback, _ := GetCallbackServer(name)
	require.NotEqual(t, port, callback.Port)
}
