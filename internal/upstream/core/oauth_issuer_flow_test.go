package core

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	uptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"

	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// browserApprove plays the browser for an authorization URL mcpproxy handed
// out: it approves at the test server and follows the redirect into the real
// callback listener. It returns the page the user sees.
func browserApprove(t *testing.T, srv *oauthserver.ServerResult, authURL string) (int, string) {
	t.Helper()
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	form := u.Query()
	form.Set("username", "testuser")
	form.Set("password", "testpass")
	form.Set("consent", "on")
	form.Set("action", "approve")
	hc := &http.Client{Timeout: 10 * time.Second}
	resp, err := hc.PostForm(srv.AuthorizationEndpoint, form)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(raw)
}

func watchOAuthFailures(t *testing.T, serverName string) <-chan error {
	t.Helper()
	failures := make(chan error, 8)
	tm := oauth.GetTokenStoreManager()
	tm.SetOAuthFailureCallback(func(server string, failure error) {
		if server == serverName {
			failures <- failure
		}
	})
	t.Cleanup(func() { tm.SetOAuthFailureCallback(nil) })
	return failures
}

// Spec 135 FR-002/FR-003/FR-014 (plan D2), end to end: a real login against
// the in-repo test server, through the real callback listener. A wrong or
// (when advertised) missing iss refuses the login before the code is
// exchanged: the token endpoint receives nothing. The explicit metadata URL
// path (source configured) is covered too; mcp-go's own RFC 9207 check does
// not engage there, so mcpproxy's decision is what refuses it.
func TestLogin_IssuerDecision_EndToEnd(t *testing.T) {
	cases := []struct {
		name       string
		mode       oauthserver.IssMode
		advertise  bool
		configured bool
		skip       bool
		refused    bool
	}{
		{"CorrectAdvertised", oauthserver.IssCorrect, true, false, false, false},
		{"WrongIss", oauthserver.IssWrong, false, false, false, true},
		{"AbsentAdvertised", oauthserver.IssOmit, true, false, false, true},
		{"AbsentNotAdvertised", oauthserver.IssOmit, false, false, false, false},
		{"EmptyIss", oauthserver.IssEmpty, false, false, false, true},
		{"ConfiguredCorrect", oauthserver.IssCorrect, true, true, false, false},
		{"ConfiguredWrong", oauthserver.IssWrong, true, true, false, true},
		{"ConfiguredAbsentAdvertised", oauthserver.IssOmit, true, true, false, true},
		{"WrongIssSkip", oauthserver.IssWrong, true, false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := "iss-e2e-" + strings.ToLower(tc.name)
			f := newStaleDCRFixtureWithOptions(t, oauthserver.Options{IssMode: tc.mode, AdvertiseIss: tc.advertise}, name)
			block := &config.OAuthConfig{Scopes: []string{"read"}}
			if tc.configured {
				block.AuthServerMetadataURL = f.srv.IssuerURL + "/.well-known/oauth-authorization-server"
			}
			if tc.skip {
				skip := true
				block.SkipIssuerValidation = &skip
			}
			c := f.client(t, block)
			failures := watchOAuthFailures(t, name)

			result, err := quickLogin(t, c)
			require.NoError(t, err)
			require.NotEmpty(t, result.AuthURL)

			status, body := browserApprove(t, f.srv, result.AuthURL)
			if !tc.refused {
				assert.Equal(t, http.StatusOK, status, body)
				require.Eventually(t, func() bool { return f.srv.Server.TokenRequestCount() >= 1 }, 10*time.Second, 20*time.Millisecond,
					"an accepted response is exchanged")
				return
			}
			assert.Equal(t, http.StatusBadRequest, status)
			assert.Contains(t, body, "expected authorization server")
			select {
			case failure := <-failures:
				assert.Equal(t, diagnostics.OAuthIssuerMismatch, diagnostics.Classify(failure, diagnostics.ClassifierHints{}), failure.Error())
			case <-time.After(5 * time.Second):
				t.Fatal("refused login was not reported")
			}
			select {
			case dup := <-failures:
				t.Fatalf("refusal recorded twice: %v", dup)
			case <-time.After(200 * time.Millisecond):
			}
			assert.EqualValues(t, 0, f.srv.Server.TokenRequestCount(), "the code must never reach the token endpoint")
		})
	}
}

