package core

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"

	uptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// Stale DCR client_id at login. Cloudflare's MCP OAuth servers delete DCR
// clients after a while; mcpproxy kept reusing the persisted client_id, the
// browser landed on HTTP 400 {"error":"invalid_request","error_description":
// "Invalid client_id"} and Cloudflare never redirected back, so the login
// hung with no signal. Before handing out the authorization URL for a
// PERSISTED DCR client_id, mcpproxy now probes it once (no redirects) and, if
// the client is rejected, re-registers exactly once.

type staleDCRFixture struct {
	srv  *oauthserver.ServerResult
	db   *storage.BoltDB
	name string
	key  string
}

func newStaleDCRFixture(t *testing.T, mode oauthserver.ErrorMode, name string) *staleDCRFixture {
	t.Helper()
	return newStaleDCRFixtureWithOptions(t, oauthserver.Options{ErrorMode: mode}, name)
}

func newStaleDCRFixtureWithOptions(t *testing.T, opts oauthserver.Options, name string) *staleDCRFixture {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "")
	t.Setenv("HEADLESS", "1") // never open a browser from a test

	srv := oauthserver.Start(t, opts)
	t.Cleanup(func() { _ = srv.Shutdown() })
	db, err := storage.NewBoltDB(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	stopCallbackServerAfterTest(t, name)

	return &staleDCRFixture{srv: srv, db: db, name: name, key: oauth.GenerateServerKey(name, srv.MCPURL)}
}

// seedPersistedClient stores a DCR registration the way a login months ago
// did: client id, the callback port and the exact redirect_uri it used.
func (f *staleDCRFixture) seedPersistedClient(t *testing.T, clientID string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, oauth.DefaultRedirectPath)
	require.NoError(t, f.db.UpdateOAuthClientCredentials(f.key, clientID, "", port, redirectURI))
}

func (f *staleDCRFixture) client(t *testing.T, oauthBlock *config.OAuthConfig) *Client {
	t.Helper()
	cfg := &config.ServerConfig{
		Name:     f.name,
		URL:      f.srv.MCPURL,
		Protocol: "http",
		Enabled:  true,
		OAuth:    oauthBlock,
	}
	c, err := NewClient(f.name, cfg, zap.NewNop(), nil, &config.Config{}, f.db, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Disconnect() })
	return c
}

func (f *staleDCRFixture) storedClientID(t *testing.T) string {
	t.Helper()
	id, _, _, _, err := f.db.GetOAuthClientCredentials(f.key)
	require.NoError(t, err)
	return id
}

func clientIDOf(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	return u.Query().Get("client_id")
}

func quickLogin(t *testing.T, c *Client) (*OAuthStartResult, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // ends the background callback waiter
	return c.StartOAuthFlowQuick(ctx)
}

func TestStaleDCRClient_LoginReRegistersOnce(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{AuthorizeUnknownClientJSON400: true}, "stale-dcr-reregister")
	f.seedPersistedClient(t, "dead-client")
	c := f.client(t, &config.OAuthConfig{Scopes: []string{"read"}})

	result, err := quickLogin(t, c)
	require.NoError(t, err)
	require.NotNil(t, result)

	newID := clientIDOf(t, result.AuthURL)
	assert.NotEmpty(t, newID)
	assert.NotEqual(t, "dead-client", newID, "the emitted authorization URL must carry the re-registered client_id")
	_, known := f.srv.Server.GetClient(newID)
	assert.True(t, known, "the new client_id must be one the authorization server registered")
	assert.Equal(t, newID, f.storedClientID(t), "the new registration must be persisted")
	assert.EqualValues(t, 1, f.srv.Server.RegistrationCount(), "exactly one re-registration")
	assert.EqualValues(t, 2, f.srv.Server.AuthorizeGETCount(), "one probe of the dead client, one of the fresh one")
}

func TestStaleDCRClient_StaticClientIDNeverProbedOrCleared(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{AuthorizeUnknownClientJSON400: true}, "stale-dcr-static")
	c := f.client(t, &config.OAuthConfig{ClientID: "static-unknown", Scopes: []string{"read"}})

	result, err := quickLogin(t, c)
	require.NoError(t, err)
	assert.Equal(t, "static-unknown", clientIDOf(t, result.AuthURL))
	assert.EqualValues(t, 0, f.srv.Server.AuthorizeGETCount(), "an operator static client_id is never probed")
	assert.EqualValues(t, 0, f.srv.Server.RegistrationCount())
}

