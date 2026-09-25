package configimport

import (
	"net"
	"net/url"
	"strings"
)

// SkipReasonSelfReference marks a source entry that points back at this
// mcpproxy instance (typically the entry Connect wrote into the client's
// config). Importing it would proxy mcpproxy through itself.
const SkipReasonSelfReference = "self_reference"

// selfMatcher recognizes URLs that address this instance's MCP endpoints
// (/mcp, /mcp/all, /mcp/call, /mcp/code, ...) given its listen address(es).
type selfMatcher struct {
	targets []selfTarget
}

type selfTarget struct {
	port string
	// hosts are the canonical URL hosts (lower-cased names, IPs in net.IP
	// String form) that reach the listener.
	hosts map[string]bool
	// anyLoopback4/anyLoopback6 are set for an all-interfaces listener, which
	// every loopback address of its family (127.0.0.0/8, ::1) reaches.
	anyLoopback4 bool
	anyLoopback6 bool
}

// canonicalHost lower-cases a host name and rewrites an IP literal to its
// canonical form so "0:0:0:0:0:0:0:1" and "::1" compare equal.
func canonicalHost(host string) string {
	host = strings.ToLower(host)
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

// newSelfMatcher builds a matcher from listen addresses such as
// "127.0.0.1:8080", ":8080", "0.0.0.0:8080" or "[::]:8080". Empty or
// unparsable entries are ignored; with none left the matcher matches nothing.
//
// Hosts are matched per address family: a socket bound to 127.0.0.1 is not
// reachable via ::1 or 127.0.0.2, so a server there is another process and must
// stay importable. A wildcard URL host (0.0.0.0, ::) is what Connect writes for
// a wildcard listen, and dialing it reaches the loopback listener of its family.
//
// "localhost" is deliberately treated as reaching any loopback listener even
// though it may resolve to the other family first: the docs tell users to write
// http://localhost:8080/mcp by hand, dialers fall back across families when the
// first refuses, and the only false positive needs a second server on the same
// port in the other loopback family.
func newSelfMatcher(listenAddrs []string) *selfMatcher {
	m := &selfMatcher{}
	for _, addr := range listenAddrs {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil || port == "" || port == "0" {
			continue
		}
		t := selfTarget{port: port, hosts: map[string]bool{}}
		host = canonicalHost(host)
		ip := net.ParseIP(host)
		add := func(hs ...string) {
			for _, h := range hs {
				t.hosts[h] = true
			}
		}
		switch {
		case host == "" || (ip != nil && ip.IsUnspecified()):
			// All interfaces: loopback, the wildcard itself, and every local
			// interface address. A host-less or [::] bind is dual-stack, but an
			// explicit 0.0.0.0 bind is IPv4-only in Go, so IPv6 hosts must not
			// match it.
			v4Only := ip != nil && ip.To4() != nil
			t.anyLoopback4 = true
			t.anyLoopback6 = !v4Only
			add("localhost", "0.0.0.0")
			if !v4Only {
				add("::")
			}
			if addrs, err := net.InterfaceAddrs(); err == nil {
				for _, a := range addrs {
					if ipn, ok := a.(*net.IPNet); ok && (!v4Only || ipn.IP.To4() != nil) {
						add(ipn.IP.String())
					}
				}
			}
		case host == "localhost":
			// Resolves to 127.0.0.1 and/or ::1 depending on the host.
			add("localhost", "127.0.0.1", "::1", "0.0.0.0", "::")
		case ip != nil && ip.IsLoopback() && ip.To4() != nil:
			add(host, "localhost", "0.0.0.0")
		case ip != nil && ip.IsLoopback():
			add(host, "localhost", "::")
		default:
			add(host)
		}
		m.targets = append(m.targets, t)
	}
	return m
}

// matchesURL reports whether raw is an http(s) URL addressing one of this
// instance's MCP endpoints.
func (m *selfMatcher) matchesURL(raw string) bool {
	if m == nil || len(m.targets) == 0 {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	path := strings.TrimSuffix(u.Path, "/")
	if path != "/mcp" && !strings.HasPrefix(path, "/mcp/") {
		return false
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	host := canonicalHost(u.Hostname())
	ip := net.ParseIP(host)
	isLoopback4 := ip != nil && ip.IsLoopback() && ip.To4() != nil
	isLoopback6 := ip != nil && ip.IsLoopback() && ip.To4() == nil
	for _, t := range m.targets {
		if t.port != port {
			continue
		}
		if t.hosts[host] || (isLoopback4 && t.anyLoopback4) || (isLoopback6 && t.anyLoopback6) {
			return true
		}
	}
	return false
}

// matchesParsed reports whether a parsed source entry points at this instance,
// either directly via its URL or through a stdio bridge (e.g. mcp-remote) whose
// args carry our endpoint.
func (m *selfMatcher) matchesParsed(parsed *ParsedServer) bool {
	if m == nil || len(m.targets) == 0 || parsed == nil {
		return false
	}
	if u, ok := parsed.Fields["url"].(string); ok && m.matchesURL(u) {
		return true
	}
	if args, ok := parsed.Fields["args"].([]string); ok {
		for _, a := range args {
			if m.matchesURL(a) {
				return true
			}
		}
	}
	return false
}
