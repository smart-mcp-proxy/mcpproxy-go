package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"go.uber.org/zap"
)

// isLoopbackHost reports whether addr refers to a loopback interface. addr may
// be a bare host ("localhost", "127.0.0.1", "::1", "[::1]") or a host:port
// pair ("localhost:3000", "127.0.0.1:3000", "[::1]:3000").
func isLoopbackHost(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// addr might be a bare host without a port.
		host = strings.Trim(addr, "[]")
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return ip.IsLoopback()
}

// hostMatchesTrusted reports whether the request Host header matches one of the
// configured trusted_hosts entries. Matching is case-insensitive on the
// hostname. An entry without a port matches that hostname on any port; an
// entry with a port requires the request port to match too. An entry with a
// leading dot is a subdomain wildcard (Django/Vite/webpack convention):
// ".example.com" matches example.com and every subdomain of it. The single
// entry "*" disables Host validation entirely.
func hostMatchesTrusted(host string, trusted []string) bool {
	reqHost, reqPort, err := net.SplitHostPort(host)
	if err != nil {
		reqHost, reqPort = strings.Trim(host, "[]"), ""
	}
	for _, entry := range trusted {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		entryHost, entryPort, err := net.SplitHostPort(entry)
		if err != nil {
			entryHost, entryPort = strings.Trim(entry, "[]"), ""
		}
		if bare, isWildcard := strings.CutPrefix(entryHost, "."); isWildcard {
			if !strings.EqualFold(reqHost, bare) && !hasSuffixFold(reqHost, "."+bare) {
				continue
			}
		} else if !strings.EqualFold(reqHost, entryHost) {
			continue
		}
		if entryPort == "" || entryPort == reqPort {
			return true
		}
	}
	return false
}

// hasSuffixFold reports whether s ends with suffix, case-insensitively.
func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

// originAllowed implements the MCP spec's Origin validation (2025-11-25 basic
// security best practices): an absent Origin header always passes (non-browser
// clients and reverse proxies don't send one), a present Origin must carry a
// loopback or trusted host. "null" and unparseable origins are invalid.
func originAllowed(origin string, trusted []string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return isLoopbackHost(u.Host) || hostMatchesTrusted(u.Host, trusted)
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
