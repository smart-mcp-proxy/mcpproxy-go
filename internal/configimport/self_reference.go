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
	// hosts are the exact (lower-cased) host names/IPs that reach the listener.
	hosts map[string]bool
	// loopback is true when the listener accepts loopback connections, so any
	// loopback alias (localhost, 127.x, ::1) reaches it.
	loopback bool
}

// newSelfMatcher builds a matcher from listen addresses such as
// "127.0.0.1:8080", ":8080", "0.0.0.0:8080" or "[::]:8080". Empty or
// unparsable entries are ignored; with none left the matcher matches nothing.
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
		host = strings.ToLower(host)
		ip := net.ParseIP(host)
		switch {
		case host == "" || (ip != nil && ip.IsUnspecified()):
			// All interfaces: loopback plus every local interface address.
			t.loopback = true
			if addrs, err := net.InterfaceAddrs(); err == nil {
				for _, a := range addrs {
					if ipn, ok := a.(*net.IPNet); ok {
						t.hosts[ipn.IP.String()] = true
					}
				}
			}
		case host == "localhost" || (ip != nil && ip.IsLoopback()):
			t.loopback = true
		default:
			t.hosts[host] = true
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
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	isLoopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if ip != nil {
		host = ip.String() // canonical form so "::1" variants compare equal
	}
	for _, t := range m.targets {
		if t.port != port {
			continue
		}
		if (isLoopback && t.loopback) || t.hosts[host] {
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
