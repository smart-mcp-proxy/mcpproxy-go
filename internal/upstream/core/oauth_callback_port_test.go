package core

import (
	"fmt"
	"net"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newCallbackPortTestStorage(t *testing.T) *storage.BoltDB {
	t.Helper()
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func stopCallbackServerAfterTest(t *testing.T, serverName string) {
	t.Helper()
	t.Cleanup(func() {
		if mgr := oauth.GetGlobalCallbackManager(); mgr != nil {
			_ = mgr.StopCallbackServer(serverName)
		}
	})
}

// TestResolveCallbackPortForPersistence_UsesLiveCallbackServer covers part (a)
// of the fix: the port we persist must be the one the callback server is
// actually listening on, not merely whatever happened to be stored already.
func TestResolveCallbackPortForPersistence_UsesLiveCallbackServer(t *testing.T) {
	db := newCallbackPortTestStorage(t)
	serverName := "live-callback-port"
	serverKey := oauth.GenerateServerKey(serverName, "https://example.test/mcp")

	// A record exists with no port — the pre-fix code would keep persisting 0.
	require.NoError(t, db.UpdateOAuthClientCredentials(serverKey, "static-client", "", 0))

	stopCallbackServerAfterTest(t, serverName)
	callbackServer, err := oauth.GetGlobalCallbackManager().StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	require.Positive(t, callbackServer.Port)

	got := resolveCallbackPortForPersistence(serverName, serverKey, db)
	assert.Equal(t, callbackServer.Port, got,
		"the live callback server port must win over the stored value")
}

// TestResolveCallbackPortForPersistence_FallsBackToStoredPort verifies we do not
// regress the original behavior when no callback server is running.
func TestResolveCallbackPortForPersistence_FallsBackToStoredPort(t *testing.T) {
	db := newCallbackPortTestStorage(t)
	serverName := "no-live-callback-port"
	serverKey := oauth.GenerateServerKey(serverName, "https://example.test/mcp")

	require.NoError(t, db.UpdateOAuthClientCredentials(serverKey, "static-client", "", 43117))

	got := resolveCallbackPortForPersistence(serverName, serverKey, db)
	assert.Equal(t, 43117, got)
}

// loginCycle runs one complete OAuth login for serverConfig against a real test
// OAuth provider using production code only:
//
//	oauth.CreateOAuthConfig()  -> allocates/binds the loopback callback server
//	client.markOAuthComplete() -> persists credentials, then frees the port
//
// It returns the loopback port the callback server bound for this login and the
// redirect_uri that would have been sent to the provider.
func loginCycle(t *testing.T, serverConfig *config.ServerConfig, db *storage.BoltDB) (port int, redirectURI string) {
	t.Helper()

	oauthConfig := oauth.CreateOAuthConfig(serverConfig, db)
	require.NotNil(t, oauthConfig, "OAuth config creation must succeed")

	callbackServer, ok := oauth.GetCallbackServer(serverConfig.Name)
	require.True(t, ok, "callback server must be running during the flow")
	port = callbackServer.Port
	redirectURI = oauthConfig.RedirectURI

	mcpClient, err := mcpclient.NewOAuthStreamableHttpClient(serverConfig.URL, *oauthConfig)
	require.NoError(t, err)

	c := &Client{
		config:  serverConfig,
		storage: db,
		logger:  zap.NewNop(),
		client:  mcpClient,
	}

	// This is what the real connection does when the browser callback lands:
	// persist the credentials, then shut the callback server down.
	c.markOAuthComplete()

	return port, redirectURI
}

// TestOAuthCallbackPort_StableAcrossLogins_StaticClient is the regression test
// for issue #975. With a static oauth.client_id, the loopback callback port used
// to change on every login, so providers that require an exact callback URL
// (GitHub OAuth Apps reject wildcards) could never be made to work.
func TestOAuthCallbackPort_StableAcrossLogins_StaticClient(t *testing.T) {
	provider := oauthserver.Start(t, oauthserver.Options{
		Clients: []oauthserver.ClientConfig{{
			ClientID:     "static-client",
			ClientName:   "Static Client",
			RedirectURIs: []string{"http://127.0.0.1/oauth/callback"},
		}},
	})
	t.Cleanup(func() { _ = provider.Shutdown() })

	db := newCallbackPortTestStorage(t)
	serverName := "static-client-port-stability"
	stopCallbackServerAfterTest(t, serverName)

	serverConfig := &config.ServerConfig{
		Name:     serverName,
		URL:      provider.MCPURL,
		Protocol: "http",
		OAuth: &config.OAuthConfig{
			ClientID: "static-client",
		},
	}

	firstPort, firstURI := loginCycle(t, serverConfig, db)
	secondPort, secondURI := loginCycle(t, serverConfig, db)

	assert.Equal(t, firstPort, secondPort,
		"a statically configured OAuth client must reuse the same loopback callback port (issue #975): got %d then %d",
		firstPort, secondPort)
	assert.Equal(t, firstURI, secondURI,
		"the redirect_uri sent to the provider must be identical across logins")

	// And the record must survive: the static client is never re-registered.
	serverKey := oauth.GenerateServerKey(serverName, serverConfig.URL)
	storedClientID, _, storedPort, err := db.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Equal(t, "static-client", storedClientID)
	assert.Equal(t, firstPort, storedPort, "the live callback port must be persisted")
}

// TestOAuthCallbackPort_PinnedRedirectURI_StableAcrossLogins covers part (c):
// an operator who pins oauth.redirect_uri gets exactly that port and URI, on
// every login, with no reliance on stored state at all.
func TestOAuthCallbackPort_PinnedRedirectURI_StableAcrossLogins(t *testing.T) {
	provider := oauthserver.Start(t, oauthserver.Options{
		Clients: []oauthserver.ClientConfig{{
			ClientID:     "pinned-client",
			ClientName:   "Pinned Client",
			RedirectURIs: []string{"http://127.0.0.1/oauth/callback"},
		}},
	})
	t.Cleanup(func() { _ = provider.Shutdown() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	pinnedPort := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	pinnedURI := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", pinnedPort)

	db := newCallbackPortTestStorage(t)
	serverName := "pinned-client-port-stability"
	stopCallbackServerAfterTest(t, serverName)

	serverConfig := &config.ServerConfig{
		Name:     serverName,
		URL:      provider.MCPURL,
		Protocol: "http",
		OAuth: &config.OAuthConfig{
			ClientID:    "pinned-client",
			RedirectURI: pinnedURI,
		},
	}

	firstPort, firstURI := loginCycle(t, serverConfig, db)
	secondPort, secondURI := loginCycle(t, serverConfig, db)

	assert.Equal(t, pinnedPort, firstPort)
	assert.Equal(t, pinnedPort, secondPort)
	assert.Equal(t, pinnedURI, firstURI)
	assert.Equal(t, pinnedURI, secondURI)
}