func TestStaleDCRClient_FreshRegistrationNotProbed(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{AuthorizeRejectAllClientsJSON400: true}, "stale-dcr-fresh")
	c := f.client(t, &config.OAuthConfig{Scopes: []string{"read"}})

	result, err := quickLogin(t, c)
	require.NoError(t, err)
	assert.NotEmpty(t, clientIDOf(t, result.AuthURL))
	assert.EqualValues(t, 1, f.srv.Server.RegistrationCount())
	assert.EqualValues(t, 0, f.srv.Server.AuthorizeGETCount(), "a client registered in this same attempt is not probed")
}

func TestStaleDCRClient_FreshClientAlsoRejected(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{AuthorizeRejectAllClientsJSON400: true}, "stale-dcr-reject-all")
	f.seedPersistedClient(t, "dead-client")
	c := f.client(t, &config.OAuthConfig{Scopes: []string{"read"}})

	_, err := quickLogin(t, c)
	require.Error(t, err)
	var flowErr *contracts.OAuthFlowError
	require.True(t, errors.As(err, &flowErr), "a structured OAuth flow error, got %T: %v", err, err)
	assert.Equal(t, contracts.OAuthErrorFlowFailed, flowErr.ErrorType)
	assert.Contains(t, flowErr.Message, "rejected")
	assert.Contains(t, flowErr.Message, "Invalid client_id")
	assert.NotEmpty(t, flowErr.Suggestion)
	assert.EqualValues(t, 1, f.srv.Server.RegistrationCount(), "never more than one re-registration per attempt")
	assert.EqualValues(t, 2, f.srv.Server.AuthorizeGETCount())
	assert.Empty(t, f.storedClientID(t), "a registration the server rejects is not kept")
}

func TestStaleDCRClient_ProbeFailureFailsOpen(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{AuthorizeUnknownClientJSON400: true}, "stale-dcr-probe-fail")
	f.seedPersistedClient(t, "dead-client")
	c := f.client(t, &config.OAuthConfig{Scopes: []string{"read"}})

	prev := authorizeProbeHTTPClient
	authorizeProbeHTTPClient = &http.Client{Transport: probeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unreachable")
	})}
	t.Cleanup(func() { authorizeProbeHTTPClient = prev })

	result, err := quickLogin(t, c)
	require.NoError(t, err)
	assert.Equal(t, "dead-client", clientIDOf(t, result.AuthURL), "a failed probe changes nothing")
	assert.Equal(t, "dead-client", f.storedClientID(t))
	assert.EqualValues(t, 0, f.srv.Server.RegistrationCount())
}

func TestStaleDCRClient_LiveClientUntouched(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{AuthorizeUnknownClientJSON400: true}, "stale-dcr-live")
	live, err := f.srv.Server.RegisterClient(oauthserver.ClientConfig{
		RedirectURIs: []string{"http://127.0.0.1/oauth/callback"},
	})
	require.NoError(t, err)
	f.seedPersistedClient(t, live.ClientID)
	c := f.client(t, &config.OAuthConfig{Scopes: []string{"read"}})

	result, err := quickLogin(t, c)
	require.NoError(t, err)
	assert.Equal(t, live.ClientID, clientIDOf(t, result.AuthURL))
	assert.Equal(t, live.ClientID, f.storedClientID(t))
	assert.EqualValues(t, 0, f.srv.Server.RegistrationCount())
	assert.EqualValues(t, 1, f.srv.Server.AuthorizeGETCount(), "one probe")
}

// newStaleDCRHelperHandler builds an mcp-go OAuth handler bound to the test
// server's metadata, as the transport would for a persisted client id.
func newStaleDCRHelperHandler(f *staleDCRFixture, clientID string) *uptransport.OAuthHandler {
	return uptransport.NewOAuthHandler(uptransport.OAuthConfig{
		ClientID:              clientID,
		RedirectURI:           "http://127.0.0.1:1" + oauth.DefaultRedirectPath,
		Scopes:                []string{"read"},
		AuthServerMetadataURL: f.srv.IssuerURL + "/.well-known/oauth-authorization-server",
		PKCEEnabled:           true,
	})
}

