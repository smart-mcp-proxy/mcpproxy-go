package server

import (
	"encoding/json"
	"net/http"
	"net/netip"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpx"
)

// Spec 132: opt-in browser login for MCP clients. This file holds only the
// routing switch. With mcp_oauth_enabled off (the default) and the feature
// never used, every pattern below answers exactly as it did before the
// patterns were registered (the previous handler is called). The gate decides
// per request from the live config; nothing is cached.

// oauthLoginExactPatterns and oauthLoginSubtreePatterns are the ONLY patterns
// registered for browser login. `/oauth/` and `/.well-known/` subtrees are
// deliberately not registered: ServeMux would add trailing-slash redirects
// that do not exist today.
var (
	// oauthLoginMCPPatterns fall back to the /mcp/ catch-all chain when off.
	oauthLoginMCPPatterns = []string{"/mcp/oauth", "/mcp/oauth/"}
	// oauthLoginRootPatterns fall back to the root handler when off.
	oauthLoginRootPatterns = []string{
		"/oauth/register",
		"/oauth/authorize",
		"/oauth/token",
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource/mcp/oauth",
		"/.well-known/oauth-protected-resource/mcp/oauth/",
	}
)

// peerIsLoopback reports whether the TCP peer of r is a loopback address.
// Unix-socket / named-pipe peers, empty and malformed addresses are not.
func peerIsLoopback(r *http.Request) bool {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	return ap.Addr().Unmap().IsLoopback()
}

// oauthLoginEverUsedNow reports whether browser login has ever been used on
// this install. It is injectable for tests.
// TODO(Spec 132): read the storage marker `oauth_login_ever_used` once the
// storage slice lands; until then the feature can never have been used.
func (s *Server) oauthLoginEverUsedNow() bool {
	if s.oauthLoginEverUsed != nil {
		return s.oauthLoginEverUsed()
	}
	return false
}

// oauthLoginGate wraps the browser-login handler `served`. prev is the handler
// that answered the same path before this change.
//
//	off, never used -> prev
//	off, used       -> 404
//	on              -> served, only for a loopback peer, a loopback Host and
//	                   the personal edition; otherwise 404
func (s *Server) oauthLoginGate(prev, served http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.liveConfig()
		if cfg == nil || !cfg.MCPOAuthEnabled {
			if s.oauthLoginEverUsedNow() {
				http.NotFound(w, r)
				return
			}
			prev.ServeHTTP(w, r)
			return
		}
		if !peerIsLoopback(r) || !httpx.IsLoopbackHost(r.Host) || config.ServerEditionEnabled(cfg) {
			http.NotFound(w, r)
			return
		}
		served.ServeHTTP(w, r)
	})
}

// oauthLoginNotImplemented is the `served` stub of this slice.
func oauthLoginNotImplemented() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":             "not_implemented",
			"error_description": "browser login (mcp_oauth_enabled) is not functional in this release",
		})
	})
}

// registerOAuthLoginRoutes registers the browser-login patterns on mux.
// mcpHandler is the existing /mcp/ catch-all chain, rootHandler the existing
// "/" handler; each is the off-state fallback of its patterns.
func (s *Server) registerOAuthLoginRoutes(mux *http.ServeMux, mcpHandler, rootHandler http.Handler) {
	served := oauthLoginNotImplemented()
	mcpGate := s.oauthLoginGate(mcpHandler, served)
	for _, p := range oauthLoginMCPPatterns {
		mux.Handle(p, mcpGate)
	}
	rootGate := s.oauthLoginGate(rootHandler, served)
	for _, p := range oauthLoginRootPatterns {
		mux.Handle(p, rootGate)
	}
}
