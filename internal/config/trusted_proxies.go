package config

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Spec 107 FR-027: `trusted_proxies` is the edition-neutral, top-level,
// hot-reloadable list of CIDRs or IP addresses whose X-Forwarded-For /
// X-Real-IP / X-Forwarded-Proto / X-Forwarded-Host headers are believed. The
// default (empty) trusts nobody, so a direct client can never spoof the
// session IP, the callback scheme, the Secure cookie decision or the audit
// IP. ForwardedHeaders is the ONE reader every consumer goes through.

// ForwardedInfo is what ForwardedHeaders resolves for one request.
type ForwardedInfo struct {
	// Scheme is "http" or "https": X-Forwarded-Proto from a trusted peer,
	// otherwise the listener's own TLS state.
	Scheme string
	// Host is X-Forwarded-Host from a trusted peer, otherwise r.Host.
	Host string
	// ClientIP is the right-most X-Forwarded-For hop that is not itself a
	// trusted proxy (then X-Real-IP) from a trusted peer, otherwise the
	// RemoteAddr host. An unparsable RemoteAddr (a socket peer) is returned
	// raw, as before.
	ClientIP string
}

// TrustedProxiesProvider yields the LIVE trusted_proxies list; consumers
// evaluate it per request so a hot reload takes effect without a restart.
type TrustedProxiesProvider func() []string

// parseTrustedProxy parses one entry as a CIDR or a bare IP (a /32 or /128).
// Whitespace around the entry is tolerated.
func parseTrustedProxy(entry string) (*net.IPNet, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil, false
	}
	if _, ipnet, err := net.ParseCIDR(entry); err == nil {
		return ipnet, true
	}
	ip := net.ParseIP(entry)
	if ip == nil {
		return nil, false
	}
	bits := 32
	if ip.To4() == nil {
		bits = 128
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, true
}

// peerIP extracts the IP of a RemoteAddr-shaped string ("host:port", a bare
// IP, or a bracketed IPv6). It returns nil for anything that is not an IP
// literal — a unix-socket peer, a hostname, an empty string — which is never
// trusted.
func peerIP(remoteAddr string) net.IP {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	return net.ParseIP(host)
}

// ipInTrusted reports whether ip is covered by any parsable entry. An
// unparsable entry is skipped (it never grants trust) and does not disable
// its neighbours; validateTrustedProxies is where it is refused.
func ipInTrusted(ip net.IP, trusted []string) bool {
	if ip == nil {
		return false
	}
	for _, entry := range trusted {
		if ipnet, ok := parseTrustedProxy(entry); ok && ipnet.Contains(ip) {
			return true
		}
	}
	return false
}

// IsTrustedProxy reports whether remoteAddr (RemoteAddr form) is inside the
// trusted list. Unparsable peers and an empty list are never trusted.
func IsTrustedProxy(remoteAddr string, trusted []string) bool {
	return ipInTrusted(peerIP(remoteAddr), trusted)
}

// ForwardedHeaders resolves the client-facing scheme, host and client IP of
// r, honouring the X-Forwarded-* / X-Real-IP headers only when the direct
// peer is a trusted proxy.
func ForwardedHeaders(r *http.Request, trusted []string) ForwardedInfo {
	info := ForwardedInfo{Scheme: "http", Host: r.Host}
	if r.TLS != nil {
		info.Scheme = "https"
	}
	if ip := peerIP(r.RemoteAddr); ip != nil {
		info.ClientIP = ip.String()
	} else {
		info.ClientIP = r.RemoteAddr
	}
	if !IsTrustedProxy(r.RemoteAddr, trusted) {
		return info
	}

	switch proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); proto {
	case "http", "https":
		info.Scheme = proto
	}
	if host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); host != "" {
		info.Host = host
	}
	if xff := r.Header.Get("X-Forwarded-For"); strings.TrimSpace(xff) != "" {
		hops := strings.Split(xff, ",")
		// Walk right to left: the first hop that is not a trusted proxy is
		// the client. When every hop is trusted the left-most one is the
		// best available client address.
		chosen := ""
		for i := len(hops) - 1; i >= 0; i-- {
			hop := strings.TrimSpace(hops[i])
			if hop == "" {
				continue
			}
			chosen = hop
			if !ipInTrusted(net.ParseIP(hop), trusted) {
				break
			}
		}
		if chosen != "" {
			info.ClientIP = chosen
			return info
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		info.ClientIP = xri
	}
	return info
}

// validateTrustedProxies refuses every entry that is neither a CIDR nor an IP
// address, with the message text of contracts/config-keys.md, so boot, PATCH
// and /config/apply say the same thing (FR-039).
func validateTrustedProxies(cfg *Config) []ValidationError {
	if cfg == nil {
		return nil
	}
	var errs []ValidationError
	for i, entry := range cfg.TrustedProxies {
		if _, ok := parseTrustedProxy(entry); !ok {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("trusted_proxies[%d]", i),
				Message: fmt.Sprintf("trusted_proxies[%d] %q is not a valid CIDR or IP address", i, entry),
			})
		}
	}
	return errs
}

// parseTrustedProxiesEnv splits the MCPPROXY_TRUSTED_PROXIES comma list,
// trimming entries and dropping empty items. Entries are kept verbatim so an
// invalid one is refused by validateTrustedProxies exactly like a file value.
func parseTrustedProxiesEnv(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