// The helper itself, as handleOAuthAuthorization and
// handleOAuthAuthorizationWithResult call it (both block on a browser
// callback, so they are not driven end to end here).
func TestRecoverStaleDCRClient_Helper(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{AuthorizeUnknownClientJSON400: true}, "stale-dcr-helper")
	f.seedPersistedClient(t, "dead-client")
	c := &Client{
		config:  &config.ServerConfig{Name: f.name, URL: f.srv.MCPURL, Protocol: "http"},
		logger:  zap.NewNop(),
		storage: f.db,
	}
	ctx := context.Background()

	t.Run("extra_params client_id override is never probed", func(t *testing.T) {
		h := newStaleDCRHelperHandler(f, "dead-client")
		authURL, err := h.GetAuthorizationURL(ctx, "st", "cc")
		require.NoError(t, err)
		extra := map[string]string{"client_id": "operator-supplied"}
		authURL = c.applyExtraParamsToAuthURL(authURL, extra)

		got, err := c.recoverStaleDCRClient(ctx, h, "dead-client", authURL, "st", "cc", extra, "")
		require.NoError(t, err)
		assert.Equal(t, authURL, got)
		assert.EqualValues(t, 0, f.srv.Server.AuthorizeGETCount())
		assert.Equal(t, "dead-client", f.storedClientID(t))
	})

	t.Run("dead client re-registered, extra params kept on the rebuilt URL", func(t *testing.T) {
		h := newStaleDCRHelperHandler(f, "dead-client")
		authURL, err := h.GetAuthorizationURL(ctx, "st", "cc")
		require.NoError(t, err)
		extra := map[string]string{"audience": "aud-1"}
		authURL = c.applyExtraParamsToAuthURL(authURL, extra)

		got, err := c.recoverStaleDCRClient(ctx, h, "dead-client", authURL, "st", "cc", extra, "corr")
		require.NoError(t, err)
		newID := clientIDOf(t, got)
		assert.NotEqual(t, "dead-client", newID)
		assert.Equal(t, newID, h.GetClientID())
		assert.Equal(t, newID, f.storedClientID(t))
		u, _ := url.Parse(got)
		assert.Equal(t, "aud-1", u.Query().Get("audience"))
		assert.Equal(t, "st", u.Query().Get("state"))
		assert.EqualValues(t, 1, f.srv.Server.RegistrationCount())
	})
}

// Review round 1 (opencode Sol 6.1) findings, each pinned by a test.
func TestRecoverStaleDCRClient_ReviewRound1(t *testing.T) {
	ctx := context.Background()
	newHelperClient := func(f *staleDCRFixture) *Client {
		return &Client{
			config:  &config.ServerConfig{Name: f.name, URL: f.srv.MCPURL, Protocol: "http"},
			logger:  zap.NewNop(),
			storage: f.db,
		}
	}

	// A concurrent login already replaced the rejected registration: the
	// compare-and-clear finds a different id. This flow must not register yet
	// another client and overwrite the winner's credentials (which would pair
	// the winner's grant with the wrong client_id).
	t.Run("registration replaced concurrently is not overwritten", func(t *testing.T) {
		f := newStaleDCRFixture(t, oauthserver.ErrorMode{AuthorizeUnknownClientJSON400: true}, "stale-dcr-concurrent")
		f.seedPersistedClient(t, "winner-client")
		c := newHelperClient(f)
		h := newStaleDCRHelperHandler(f, "dead-client")
		authURL, err := h.GetAuthorizationURL(ctx, "st", "cc")
		require.NoError(t, err)

		_, err = c.recoverStaleDCRClient(ctx, h, "dead-client", authURL, "st", "cc", nil, "corr")
		require.Error(t, err, "a structured retry error, not a URL with yet another client")
		assert.Equal(t, "winner-client", f.storedClientID(t))
		assert.EqualValues(t, 0, f.srv.Server.RegistrationCount())
	})

	// An extra_params client_id equal to the persisted id would be re-applied
	// over the fresh client's id after re-registration, so the second probe
	// would test the dead id again and wrongly clear the fresh registration.
	// Any operator-supplied client_id in extra_params opts out of recovery.
	t.Run("extra_params client_id equal to the persisted id is never probed", func(t *testing.T) {
		f := newStaleDCRFixture(t, oauthserver.ErrorMode{AuthorizeUnknownClientJSON400: true}, "stale-dcr-extra-same")
		f.seedPersistedClient(t, "dead-client")
		c := newHelperClient(f)
		h := newStaleDCRHelperHandler(f, "dead-client")
		authURL, err := h.GetAuthorizationURL(ctx, "st", "cc")
		require.NoError(t, err)
		extra := map[string]string{"client_id": "dead-client"}
		authURL = c.applyExtraParamsToAuthURL(authURL, extra)

		got, err := c.recoverStaleDCRClient(ctx, h, "dead-client", authURL, "st", "cc", extra, "")
		require.NoError(t, err)
		assert.Equal(t, authURL, got)
		assert.EqualValues(t, 0, f.srv.Server.AuthorizeGETCount())
		assert.EqualValues(t, 0, f.srv.Server.RegistrationCount())
		assert.Equal(t, "dead-client", f.storedClientID(t))
	})
}

