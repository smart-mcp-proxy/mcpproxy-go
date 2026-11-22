//go:build server

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Server edition never serves browser login, even with the switch on.
func TestOAuthLogin_Gate_ServerEdition_404(t *testing.T) {
	srv := newOAuthLoginTestServer(t, func(c *config.Config) {
		c.MCPOAuthEnabled = true
		c.ServerEdition = &config.ServerEditionConfig{Enabled: true}
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
}
