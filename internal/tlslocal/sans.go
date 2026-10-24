package tlslocal

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// Names every generated server certificate carries, with or without
// tls.extra_sans.
var (
	defaultDNSNames = []string{"localhost"}
	defaultIPs      = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
)

// ParseSANs validates the tls.extra_sans entries and splits them into DNS
// names and IP addresses. An entry is an IP literal ("192.168.1.10", "fd7a::1")
// or a host name made of letters, digits, '-' and '_' separated by dots; it is
// not a URL, has no port and no wildcard. DNS names are lower-cased, and an
// entry that repeats another one, or one of the names every certificate
// already carries (localhost, 127.0.0.1, ::1), is dropped. The error names the
// zero-based index and the offending value.
func ParseSANs(entries []string) (dnsNames []string, ips []net.IP, err error) {
	seen := map[string]struct{}{}
	for _, d := range defaultDNSNames {
		seen["dns:"+d] = struct{}{}
	}
	for _, ip := range defaultIPs {
		seen["ip:"+ip.String()] = struct{}{}
	}

	for i, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return nil, nil, fmt.Errorf("extra_sans[%d] is empty", i)
		}
		if ip := net.ParseIP(entry); ip != nil {
			if _, dup := seen["ip:"+ip.String()]; !dup {
				seen["ip:"+ip.String()] = struct{}{}
				ips = append(ips, ip)
			}
			continue
		}
		name := strings.ToLower(entry)
		if err := checkDNSName(name); err != nil {
			return nil, nil, fmt.Errorf("extra_sans[%d] %q: %w", i, raw, err)
		}
		if _, dup := seen["dns:"+name]; !dup {
			seen["dns:"+name] = struct{}{}
			dnsNames = append(dnsNames, name)
		}
	}
	return dnsNames, ips, nil
}

// checkDNSName accepts a lower-case host name: dot-separated labels of 1-63
// characters from a-z, 0-9, '-' and '_' (Docker and Compose service names may
// contain an underscore), no label starting or ending with '-', 253 characters
// at most. Nothing else is accepted, which rules out wildcards, URLs, ports,
// brackets, trailing dots and non-ASCII names (use the punycode form).
func checkDNSName(name string) error {
	if strings.Contains(name, "*") {
		return fmt.Errorf("wildcard names are not supported; list each name")
	}
	if len(name) > 253 {
		return fmt.Errorf("longer than 253 characters")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return fmt.Errorf("not a valid host name or IP address (empty label)")
		}
		if len(label) > 63 {
			return fmt.Errorf("label %q is longer than 63 characters", label)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("not a valid host name (label %q starts or ends with '-')", label)
		}
		for _, c := range []byte(label) {
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			default:
				return fmt.Errorf("not a valid host name or IP address (a name is letters, digits, '-' and '_' separated by dots, with no scheme, port or path)")
			}
		}
	}
	return nil
}

// sanSet returns the DNS names and IP addresses as one comparable set.
func sanSet(dnsNames []string, ips []net.IP) []string {
	set := make([]string, 0, len(dnsNames)+len(ips))
	for _, d := range dnsNames {
		set = append(set, "dns:"+strings.ToLower(d))
	}
	for _, ip := range ips {
		set = append(set, "ip:"+ip.String())
	}
	sort.Strings(set)
	return set
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
