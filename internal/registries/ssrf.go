package registries

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
)

// ErrBlockedRegistryHost is returned when a registry URL targets — or resolves
// to — a non-routable / internal address. It bounds CWE-918 (request forgery):
// a malicious or typo'd registry source must never let the daemon fetch from
// loopback, RFC1918/CGNAT private space, link-local (incl. the
// 169.254.169.254 cloud-metadata endpoint), or other internal ranges.
var ErrBlockedRegistryHost = errors.New("registry host is not allowed (internal/non-routable address)")

// registryAllowPrivateFetch relaxes the SSRF guard so a fetch may reach
// loopback/private targets. It is OFF by default (secure) and set from the
// user's `allow_private_registry_fetch` config flag by SetRegistriesFromConfig
// — the opt-in allow-policy for operators who run a trusted registry mirror on
// an internal/private address. atomic.Bool because it is written on config
// (re)load while concurrent fetches read it on the dial path. The registries
// test binary also flips it on (httptest servers bind 127.0.0.1).
var registryAllowPrivateFetch atomic.Bool

// testForceAllowPrivate pins the guard open regardless of config. It is set ONLY
// by the registries test binary (httptest servers bind loopback) so a fetch
// still works across a SetRegistriesFromConfig(defaultConfig) call that would
// otherwise reset the flag to false. Always false in production builds.
var testForceAllowPrivate atomic.Bool

// SetAllowPrivateRegistryFetch sets the SSRF allow-policy from config. Exposed so
// SetRegistriesFromConfig (and only it) can propagate the user's flag. The
// test-force override keeps loopback fetches working in the test binary.
func SetAllowPrivateRegistryFetch(allow bool) {
	registryAllowPrivateFetch.Store(allow || testForceAllowPrivate.Load())
}

// isBlockedIP reports whether ip falls in a range a registry fetch must never
// reach. This is the single predicate behind both the pre-flight URL check and
// the dial-time Control guard, so the policy lives in exactly one place.
//
// Blocked: loopback, RFC1918 private (10/8, 172.16/12, 192.168/16), IPv6
// unique-local (fc00::/7, via IsPrivate), RFC6598 CGNAT (100.64/10), link-local
// unicast (169.254/16, fe80::/10 — covers the cloud metadata endpoint),
// link-local & interface-local multicast, any other multicast, and the
// unspecified address. A nil/unparseable IP fails closed (blocked).
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true // fail closed: an address we can't reason about is not safe
	}
	// RFC6598 carrier-grade NAT (100.64.0.0/10) is not covered by IsPrivate.
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// hostLiteralBlocked returns a non-nil error if host is a LITERAL IP in a blocked
// range. host may be a bare host, host:port, or a bracketed IPv6 literal. A
// hostname (not an IP literal) returns nil here — hostnames are validated
// authoritatively at dial time (registryDialControl), which also defeats
// DNS-rebinding TOCTOU. allowPrivate (the config opt-in) short-circuits to nil.
func hostLiteralBlocked(host string, allowPrivate bool) error {
	if allowPrivate {
		return nil
	}
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	ip := net.ParseIP(h)
	if ip == nil {
		return nil // not a literal IP — defer to the dial-time guard
	}
	if isBlockedIP(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedRegistryHost, ip)
	}
	return nil
}

// ValidateRegistrySourceURL is the add-source / edit-source fail-fast: it rejects
// a user-supplied registry URL whose host is a literal IP in a blocked range, so
// `registry add-source https://169.254.169.254/...` is refused up front with a
// clear error instead of failing later at fetch time. It performs NO DNS lookup
// (keeping add/edit pure and offline) — hostname sources pass here and are
// guarded authoritatively when the daemon actually dials them.
func ValidateRegistrySourceURL(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid registry URL: %w", err)
	}
	return hostLiteralBlocked(u.Host, registryAllowPrivateFetch.Load())
}

// registryDialControl is the authoritative SSRF guard. It is wired as the
// net.Dialer Control hook on the shared registry HTTP client, so it runs with
// the ACTUAL resolved address the connection is about to dial — after DNS
// resolution and before connect. This catches hostnames that resolve into
// blocked ranges and closes the DNS-rebinding TOCTOU window that a parse-time
// check alone leaves open.
func registryDialControl(_, address string, _ syscall.RawConn) error {
	if registryAllowPrivateFetch.Load() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if isBlockedIP(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedRegistryHost, address)
	}
	return nil
}
