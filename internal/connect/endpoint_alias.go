package connect

import (
	"net"
	"net/url"
	"strings"
)

// loopbackNameAlias reports whether u, a URL found in a client config,
// addresses the MCP endpoint baseURL of this instance written with the name
// localhost instead of the loopback address (or the other way round). The
// exact address and its legacy ?apikey= form are matched by the callers.
//
// An entry a user (or another tool) wrote as http://localhost:8080/mcp is this
// instance's entry when the instance is on http://127.0.0.1:8080/mcp. Without
// this the status row reads "connected to another instance".
//
// The alias rule is deliberately narrow, because an entry recognised here can
// be removed by Disconnect. Callers that scan a whole file (the entry finders)
// also prefer an exact match anywhere in the file over an alias. One ambiguity
// remains and is accepted: on a machine where another program listens on the
// same port of the OTHER loopback family, the name localhost can reach that
// program instead of this instance.
//
//   - one side is the name localhost and the other is localhost, 127.0.0.1 or
//     ::1. Two different loopback addresses (127.0.0.1 and 127.0.0.2, or
//     127.0.0.1 and ::1) can carry two different listeners and never match;
//   - scheme, port and path are equal as written;
//   - no userinfo, no fragment, and a bracketed host must be an IPv6 literal.
//
// Anything else is somebody else's endpoint.
func loopbackNameAlias(u, baseURL string) bool {
	got, err := url.Parse(u)
	if err != nil {
		return false
	}
	want, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	if got.Opaque != "" || got.User != nil || got.Fragment != "" || strings.Contains(u, "#") {
		return false
	}
	if got.Scheme == "" || !strings.EqualFold(got.Scheme, want.Scheme) {
		return false
	}
	if got.Port() == "" || got.Port() != want.Port() {
		return false
	}
	if got.EscapedPath() != want.EscapedPath() {
		return false
	}
	if !wellFormedHost(got.Host) || !wellFormedHost(want.Host) {
		return false
	}
	a, b := got.Hostname(), want.Hostname()
	switch {
	case isLocalhostName(a):
		return isLocalhostName(b) || isCanonicalLoopback(b)
	case isLocalhostName(b):
		return isCanonicalLoopback(a)
	}
	return false
}

func isLocalhostName(host string) bool { return strings.EqualFold(host, "localhost") }

// isCanonicalLoopback reports whether host is the address localhost resolves
// to: 127.0.0.1 or ::1. Other 127/8 addresses are loopback too, but localhost
// does not name them.
func isCanonicalLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && (ip.Equal(net.IPv4(127, 0, 0, 1)) || ip.Equal(net.IPv6loopback))
}

// wellFormedHost rejects an authority a client's URL parser would refuse but
// url.Parse accepts: brackets around anything that is not an IPv6 literal
// ("[localhost]:8080", "[127.0.0.1]:8080") and an IPv6 literal without
// brackets ("::1:8080").
func wellFormedHost(hostport string) bool {
	if !strings.HasPrefix(hostport, "[") {
		// Without brackets there is at most one colon, the one before the port:
		// "::1:8080" is not an authority a client can dial.
		return strings.Count(hostport, ":") <= 1
	}
	end := strings.IndexByte(hostport, ']')
	if end < 0 {
		return false
	}
	ip := net.ParseIP(hostport[1:end])
	return ip != nil && strings.Contains(hostport[1:end], ":")
}
