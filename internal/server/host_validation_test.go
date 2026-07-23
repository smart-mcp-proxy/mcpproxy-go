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
