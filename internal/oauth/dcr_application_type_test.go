package oauth

import (
	"context"
	"net/http"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// registerThroughWrapper runs mcp-go's real DCR request through mcpproxy's
// transport wrapper against the in-repo OAuth test server.
func registerThroughWrapper(t *testing.T, opts oauthserver.Options, redirectURI string) (*oauthserver.ServerResult, error) {
	t.Helper()
	provider := oauthserver.Start(t, opts)
	t.Cleanup(func() { _ = provider.Shutdown() })
	h := transport.NewOAuthHandler(transport.OAuthConfig{
		RedirectURI:           redirectURI,
		PKCEEnabled:           true,
		AuthServerMetadataURL: provider.IssuerURL + "/.well-known/oauth-authorization-server",
		HTTPClient:            newOAuthHTTPClientForTest(),
	})
	return provider, h.RegisterClient(context.Background(), "mcpproxy-go")
}

func newOAuthHTTPClientForTest() *http.Client {
	return &http.Client{Transport: NewOAuthTransportWrapper(nil, nil, zap.NewNop())}
}

// Spec 135 FR-016 / research R4: DCR sends application_type "native" for an
// http redirect URI on a loopback host (all of 127.0.0.0/8, ::1, localhost)
// and "web" otherwise. mcp-go v1.6 recognises only 127.0.0.1, ::1 and
// localhost, so the wrapper rewrites the field.
func TestDCR_ApplicationType(t *testing.T) {
	cases := []struct {
		name, redirect, want string
	}{
		{"IPv4", "http://127.0.0.1:43123/oauth/callback", "native"},
		{"IPv4OtherLoopback", "http://127.0.0.2:43123/oauth/callback", "native"},
		{"IPv6", "http://[::1]:43123/oauth/callback", "native"},
		{"Localhost", "http://localhost:43123/oauth/callback", "native"},
		{"LocalhostUpper", "http://LOCALHOST:43123/oauth/callback", "native"},
		{"Remote", "https://proxy.example.com/oauth/callback/github", "web"},
		{"RemoteHTTP", "http://10.0.0.5:8080/oauth/callback/github", "web"},
		{"HTTPSLoopback", "https://127.0.0.1:8443/oauth/callback", "web"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := registerThroughWrapper(t, oauthserver.Options{}, tc.redirect)
			require.NoError(t, err)
			reqs := provider.Server.RegistrationRequests()
			require.Len(t, reqs, 1)
			assert.Equal(t, tc.want, reqs[0].ApplicationType)
			assert.Equal(t, []string{tc.redirect}, reqs[0].RedirectURIs, "nothing else in the body changes")
		})
	}
}

// FR-016: a server that refuses application_type is not retried without it.
func TestDCR_RejectApplicationType_NoRetry(t *testing.T) {
	provider, err := registerThroughWrapper(t, oauthserver.Options{RejectApplicationType: true}, "http://127.0.0.1:43124/oauth/callback")
	require.Error(t, err)
	assert.Equal(t, int64(1), provider.Server.RegistrationCount(), "exactly one registration request")
	reqs := provider.Server.RegistrationRequests()
	require.Len(t, reqs, 1)
	assert.Equal(t, "native", reqs[0].ApplicationType)
}

func TestApplicationTypeFor(t *testing.T) {
	for redirect, want := range map[string]string{
		"http://127.9.9.9/cb":   "native",
		"http://[::1]/cb":       "native",
		"http://localhost./cb":  "web", // not what config.IsLoopbackHost accepts
		"https://localhost/cb":  "web",
		"http://example.com/cb": "web",
		"":                      "web",
		"::not a url":           "web",
	} {
		assert.Equal(t, want, applicationTypeFor(redirect), redirect)
	}
}
