package httpx

import "testing"

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"localhost", true},
		{"LocalHost:3000", true},
		{"127.0.0.1", true},
		{"127.0.0.1:8080", true},
		{"127.9.9.9", true},
		{"::1", true},
		{"[::1]", true},
		{"[::1]:8080", true},
		{"example.com", false},
		{"example.com:443", false},
		{"127.0.0.1.evil.com", false},
		{"", false},
		{"10.0.0.1", false},
	}
	for _, tc := range tests {
		if got := IsLoopbackHost(tc.addr); got != tc.want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestHostMatchesTrusted(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		trusted []string
		want    bool
	}{
		{name: "empty list matches nothing", host: "example.com", want: false},
		{name: "exact", host: "example.com", trusted: []string{"example.com"}, want: true},
		{name: "case insensitive", host: "EXAMPLE.com", trusted: []string{"example.COM"}, want: true},
		{name: "entry without port matches any port", host: "example.com:8443", trusted: []string{"example.com"}, want: true},
		{name: "entry with port must match", host: "example.com:8443", trusted: []string{"example.com:443"}, want: false},
		{name: "entry with matching port", host: "example.com:443", trusted: []string{"example.com:443"}, want: true},
		{name: "trailing dot is DNS-equivalent", host: "example.com.", trusted: []string{"example.com"}, want: true},
		{name: "subdomain wildcard matches bare", host: "example.com", trusted: []string{".example.com"}, want: true},
		{name: "subdomain wildcard matches child", host: "ui.example.com", trusted: []string{".example.com"}, want: true},
		{name: "subdomain wildcard rejects suffix confusion", host: "notexample.com", trusted: []string{".example.com"}, want: false},
		{name: "star matches everything", host: "evil.example", trusted: []string{"*"}, want: true},
		{name: "blank entries skipped", host: "example.com", trusted: []string{"", "  "}, want: false},
		{name: "bracketed ipv6", host: "[2001:db8::1]:8080", trusted: []string{"2001:db8::1"}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HostMatchesTrusted(tc.host, tc.trusted); got != tc.want {
				t.Errorf("HostMatchesTrusted(%q, %v) = %v, want %v", tc.host, tc.trusted, got, tc.want)
			}
		})
	}
}

func TestOriginAllowed(t *testing.T) {
	tests := []struct {
		name    string
		origin  string
		trusted []string
		want    bool
	}{
		{name: "loopback ipv4 any port", origin: "http://127.0.0.1:5173", want: true},
		{name: "localhost", origin: "http://localhost:3000", want: true},
		{name: "bracketed ipv6 loopback", origin: "http://[::1]:8080", want: true},
		{name: "https loopback", origin: "https://localhost", want: true},
		{name: "ws scheme", origin: "ws://127.0.0.1:9000", want: true},
		{name: "remote origin untrusted", origin: "https://evil.example", want: false},
		{name: "remote origin trusted", origin: "https://ui.example.com", trusted: []string{"ui.example.com"}, want: true},
		{name: "null", origin: "null", want: false},
		{name: "empty", origin: "", want: false},
		{name: "suffix confusion", origin: "http://127.0.0.1.evil.com", want: false},
		{name: "path is not an origin", origin: "http://localhost:3000/path", want: false},
		{name: "query is not an origin", origin: "http://localhost:3000?a=1", want: false},
		{name: "fragment is not an origin", origin: "http://localhost:3000#x", want: false},
		// An empty delimiter still makes the value something other than a
		// serialized origin, and url.Parse hides it in ForceQuery / a dropped
		// fragment rather than in RawQuery / Fragment.
		{name: "empty query delimiter", origin: "http://localhost:3000?", want: false},
		{name: "empty fragment delimiter", origin: "http://localhost:3000#", want: false},
		{name: "trusted host with empty query delimiter", origin: "https://ui.example.com?", trusted: []string{"ui.example.com"}, want: false},
		{name: "userinfo rejected", origin: "http://user@localhost:3000", want: false},
		{name: "file scheme rejected", origin: "file://localhost", want: false},
		{name: "dangling colon rejected", origin: "http://localhost:", want: false},
		{name: "port out of range", origin: "http://localhost:99999", want: false},
		{name: "port zero", origin: "http://localhost:0", want: false},
		{name: "star trusted echoes allowance", origin: "https://evil.example", trusted: []string{"*"}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := OriginAllowed(tc.origin, tc.trusted); got != tc.want {
				t.Errorf("OriginAllowed(%q, %v) = %v, want %v", tc.origin, tc.trusted, got, tc.want)
			}
		})
	}
}
