package core

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// UX-01 r10: the OAuth config's own HTTP client (metadata, registration and
// token requests, which carry the client secret or refresh token) obeys the
// retirement gate like the MCP traffic.
func TestOAuthHTTPClient_IsGatedByRetirement(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)

	for _, protocol := range []string{"http", "sse"} {
		t.Run(protocol, func(t *testing.T) {
			hits.Store(0)
			cfg := &config.ServerConfig{Name: "oauth-" + protocol, Protocol: protocol, URL: srv.URL + "/mcp", Enabled: true}
			c, err := NewClient(cfg.Name, cfg, zap.NewNop(), nil, &config.Config{}, nil, secret.NewResolver())
			require.NoError(t, err)

			retired := false
			c.SetDialGate(func() (func(), bool) {
				if retired {
					return nil, false
				}
				return func() {}, true
			})

			oc := &mcpclient.OAuthConfig{HTTPClient: &http.Client{}}
			c.httpTransportConfig(cfg, oc)

			resp, err := oc.HTTPClient.Post(srv.URL+"/token", "application/x-www-form-urlencoded", nil)
			require.NoError(t, err)
			_ = resp.Body.Close()
			require.EqualValues(t, 1, hits.Load())

			retired = true
			_, err = oc.HTTPClient.Post(srv.URL+"/token", "application/x-www-form-urlencoded", nil)
			require.Error(t, err)
			require.EqualValues(t, 1, hits.Load(), "a retired client sent an OAuth request to its old endpoint")
		})
	}
}
