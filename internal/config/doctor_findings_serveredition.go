//go:build server

package config

import (
	"fmt"
	"net"
)

// serverEditionDoctorFindings are the server-only doctor findings of Spec 107:
// an overridden require_mcp_auth (FR-029), an unset public_url on a
// non-loopback listener (FR-025) and an explicit session_cookie_secure: false
// (FR-026). T074 adds the unknown access-map names.
func serverEditionDoctorFindings(cfg *Config) []string {
	if !ServerEditionEnabled(cfg) {
		return nil
	}
	var out []string
	if RequireMCPAuthOverridden(cfg) {
		out = append(out, MsgRequireMCPAuthOverridden)
	}
	if cfg.ServerEdition.PublicURL == "" && !ListenIsLoopback(cfg.Listen) {
		out = append(out, fmt.Sprintf("server_edition.public_url is unset while listening on %s: set it (or MCPPROXY_PUBLIC_URL) to the origin users reach so the OAuth callback URL and Secure cookie decision do not depend on Host or X-Forwarded-* headers", cfg.Listen))
	}
	if cfg.ServerEdition.SessionCookieSecure == SessionCookieSecureFalse {
		out = append(out, "server_edition.session_cookie_secure is explicitly false: the session cookie is sent over plain http; only a loopback or test deployment should run this way")
	}
	return out
}

// ListenIsLoopback reports whether a listen address binds a loopback
// interface only. An empty or port-only address (":8080") and the unspecified
// addresses (0.0.0.0, [::]) are non-loopback; an unparsable host is treated
// as non-loopback so the warning errs on the side of being shown.
func ListenIsLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		host = listen
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
