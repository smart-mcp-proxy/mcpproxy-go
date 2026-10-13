package registries

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
)

// registryResolveHost resolves a hostname to its IPs. A package var so tests can
// simulate a hostname resolving into a blocked range without real DNS.
var registryResolveHost = func(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, len(addrs))
	for i := range addrs {
		ips[i] = addrs[i].IP
	}
	return ips, nil
}

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
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return true
	}
	// IPv6 transition addresses (6to4, NAT64, Teredo, IPv4-compatible) embed an
	// IPv4 address none of the checks above look at. Unwrap and re-check it so
	// [2002:a9fe:a9fe::1] is treated as 169.254.169.254.
	for _, inner := range embeddedIPv4(ip) {
		if isBlockedIP(inner) {
			return true
		}
	}
	return false
}

// embeddedIPv4 returns the IPv4 addresses carried inside an IPv6 transition
// address, or nil when it carries none. Teredo yields two: the relay server and
// the (obfuscated) client.
func embeddedIPv4(ip net.IP) []net.IP {
	v6 := ip.To16()
	if v6 == nil || ip.To4() != nil {
		return nil
	}
	switch {
	// 6to4 — RFC 3056, 2002::/16, IPv4 in bytes 2-6.
	case v6[0] == 0x20 && v6[1] == 0x02:
		return []net.IP{net.IPv4(v6[2], v6[3], v6[4], v6[5])}
	// NAT64 well-known prefix — RFC 6052, 64:ff9b::/96, IPv4 in the low 32 bits.
	case v6[0] == 0x00 && v6[1] == 0x64 && v6[2] == 0xff && v6[3] == 0x9b && allZeros(v6[4:12]):
		return []net.IP{net.IPv4(v6[12], v6[13], v6[14], v6[15])}
	// NAT64 local-use prefix — RFC 8215, 64:ff9b:1::/48. Embedded IPv4 position
	// depends on the operator's prefix length, so block the whole range.
	case v6[0] == 0x00 && v6[1] == 0x64 && v6[2] == 0xff && v6[3] == 0x9b && v6[4] == 0x00 && v6[5] == 0x01:
		return []net.IP{net.IPv4zero}
	// Teredo — RFC 4380, 2001::/32. Server IPv4 in bytes 4-8, client IPv4 in
	// bytes 12-16 obfuscated by XOR with 0xff.
	case v6[0] == 0x20 && v6[1] == 0x01 && v6[2] == 0x00 && v6[3] == 0x00:
		return []net.IP{
			net.IPv4(v6[4], v6[5], v6[6], v6[7]),
			net.IPv4(v6[12]^0xff, v6[13]^0xff, v6[14]^0xff, v6[15]^0xff),
		}
	// IPv4-compatible — deprecated ::a.b.c.d, not unwrapped by net.IP.To4.
	case allZeros(v6[0:12]):
		return []net.IP{net.IPv4(v6[12], v6[13], v6[14], v6[15])}
	}
	return nil
}

func allZeros(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
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

// guardRegistryTargetHost is the application-layer SSRF guard: it resolves the
// registry TARGET host and rejects the fetch if ANY resolved address is in a
// blocked range. Unlike registryDialControl, it runs BEFORE the request and is
// independent of the transport, so it holds even when an HTTP(S)_PROXY is set —
// in which case the dialer connects to the proxy and the dial-time Control only
// ever sees the proxy's IP, never the real target (the proxy resolves the host
// itself). The dial-time guard remains as defense-in-depth for the direct
// (no-proxy) path. Caveats: a literal-IP host is already covered by
// validateRegistryURL; a DNS lookup failure is left to the request to surface
// (fail-open on resolver error so a flaky resolver does not break every fetch —
// the dial guard still covers the no-proxy path). Relaxed by the
// allow_private_registry_fetch opt-in.
func guardRegistryTargetHost(ctx context.Context, reqURL string) error {
	if registryAllowPrivateFetch.Load() {
		return nil
	}
	u, err := url.Parse(reqURL)
	if err != nil {
		return fmt.Errorf("invalid request URL: %w", err)
	}
	host := u.Hostname() // strips the port and unbrackets an IPv6 literal
	if host == "" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		// Literal IP — already validated pre-flight; re-check defensively.
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: %s", ErrBlockedRegistryHost, ip)
		}
		return nil
	}
	ips, err := registryResolveHost(ctx, host)
	if err != nil {
		// Resolution failed — let the request itself surface the failure.
		return nil //nolint:nilerr // fail-open on lookup error; dial guard covers no-proxy
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: host %q resolves to %s", ErrBlockedRegistryHost, host, ip)
		}
	}
	return nil
}
