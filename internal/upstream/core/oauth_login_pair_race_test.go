package core

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	uptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// gatedTokenStore delays SaveToken until release is closed, after signalling
// entered: a login whose code exchange succeeded but whose save is slow.
type gatedTokenStore struct {
	client.TokenStore
	entered chan struct{}
	release chan struct{}
}

func (g *gatedTokenStore) SaveToken(ctx context.Context, tok *client.Token) error {
	close(g.entered)
	<-g.release
	return g.TokenStore.SaveToken(ctx, tok)
}

// loginPairHandler registers a fresh client at the test server and returns an
// mcp-go handler for it plus an authorization code issued to that client.
func loginPairHandler(t *testing.T, f *staleDCRFixture, store client.TokenStore) (h *uptransport.OAuthHandler, code, state, verifier string) {
	t.Helper()
	const redirect = "http://127.0.0.1:1" + oauth.DefaultRedirectPath
	reg, err := f.srv.Server.RegisterClient(oauthserver.ClientConfig{RedirectURIs: []string{redirect}})
	require.NoError(t, err)
	h = uptransport.NewOAuthHandler(uptransport.OAuthConfig{
		ClientID:              reg.ClientID,
		ClientSecret:          reg.ClientSecret,
		RedirectURI:           redirect,
		Scopes:                []string{"read"},
		AuthServerMetadataURL: f.srv.IssuerURL + "/.well-known/oauth-authorization-server",
		PKCEEnabled:           true,
		TokenStore:            store,
	})
	verifier, err = uptransport.GenerateCodeVerifier()
	require.NoError(t, err)
	state, err = uptransport.GenerateState()
	require.NoError(t, err)
	authURL, err := h.GetAuthorizationURL(context.Background(), state, uptransport.GenerateCodeChallenge(verifier))
	require.NoError(t, err)

	u, err := url.Parse(authURL)
	require.NoError(t, err)
	form := u.Query()
	form.Set("username", "testuser")
	form.Set("password", "testpass")
	form.Set("consent", "on")
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.PostForm(f.srv.AuthorizationEndpoint, form)
	require.NoError(t, err)
	_ = resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	code = loc.Query().Get("code")
	require.NotEmpty(t, code, "authorize redirect %q", resp.Header.Get("Location"))
	return h, code, state, verifier
}

// Login A's code exchange succeeds but its token save is delayed; login B (a
// different client registration) completes meanwhile. Whatever ends up stored
// must be one login's refresh token next to THAT login's client registration,
// so the next refresh is accepted by the provider.
func TestLoginCompletion_TokenAndClientStoredAsOnePair(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{}, "login-pair-race")
	c := f.client(t, nil) // DCR mode: no operator client id

	gateA := &gatedTokenStore{
		TokenStore: oauth.NewPersistentTokenStore(f.name, f.srv.MCPURL, f.db),
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	hA, codeA, stateA, verA := loginPairHandler(t, f, gateA)
	hB, codeB, stateB, verB := loginPairHandler(t, f, oauth.NewPersistentTokenStore(f.name, f.srv.MCPURL, f.db))
	// Each login's own callback server (what received its callback); the
	// stored callback metadata must come from it, not the registry, which by
	// completion time may hold a replacement listener on another port.
	replacement, err := oauth.GetGlobalCallbackManager().StartCallbackServer(f.name, 0)
	require.NoError(t, err)
	cbA := &oauth.CallbackServer{Port: 41001, RedirectURI: "http://127.0.0.1:41001" + oauth.DefaultRedirectPath}
	cbB := &oauth.CallbackServer{Port: 41002, RedirectURI: "http://127.0.0.1:41002" + oauth.DefaultRedirectPath}
	ctx := context.Background()

	doneA := make(chan error, 1)
	go func() {
		stored, err := c.exchangeAuthorizationCode(ctx, hA, cbA, codeA, stateA, verA)
		if err == nil && !stored { // as finishOAuth(!stored)
			c.persistCompletedDCRCredentials(hA.GetClientID(), hA.GetClientSecret())
		}
		doneA <- err
	}()
	select {
	case <-gateA.entered:
	case err := <-doneA:
		t.Fatalf("login A finished before saving: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("login A never reached its token save")
	}

	storedB, err := c.exchangeAuthorizationCode(ctx, hB, cbB, codeB, stateB, verB)
	require.NoError(t, err)
	require.True(t, storedB)

	close(gateA.release)
	require.NoError(t, <-doneA)

	rec, err := f.db.GetOAuthToken(f.key)
	require.NoError(t, err)
	require.NotEmpty(t, rec.RefreshToken)
	require.NotEmpty(t, rec.ClientID)
	wantCB := cbA
	if rec.ClientID == hB.GetClientID() {
		wantCB = cbB
	}
	require.NotEqual(t, replacement.Port, wantCB.Port)
	assert.Equal(t, wantCB.Port, rec.CallbackPort)
	assert.Equal(t, wantCB.RedirectURI, rec.RedirectURI)

	// The provider accepts the stored pair only if the refresh token was
	// issued to the stored client.
	resp, err := http.PostForm(f.srv.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {rec.RefreshToken},
		"client_id":     {rec.ClientID},
		"client_secret": {rec.ClientSecret},
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "stored refresh token rejected for stored client: %v", body)
}

// A completion write that lands after a refresh failure cleared the stored
// client (keeping another login's refresh token) must not attach this login's
// client to that refresh token.
func TestPersistCompletedDCRCredentials_NotPairedWithForeignRefreshToken(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{}, "login-pair-cleared")
	c := f.client(t, nil)
	require.NoError(t, f.db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: f.key, AccessToken: "at-b", RefreshToken: "rt-b"}))
	c.persistCompletedDCRCredentials("client-a", "")
	assert.Empty(t, f.storedClientID(t))
}

