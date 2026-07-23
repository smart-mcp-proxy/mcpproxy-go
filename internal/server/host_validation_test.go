package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// fakeAddr implements net.Addr with a fixed String() value.
type fakeAddr struct{ addr string }

func (f fakeAddr) Network() string { return "tcp" }
func (f fakeAddr) String() string  { return f.addr }

// doHostValidation runs a request with the given Host header and connection
// local address through the host-validation handler and returns the status code.
// localAddr == "" means no LocalAddrContextKey in context (e.g. unix socket
// handled by a custom listener that doesn't set it).
func doHostValidation(t *testing.T, trusted []string, localAddr, host string) int {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := newHostValidationHandler(next, func() []string { return trusted }, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "http://placeholder/mcp", nil)
	req.Host = host
	if localAddr != "" {
		ctx := context.WithValue(req.Context(), http.LocalAddrContextKey, net.Addr(fakeAddr{addr: localAddr}))
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestHostValidation(t *testing.T) {
	cases := []struct {
		name      string
		trusted   []string
		localAddr string
		host      string
		want      int
	}{
		// Baseline DNS-rebinding protection (no trusted hosts configured):
		// identical to mcp-go's built-in behavior.
		{"loopback listener, loopback host", nil, "127.0.0.1:8080", "127.0.0.1:8080", http.StatusOK},
		{"loopback listener, localhost host", nil, "127.0.0.1:8080", "localhost:8080", http.StatusOK},
		{"loopback listener, ipv6 loopback host", nil, "[::1]:8080", "[::1]:8080", http.StatusOK},
		{"loopback listener, external host rejected", nil, "127.0.0.1:8080", "mcp.example.com", http.StatusForbidden},
		{"loopback listener, external host with port rejected", nil, "127.0.0.1:8080", "mcp.example.com:443", http.StatusForbidden},
		{"loopback listener, empty host rejected", nil, "127.0.0.1:8080", "", http.StatusForbidden},

		// Non-loopback listeners are never subject to Host validation
		// (DNS rebinding only targets localhost-bound servers).
		{"public listener, external host allowed", nil, "10.0.0.5:8080", "mcp.example.com", http.StatusOK},

		// No local addr in context (unix socket/tray path): allowed.
		{"no local addr, external host allowed", nil, "", "mcp.example.com", http.StatusOK},

		// trusted_hosts allowlist (GH #898): reverse-proxied public domains.
		{"trusted host allowed", []string{"mcp.example.com"}, "127.0.0.1:8080", "mcp.example.com", http.StatusOK},
		{"trusted host any port allowed", []string{"mcp.example.com"}, "127.0.0.1:8080", "mcp.example.com:443", http.StatusOK},
		{"trusted host case-insensitive", []string{"MCP.Example.COM"}, "127.0.0.1:8080", "mcp.example.com", http.StatusOK},
		{"trusted entry with port, matching", []string{"mcp.example.com:8443"}, "127.0.0.1:8080", "mcp.example.com:8443", http.StatusOK},
		{"trusted entry with port, port mismatch", []string{"mcp.example.com:8443"}, "127.0.0.1:8080", "mcp.example.com:443", http.StatusForbidden},
		{"untrusted host still rejected", []string{"mcp.example.com"}, "127.0.0.1:8080", "evil.example.net", http.StatusForbidden},
		{"loopback still allowed alongside trusted list", []string{"mcp.example.com"}, "127.0.0.1:8080", "localhost:8080", http.StatusOK},

		// "*" wildcard disables Host validation entirely.
		{"wildcard allows anything", []string{"*"}, "127.0.0.1:8080", "anything.example.com", http.StatusOK},

		// Leading-dot entries are subdomain wildcards (Django/Vite/webpack
		// convention): ".example.com" matches the bare domain and any subdomain.
		{"dot wildcard matches bare domain", []string{".example.com"}, "127.0.0.1:8080", "example.com", http.StatusOK},
		{"dot wildcard matches subdomain", []string{".example.com"}, "127.0.0.1:8080", "mcp.example.com", http.StatusOK},
		{"dot wildcard matches nested subdomain", []string{".example.com"}, "127.0.0.1:8080", "a.b.example.com:443", http.StatusOK},
		{"dot wildcard case-insensitive", []string{".Example.COM"}, "127.0.0.1:8080", "MCP.example.com", http.StatusOK},
		{"dot wildcard rejects suffix-collision domain", []string{".example.com"}, "127.0.0.1:8080", "evilexample.com", http.StatusForbidden},
		{"dot wildcard rejects domain as prefix", []string{".example.com"}, "127.0.0.1:8080", "example.com.evil.net", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := doHostValidation(t, tc.trusted, tc.localAddr, tc.host)
			if got != tc.want {
				t.Fatalf("trusted=%v localAddr=%q host=%q: got status %d, want %d",
					tc.trusted, tc.localAddr, tc.host, got, tc.want)
			}
		})
	}
}

// doOriginValidation is doHostValidation with an Origin header attached.
func doOriginValidation(t *testing.T, trusted []string, localAddr, host, origin string) int {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := newHostValidationHandler(next, func() []string { return trusted }, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "http://placeholder/mcp", nil)
	req.Host = host
	req.Header.Set("Origin", origin)
	if localAddr != "" {
		ctx := context.WithValue(req.Context(), http.LocalAddrContextKey, net.Addr(fakeAddr{addr: localAddr}))
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// MCP spec (2025-11-25): "Servers MUST validate the Origin header... If the
// Origin header is present and invalid, servers MUST respond with 403".
// Absent Origin (non-browser clients, reverse proxies) always passes, so this
// cannot re-break proxied deployments.
func TestOriginValidation(t *testing.T) {
	cases := []struct {
		name      string
		trusted   []string
		localAddr string
		host      string
		origin    string
		want      int
	}{
		{"loopback origin allowed", nil, "127.0.0.1:8080", "127.0.0.1:8080", "http://localhost:5173", http.StatusOK},
		{"loopback ip origin allowed", nil, "127.0.0.1:8080", "localhost:8080", "http://127.0.0.1:8080", http.StatusOK},
		{"external origin rejected on loopback listener", nil, "127.0.0.1:8080", "127.0.0.1:8080", "https://evil.example.net", http.StatusForbidden},
		{"null origin rejected", nil, "127.0.0.1:8080", "127.0.0.1:8080", "null", http.StatusForbidden},
		{"malformed origin rejected", nil, "127.0.0.1:8080", "127.0.0.1:8080", "not a url", http.StatusForbidden},
		{"trusted origin allowed", []string{"mcp.example.com"}, "127.0.0.1:8080", "mcp.example.com", "https://mcp.example.com", http.StatusOK},
		{"dot-wildcard origin allowed", []string{".example.com"}, "127.0.0.1:8080", "mcp.example.com", "https://app.example.com", http.StatusOK},
		{"untrusted origin rejected despite trusted host", []string{"mcp.example.com"}, "127.0.0.1:8080", "mcp.example.com", "https://evil.example.net", http.StatusForbidden},
		{"star wildcard allows any origin", []string{"*"}, "127.0.0.1:8080", "127.0.0.1:8080", "https://anything.example.com", http.StatusOK},
		{"non-loopback listener skips origin check", nil, "10.0.0.5:8080", "mcp.example.com", "https://evil.example.net", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := doOriginValidation(t, tc.trusted, tc.localAddr, tc.host, tc.origin)
			if got != tc.want {
				t.Fatalf("trusted=%v localAddr=%q host=%q origin=%q: got status %d, want %d",
					tc.trusted, tc.localAddr, tc.host, tc.origin, got, tc.want)
			}
		})
	}
}

func TestHostValidationNilProvider(t *testing.T) {
	// A nil trusted-hosts provider must behave like an empty list, not panic.
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := newHostValidationHandler(next, nil, zap.NewNop())
	req := httptest.NewRequest(http.MethodPost, "http://placeholder/mcp", nil)
	req.Host = "mcp.example.com"
	ctx := context.WithValue(req.Context(), http.LocalAddrContextKey, net.Addr(fakeAddr{addr: "127.0.0.1:8080"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("nil provider: got %d, want 403", rec.Code)
	}
}
