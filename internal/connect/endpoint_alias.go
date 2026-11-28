package connect

import (
	"net"
	"net/url"
	"strings"
)

// endpointURLMatches reports whether u, a URL found in a client config,
// addresses the MCP endpoint baseURL of this instance: the endpoint itself,
// with or without a query (the legacy ?apikey= shape), or the same endpoint
// written with another name for the loopback interface.
//
// localhost, 127.0.0.1 and [::1] reach the same listener, so an entry a user
// (or another tool) wrote as http://localhost:8080/mcp is this instance's
// entry when the instance is on http://127.0.0.1:8080/mcp. Without this the
// status row reads "connected to another instance" and Connect writes a second
// entry beside the first.
//
// The alias rule is deliberately narrow: both hosts must be loopback names,
// and scheme, port and path must be equal as written. Userinfo and fragments
// never match. Anything else is somebody else's endpoint.
func endpointURLMatches(u, baseURL string) bool {
	if u == baseURL || strings.HasPrefix(u, baseURL+"?") {
		return true
	}
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
	return isLoopbackHost(got.Hostname()) && isLoopbackHost(want.Hostname())
}

// isLoopbackHost reports whether host names the loopback interface: the name
// localhost or a loopback IP literal. No DNS lookup is made.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