// With oauth.extra_params.client_id the token request is sent with that
// client id (OAuthTransportWrapper overrides the handler's), so the exchange
// must not record the handler's client next to the token.
func TestLoginCompletion_ExtraParamsClientIDNotReplacedByHandlerClient(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{}, "login-pair-extra")
	c := f.client(t, &config.OAuthConfig{ExtraParams: map[string]string{"client_id": "from-extra-params"}})
	h, code, state, verifier := loginPairHandler(t, f, oauth.NewPersistentTokenStore(f.name, f.srv.MCPURL, f.db))
	stored, err := c.exchangeAuthorizationCode(context.Background(), h, nil, code, state, verifier)
	require.NoError(t, err)
	assert.False(t, stored, "an untagged exchange leaves the client write to finishOAuth")
	rec, err := f.db.GetOAuthToken(f.key)
	require.NoError(t, err)
	assert.NotEmpty(t, rec.RefreshToken)
	assert.NotEqual(t, h.GetClientID(), rec.ClientID)
}

// Every authorization-code exchange in this package goes through
// exchangeAuthorizationCode, so every login saves its token and client as one
// pair.
func TestLoginCompletion_AllExchangeSitesUseHelper(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	callers := map[string]int{}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, e.Name(), nil, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ProcessAuthorizationResponse" {
						callers[fn.Name.Name]++
					}
				}
				return true
			})
		}
	}
	assert.Equal(t, map[string]int{"exchangeAuthorizationCode": 1}, callers)
}

// Each exchange site completes through finishOAuth with the exchange's own
// result, never markOAuthComplete (which always rewrites the client and its
// callback metadata from the registry).
func TestLoginCompletion_ExchangeSitesFinishWithExchangeResult(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "connection_oauth.go", nil, 0)
	require.NoError(t, err)
	sites := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name.Name == "exchangeAuthorizationCode" {
			continue
		}
		calls := map[string][]*ast.CallExpr{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					calls[sel.Sel.Name] = append(calls[sel.Sel.Name], call)
				}
			}
			return true
		})
		if len(calls["exchangeAuthorizationCode"]) == 0 {
			continue
		}
		sites++
		assert.Empty(t, calls["markOAuthComplete"], "%s must finish with finishOAuth", fn.Name.Name)
		require.Len(t, calls["finishOAuth"], 1, fn.Name.Name)
		arg, ok := calls["finishOAuth"][0].Args[0].(*ast.UnaryExpr)
		require.True(t, ok, "%s: finishOAuth(!clientStored)", fn.Name.Name)
		id, ok := arg.X.(*ast.Ident)
		assert.True(t, ok && id.Name == "clientStored", "%s: finishOAuth(!clientStored)", fn.Name.Name)
	}
	assert.Equal(t, 3, sites)
}
