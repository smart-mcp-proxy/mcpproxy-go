//go:build server

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// A server-edition binary never serves browser login, whatever the config:
// no server_edition block, enabled=false and enabled=true all answer 404.
func TestOAuthLogin_Gate_ServerEditionBuild_404(t *testing.T) {
	cases := map[string]func(*config.Config){
		"block absent":  func(c *config.Config) { c.ServerEdition = nil },
		"enabled=false": func(c *config.Config) { c.ServerEdition = &config.ServerEditionConfig{Enabled: false} },
		"enabled=true":  func(c *config.Config) { c.ServerEdition = &config.ServerEditionConfig{Enabled: true} },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newOAuthLoginTestServer(t, func(c *config.Config) {
				c.MCPOAuthEnabled = true
				edit(c)
			})
			mux := oauthLoginTestMux(srv, true)
			for _, path := range oauthLoginGatedPaths {
				req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
				req.RemoteAddr = "127.0.0.1:4000"
				req.Host = "127.0.0.1:8080"
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				assert.Equal(t, http.StatusNotFound, rec.Code, path)
			}
		})
	}
}

// Switch off and never used: the server build still delegates to the previous
// handler (unchanged behaviour).
func TestOAuthLogin_Gate_ServerEditionBuild_OffDelegates(t *testing.T) {
	srv := newOAuthLoginTestServer(t, nil)
	mux := oauthLoginTestMux(srv, true)
	req := httptest.NewRequest(http.MethodGet, "/mcp/oauth/cursor", http.NoBody)
	req.RemoteAddr = "127.0.0.1:4000"
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "mcp-chain GET /mcp/oauth/cursor")
}
