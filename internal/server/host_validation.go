package server

import (
	"fmt"
	"net"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpx"

	"go.uber.org/zap"
)

// isLoopbackHost delegates to httpx.IsLoopbackHost. The MCP surface and the
// REST/SSE surface must share one origin policy, so the implementation lives in
// the leaf package internal/httpx; these thin wrappers keep this file readable.
func isLoopbackHost(addr string) bool { return httpx.IsLoopbackHost(addr) }

// hostMatchesTrusted delegates to httpx.HostMatchesTrusted.
func hostMatchesTrusted(host string, trusted []string) bool {
	return httpx.HostMatchesTrusted(host, trusted)
}

// originAllowed delegates to httpx.OriginAllowed.
func originAllowed(origin string, trusted []string) bool {
	return httpx.OriginAllowed(origin, trusted)
}

// newHostValidationHandler applies DNS-rebinding protection with a
// user-configurable allowlist (GH #898). A request that arrives on a loopback
// connection must carry a loopback Host header — otherwise a malicious website
// could rebind its own domain to 127.0.0.1 and drive a victim's browser into a
// local MCP server. Reverse-proxied deployments (nginx forwarding
// mcp.example.com → 127.0.0.1) legitimately hit this guard, so hosts listed in
// config trusted_hosts are also accepted.
//
// This replaces mcp-go's built-in check (disabled via
// WithDisableLocalhostProtection) with identical default semantics: requests on
// non-loopback local addresses — or with no local address at all (unix
// socket/tray) — are never rejected. trustedHosts is read per request so config
// hot-reload takes effect without a restart.
func newHostValidationHandler(next http.Handler, trustedHosts func() []string, logger *zap.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localAddr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
		if !ok || localAddr == nil || !isLoopbackHost(localAddr.String()) {
			next.ServeHTTP(w, r)
			return
		}
		var trusted []string
		if trustedHosts != nil {
			trusted = trustedHosts()
		}
		if !isLoopbackHost(r.Host) && !hostMatchesTrusted(r.Host, trusted) {
			logger.Warn("Rejected MCP request with untrusted Host header (DNS-rebinding protection)",
				zap.String("host", r.Host),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("hint", "if this is a reverse-proxy deployment, add the public domain to trusted_hosts in mcp_config.json"))
			http.Error(w, fmt.Sprintf("Forbidden: invalid Host header %q — add this host to trusted_hosts in mcp_config.json to allow reverse-proxy access", r.Host), http.StatusForbidden)
			return
		}
		// MCP spec: reject only when Origin is present AND invalid, so
		// header-less non-browser clients and proxied traffic pass untouched.
		if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(origin, trusted) {
			logger.Warn("Rejected MCP request with untrusted Origin header (DNS-rebinding protection)",
				zap.String("origin", origin),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("hint", "if this browser origin is legitimate, add its host to trusted_hosts in mcp_config.json"))
			http.Error(w, fmt.Sprintf("Forbidden: invalid Origin header %q — add this host to trusted_hosts in mcp_config.json if the origin is legitimate", origin), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostValidationMiddleware wraps an MCP endpoint handler with
// newHostValidationHandler, sourcing trusted_hosts live from runtime config.
func (s *Server) hostValidationMiddleware(next http.Handler) http.Handler {
	return newHostValidationHandler(next, func() []string {
		if cfg := s.runtime.Config(); cfg != nil {
			return cfg.TrustedHosts
		}
		return nil
	}, s.logger)
}