// US1.5: a flow started against a hostile authorization server A receives a
// response carrying the honest server H's iss (the browser was handed to H).
// The exchange refuses it, so A's token endpoint never sees H's code.
func TestMixUp_HonestIssRefusedInHostileFlow(t *testing.T) {
	hostile := newStaleDCRFixture(t, oauthserver.ErrorMode{}, "mixup-hostile")
	honest := oauthserver.Start(t, oauthserver.Options{})
	t.Cleanup(func() { _ = honest.Shutdown() })
	c := hostile.client(t, nil)

	h, code, state, verifier := loginPairHandler(t, hostile, oauth.NewPersistentTokenStore(hostile.name, hostile.srv.MCPURL, hostile.db))
	meta, err := h.GetServerMetadata(context.Background())
	require.NoError(t, err)
	exp := oauth.IssuerExpectation{Expected: oauth.ExpectedIssuerFromMetadata(oauth.IssuerSourceDiscovered,
		meta.Issuer, meta.AuthorizationResponseIssParameterSupported, "", meta.AuthorizationEndpoint, meta.TokenEndpoint)}
	before := hostile.srv.Server.TokenRequestCount()

	q := url.Values{"code": {code}, "state": {state}, "iss": {honest.IssuerURL}}
	_, err = c.exchangeAuthorizationCode(context.Background(), h, nil, q, exp, verifier)
	var mm *oauth.IssuerMismatchError
	require.True(t, errors.As(err, &mm), "got %v", err)
	assert.Equal(t, before, hostile.srv.Server.TokenRequestCount(), "A's token endpoint receives nothing")

	// A flow with no recorded expectation is refused the same way.
	q.Set("iss", meta.Issuer)
	_, err = c.exchangeAuthorizationCode(context.Background(), h, nil, q, oauth.IssuerExpectation{}, verifier)
	require.True(t, errors.As(err, &mm), "no expectation, got %v", err)
	assert.Equal(t, before, hostile.srv.Server.TokenRequestCount())

	// The matching iss is exchanged.
	_, err = c.exchangeAuthorizationCode(context.Background(), h, nil, q, exp, verifier)
	require.NoError(t, err)
	assert.Equal(t, before+1, hostile.srv.Server.TokenRequestCount())
}

// Every mcp-go code exchange goes through exchangeAuthorizationCode, which
// runs the issuer decision; and every login flow arms the callback check
// before the authorization URL leaves the function that built it.
func TestOAuthIssuerChecks_AllExchangePaths(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if strings.HasPrefix(sel.Sel.Name, "ProcessAuthorization") {
					assert.Equal(t, "exchangeAuthorizationCode", fn.Name.Name,
						"%s: %s outside exchangeAuthorizationCode", fset.Position(call.Pos()), sel.Sel.Name)
				}
				return true
			})
		}
	}

	f, err := parser.ParseFile(fset, "connection_oauth.go", nil, 0)
	require.NoError(t, err)
	// First position of each call of interest, per function.
	first := map[string]map[string]token.Pos{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch sel.Sel.Name {
					case "armIssuerCheck", "applyExtraParamsToAuthURL", "recoverStaleDCRClient", "GetAuthorizationURL", "RegisterState":
						if first[fn.Name.Name] == nil {
							first[fn.Name.Name] = map[string]token.Pos{}
						}
						if _, seen := first[fn.Name.Name][sel.Sel.Name]; !seen {
							first[fn.Name.Name][sel.Sel.Name] = call.Pos()
						}
					}
				}
			}
			return true
		})
	}
	// Each flow arms the check right after GetAuthorizationURL and before the
	// URL is used anywhere (the stale-client probe sends it to the provider).
	for _, fn := range []string{"handleOAuthAuthorization", "handleOAuthAuthorizationWithResult", "getAuthorizationURLQuick"} {
		pos := first[fn]
		arm, ok := pos["armIssuerCheck"]
		require.True(t, ok, "%s must arm the issuer check", fn)
		assert.Greater(t, arm, pos["GetAuthorizationURL"], "%s arms after building the URL", fn)
		// No waiter exists unarmed: the flow never calls RegisterState itself;
		// armIssuerCheck registers and arms in one step (RegisterArmedState).
		_, registers := pos["RegisterState"]
		assert.False(t, registers, "%s must not register an unarmed waiter", fn)
		for _, use := range []string{"applyExtraParamsToAuthURL", "recoverStaleDCRClient"} {
			if p, ok := pos[use]; ok {
				assert.Less(t, arm, p, "%s must arm before %s", fn, use)
			}
		}
	}
}

// exchangeForTest runs exchangeAuthorizationCode the way a login does after
// its callback was accepted: the response carries no iss, and the
// expectation is the one armIssuerCheck would record from the handler.
func exchangeForTest(ctx context.Context, t *testing.T, c *Client, h *uptransport.OAuthHandler, cb *oauth.CallbackServer, code, state, verifier string) (bool, error) {
	t.Helper()
	meta, err := h.GetServerMetadata(ctx)
	require.NoError(t, err)
	exp := oauth.IssuerExpectation{Expected: oauth.ExpectedIssuerFromMetadata(oauth.IssuerSourceDiscovered,
		meta.Issuer, meta.AuthorizationResponseIssParameterSupported, "", meta.AuthorizationEndpoint, meta.TokenEndpoint)}
	return c.exchangeAuthorizationCode(ctx, h, cb, url.Values{"code": {code}, "state": {state}}, exp, verifier)
}
