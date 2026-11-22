//go:build !server

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Personal edition only: a server-edition binary never serves browser login.

func TestOAuthLogin_Gate_On(t *testing.T) {
	srv := newOAuthLoginTestServer(t, func(c *config.Config) { c.MCPOAuthEnabled = true })
	mux := oauthLoginTestMux(srv, true)

	for _, path := range oauthLoginGatedPaths {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req.RemoteAddr = "127.0.0.1:4000"
		req.Host = "127.0.0.1:8080"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotImplemented, rec.Code, "loopback peer+host "+path)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/json", path)

		req = httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req.RemoteAddr = "[::1]:4000"
		req.Host = "localhost:8080"
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotImplemented, rec.Code, "ipv6 loopback peer "+path)

		req = httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req.RemoteAddr = "192.0.2.1:4000"
		req.Host = "127.0.0.1:8080"
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code, "non-loopback peer "+path)

		req = httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req.RemoteAddr = "127.0.0.1:4000"
		req.Host = "example.com"
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code, "non-loopback host "+path)
	}
}

// The gate reads the live config on every request: flipping the switch needs
// no restart and leaves no cached state.
func TestOAuthLogin_Gate_FollowsLiveConfig(t *testing.T) {
	srv := newOAuthLoginTestServer(t, nil)
	mux := oauthLoginTestMux(srv, true)
	get := func() int {
		req := httptest.NewRequest(http.MethodGet, "/oauth/token", http.NoBody)
		req.RemoteAddr = "127.0.0.1:4000"
		req.Host = "127.0.0.1:8080"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusNotFound, get(), "off: previous behaviour (root handler 404)")

	cfg := srv.runtime.Config()
	require.NotNil(t, cfg)
	on := *cfg
	on.MCPOAuthEnabled = true
	srv.runtime.UpdateConfig(&on, "")
	assert.Equal(t, http.StatusNotImplemented, get(), "on")
}