// Review round 2 (opencode Sol 6.1) findings, each pinned by a test.
func TestRecoverStaleDCRClient_ReviewRound2(t *testing.T) {
	ctx := context.Background()

	// Another login saves its own registration WHILE this recovery's DCR call
	// is in flight (after the compare-and-clear). The recovered client must
	// not overwrite it.
	t.Run("registration saved during re-registration is not overwritten", func(t *testing.T) {
		var f *staleDCRFixture
		var once sync.Once
		f = newStaleDCRFixtureWithOptions(t, oauthserver.Options{
			ErrorMode: oauthserver.ErrorMode{AuthorizeUnknownClientJSON400: true},
			OnRegister: func() {
				once.Do(func() {
					require.NoError(t, f.db.UpdateOAuthClientCredentials(f.key, "concurrent-winner", "", 1, "http://127.0.0.1:1"+oauth.DefaultRedirectPath))
				})
			},
		}, "stale-dcr-race-after-clear")
		f.seedPersistedClient(t, "dead-client")
		c := &Client{
			config:  &config.ServerConfig{Name: f.name, URL: f.srv.MCPURL, Protocol: "http"},
			logger:  zap.NewNop(),
			storage: f.db,
		}
		h := newStaleDCRHelperHandler(f, "dead-client")
		authURL, err := h.GetAuthorizationURL(ctx, "st", "cc")
		require.NoError(t, err)

		_, err = c.recoverStaleDCRClient(ctx, h, "dead-client", authURL, "st", "cc", nil, "corr")
		require.Error(t, err, "sign in again: another login's registration is now stored")
		assert.Equal(t, "concurrent-winner", f.storedClientID(t))
	})

	// mcp-go keeps the old client_secret in memory when a re-registration
	// returns none, then sends it with the new client_id at code exchange. A
	// rejected CONFIDENTIAL registration is therefore cleared and the user
	// is asked to sign in again (a fresh handler registers cleanly), rather
	// than re-registering on the handler that still holds the old secret.
	t.Run("rejected confidential registration is cleared, not re-registered in place", func(t *testing.T) {
		f := newStaleDCRFixture(t, oauthserver.ErrorMode{AuthorizeUnknownClientJSON400: true}, "stale-dcr-secret")
		f.seedPersistedClient(t, "dead-client")
		c := &Client{
			config:  &config.ServerConfig{Name: f.name, URL: f.srv.MCPURL, Protocol: "http"},
			logger:  zap.NewNop(),
			storage: f.db,
		}
		h := uptransport.NewOAuthHandler(uptransport.OAuthConfig{
			ClientID:              "dead-client",
			ClientSecret:          "old-secret",
			RedirectURI:           "http://127.0.0.1:1" + oauth.DefaultRedirectPath,
			Scopes:                []string{"read"},
			AuthServerMetadataURL: f.srv.IssuerURL + "/.well-known/oauth-authorization-server",
			PKCEEnabled:           true,
		})
		authURL, err := h.GetAuthorizationURL(ctx, "st", "cc")
		require.NoError(t, err)

		_, err = c.recoverStaleDCRClient(ctx, h, "dead-client", authURL, "st", "cc", nil, "corr")
		require.Error(t, err)
		assert.Equal(t, "", f.storedClientID(t), "the rejected registration is removed")
		assert.EqualValues(t, 0, f.srv.Server.RegistrationCount())
	})
}

// The three authorization-URL emission paths are near-duplicates; all three
// must run the stale-client recovery (they change in lockstep).
func TestRecoverStaleDCRClient_CalledFromAllAuthorizePaths(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "connection_oauth.go", nil, 0)
	require.NoError(t, err)
	want := map[string]bool{
		"getAuthorizationURLQuick":           false,
		"handleOAuthAuthorization":           false,
		"handleOAuthAuthorizationWithResult": false,
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, tracked := want[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "recoverStaleDCRClient" {
					want[fn.Name.Name] = true
				}
			}
			return true
		})
	}
	for name, called := range want {
		assert.True(t, called, "%s must call recoverStaleDCRClient", name)
	}
}

type probeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f probeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
