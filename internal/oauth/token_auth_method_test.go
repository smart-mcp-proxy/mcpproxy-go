package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 135 FR-019 / research R1a: mcp-go v1.0 always sent a client secret in
// the token request body. v1.2+ follows the registration response or the
// server metadata (client_secret_basic), and the registered method is not
// persisted by mcpproxy, so a reconnect could switch methods mid-grant. The
// OAuth config pins client_secret_post, as before the bump.
func TestOAuthConfig_PinsClientSecretPost(t *testing.T) {
	resetDiscoveryStateForTest(t)
	var mu sync.Mutex
	var tokenForm url.Values
	var basicSeen bool
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/meta":
			_, _ = w.Write([]byte(`{"issuer":"` + srvURL + `","authorization_endpoint":"` + srvURL + `/authorize","token_endpoint":"` + srvURL + `/token","response_types_supported":["code"],"token_endpoint_auth_methods_supported":["client_secret_basic"]}`))
		case "/token":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			tokenForm, _ = url.ParseQuery(string(b))
			_, _, basicSeen = r.BasicAuth()
			mu.Unlock()
			_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","expires_in":3600}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	name := "auth-method-pin"
	stopCallbackServer(t, name)
	sc := &config.ServerConfig{Name: name, URL: srv.URL + "/mcp", OAuth: &config.OAuthConfig{
		ClientID: "static-id", ClientSecret: "static-secret", AuthServerMetadataURL: srv.URL + "/meta",
	}}
	cfg, err := createOAuthConfigInternal(defaultEnv(), sc, nil, nil, zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, "client_secret_post", cfg.TokenEndpointAuthMethod)

	h := transport.NewOAuthHandler(*cfg)
	ctx := context.Background()
	h.SetExpectedState("st")
	_, err = h.GetAuthorizationURL(ctx, "st", "challenge")
	require.NoError(t, err)
	require.NoError(t, h.ProcessAuthorizationResponse(ctx, "code", "st", "verifier"))

	mu.Lock()
	defer mu.Unlock()
	assert.False(t, basicSeen, "no HTTP Basic, even for a basic-only server (v1.0 behaviour)")
	assert.Equal(t, "static-secret", tokenForm.Get("client_secret"))
	assert.Equal(t, "static-id", tokenForm.Get("client_id"))
}
