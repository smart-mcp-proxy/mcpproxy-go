package oauth

import (
	"context"
	"io"
	"net/http"
	"strings"
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

// Only mcp-go's registration body is touched; any other request reaches the
// server byte for byte, read errors included.
func TestDCRRewrite_LeavesOtherRequestsAlone(t *testing.T) {
	var seen []string
	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			seen = append(seen, "ERR:"+err.Error())
		} else {
			seen = append(seen, string(b))
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	})
	w := NewOAuthTransportWrapper(inner, nil, zap.NewNop())
	w.learnEndpoints("", "https://as.example/token")
	post := func(url, body string) {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		_, err := w.RoundTrip(req)
		require.NoError(t, err)
	}
	reg := `{"client_name":"mcpproxy-go","grant_types":["authorization_code"],"redirect_uris":["http://127.0.0.2:1/cb"],"application_type":"web"}`
	notReg := `{"redirect_uris":["http://127.0.0.2:1/cb"],"application_type":"web"}`

	post("https://as.example/register", reg)
	assert.Contains(t, seen[0], `"application_type":"native"`, "the registration is corrected")
	post("https://as.example/token", reg)
	assert.Equal(t, reg, seen[1], "never a request to the token endpoint")
	post("https://mcp.example/mcp", notReg)
	assert.Equal(t, notReg, seen[2], "a JSON body that is not mcp-go's registration")

	req, _ := http.NewRequest(http.MethodPost, "https://as.example/register", io.NopCloser(&failingReader{data: []byte(`{"client_na`)}))
	req.ContentLength = 200
	req.Header.Set("Content-Type", "application/json")
	_, err := w.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, "ERR:"+io.ErrUnexpectedEOF.Error(), seen[3], "a read error reaches the transport as it was")
}

type failingReader struct {
	data []byte
	done bool
}

func (f *failingReader) Read(p []byte) (int, error) {
	if !f.done {
		f.done = true
		return copy(p, f.data), nil
	}
	return 0, io.ErrUnexpectedEOF
}

func TestDCR_RejectApplicationType_EmptyValueStillPresent(t *testing.T) {
	provider := oauthserver.Start(t, oauthserver.Options{RejectApplicationType: true})
	t.Cleanup(func() { _ = provider.Shutdown() })
	resp, err := http.Post(provider.RegistrationEndpoint, "application/json",
		strings.NewReader(`{"redirect_uris":["http://127.0.0.1:1/cb"],"application_type":""}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "the member's presence is refused, whatever its value")
}
