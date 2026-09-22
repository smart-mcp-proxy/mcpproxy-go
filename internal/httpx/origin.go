// Package httpx holds HTTP primitives shared by the MCP surface
// (internal/server) and the REST/SSE surface (internal/httpapi). It is a leaf
// package: it imports nothing from the rest of the tree, so both surfaces can
// depend on it without an import cycle (internal/server already imports
// internal/httpapi).
package httpx

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// IsLoopbackHost reports whether addr refers to a loopback interface. addr may
// be a bare host ("localhost", "127.0.0.1", "::1", "[::1]") or a host:port
// pair ("localhost:3000", "127.0.0.1:3000", "[::1]:3000").
func IsLoopbackHost(addr string) bool {
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

// HostMatchesTrusted reports whether host matches one of the configured
// trusted_hosts entries. Matching is case-insensitive on the hostname. An
// entry without a port matches that hostname on any port; an entry with a port
// requires the request port to match too. An entry with a leading dot is a
// subdomain wildcard (Django/Vite/webpack convention): ".example.com" matches
// example.com and every subdomain of it. The single entry "*" disables
// validation entirely.
func HostMatchesTrusted(host string, trusted []string) bool {
	reqHost, reqPort, err := net.SplitHostPort(host)
	if err != nil {
		reqHost, reqPort = strings.Trim(host, "[]"), ""
	}
	// A trailing dot ("example.com.") is DNS-equivalent to the undotted name.
	reqHost = strings.TrimSuffix(reqHost, ".")
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

// OriginAllowed implements the MCP spec's Origin validation (2025-11-25 basic
// security best practices): a present Origin must be a well-formed serialized
// origin — scheme://host[:port] over http(s)/ws(s), no userinfo, path, query,
// or fragment — whose host is loopback or trusted. "null", unparseable, and
// non-origin-shaped values are invalid. Absence is handled by the caller
// (non-browser clients don't send Origin at all).
func OriginAllowed(origin string, trusted []string) bool {
	// A query or fragment delimiter disqualifies the value even when what
	// follows it is empty: url.Parse records a bare "?" in ForceQuery (not
	// RawQuery) and drops a bare "#" entirely, so the struct checks below
	// cannot see either one.
	if strings.ContainsAny(origin, "?#") {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ws", "wss":
	default:
		return false
	}
	// url.Parse tolerates a dangling colon ("host:") and any digit run as a
	// port; a serialized origin's port must be a real one.
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return IsLoopbackHost(u.Host) || HostMatchesTrusted(u.Host, trusted)
}
