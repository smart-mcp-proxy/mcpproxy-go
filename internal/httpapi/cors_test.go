package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// corsController supplies both config doors the CORS middleware depends on:
// GetCurrentConfig (read by apiKeyAuthMiddleware) and GetConfig (read by
// trustedHostsProvider).
type corsController struct {
	baseController
	apiKey       string
	trustedHosts []string
}

func (m *corsController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey}
}

func (m *corsController) GetConfig() (*config.Config, error) {
	return &config.Config{APIKey: m.apiKey, TrustedHosts: m.trustedHosts}, nil
}

const corsTestAPIKey = "test-api-key"

func newCORSServer(trustedHosts []string) *Server {
	return NewServer(&corsController{apiKey: corsTestAPIKey, trustedHosts: trustedHosts}, zap.NewNop().Sugar(), nil)
}

// TestCORSOriginAllowlist pins the exact Access-Control-Allow-Origin behavior
// on both doors that emit it: the shared /api/v1 surface and the /events SSE
// stream. The wildcard "*" must never appear, and a disallowed (or absent)
// Origin must produce no CORS headers at all.
func TestCORSOriginAllowlist(t *testing.T) {
	tests := []struct {
		name         string
		origin       string
		trustedHosts []string
		wantEcho     bool
	}{
		{name: "absent origin", origin: "", wantEcho: false},
		{name: "loopback ipv4 with port", origin: "http://127.0.0.1:5173", wantEcho: true},
		{name: "localhost with port", origin: "http://localhost:3000", wantEcho: true},
		{name: "loopback ipv6 bracketed", origin: "http://[::1]:8080", wantEcho: true},
		{name: "remote https origin", origin: "https://evil.example", wantEcho: false},
		{name: "null origin", origin: "null", wantEcho: false},
		{name: "loopback suffix confusion", origin: "http://127.0.0.1.evil.com", wantEcho: false},
		{name: "origin with path is not an origin", origin: "http://localhost:3000/path", wantEcho: false},
		{
			name:         "trusted host echoed",
			origin:       "https://ui.example.com",
			trustedHosts: []string{"ui.example.com"},
			wantEcho:     true,
		},
		{
			name:         "wildcard trusted host echoes concrete origin",
			origin:       "https://evil.example",
			trustedHosts: []string{"*"},
			wantEcho:     true,
		},
	}

	doors := []struct {
		name   string
		method string
		path   string
	}{
		// HEAD, not GET: handleSSEEvents returns immediately on HEAD while a
		// GET blocks on the live stream.
		{name: "api", method: http.MethodGet, path: "/api/v1/status"},
		{name: "sse", method: http.MethodHead, path: "/events"},
	}

	for _, tc := range tests {
		for _, door := range doors {
			t.Run(tc.name+"/"+door.name, func(t *testing.T) {
				srv := newCORSServer(tc.trustedHosts)

				req := httptest.NewRequest(door.method, door.path, nil)
				req.Header.Set("X-API-Key", corsTestAPIKey)
				if tc.origin != "" {
					req.Header.Set("Origin", tc.origin)
				}
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, req)

				gotOrigin := w.Header().Get("Access-Control-Allow-Origin")
				assert.NotEqual(t, "*", gotOrigin, "wildcard CORS origin must never be emitted")

				if tc.wantEcho {
					assert.Equal(t, tc.origin, gotOrigin, "allowed origin must be echoed verbatim")
				} else {
					assert.Empty(t, gotOrigin, "disallowed origin must get no Access-Control-Allow-Origin")
					assert.Empty(t, w.Header().Get("Access-Control-Allow-Methods"))
					assert.Empty(t, w.Header().Get("Access-Control-Allow-Headers"))
				}

				// Vary: Origin is required on every response, allowed or not,
				// so shared caches never serve one origin's response to another.
				assert.Contains(t, w.Header().Values("Vary"), "Origin", "Vary must advertise Origin")

				// Credentialed CORS is never enabled.
				assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
			})
		}
	}
}

// TestCORSOnUnauthenticatedResponse checks that the CORS decision is made
// ahead of authentication, so a 401 still carries Vary: Origin and does not
// become a cache entry served to a different origin.
func TestCORSOnUnauthenticatedResponse(t *testing.T) {
	srv := newCORSServer(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Header().Values("Vary"), "Origin")
	assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
}

// TestCORSPreflight pins the OPTIONS short-circuit, which runs ahead of
// apiKeyAuthMiddleware: browsers never send credentials on a preflight, so it
// must stay unauthenticated, but it must still only advertise CORS to an
// allowed origin.
func TestCORSPreflight(t *testing.T) {
	t.Run("allowed origin", func(t *testing.T) {
		srv := newCORSServer(nil)

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/servers", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "POST")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Less(t, w.Code, 300, "preflight must not require an API key")
		assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
		// PATCH is advertised: the REST API registers PATCH routes
		// (/api/v1/servers/{id}, /api/v1/config), and omitting it makes them
		// unreachable from an allowed cross-origin caller.
		assert.Equal(t, "GET, POST, PUT, PATCH, DELETE, OPTIONS", w.Header().Get("Access-Control-Allow-Methods"))
		assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"))
		assert.Contains(t, w.Header().Values("Vary"), "Origin")
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
	})

	t.Run("disallowed origin", func(t *testing.T) {
		srv := newCORSServer(nil)

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/servers", nil)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Access-Control-Request-Method", "POST")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Less(t, w.Code, 300, "preflight still short-circuits")
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Methods"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Headers"))
		assert.Contains(t, w.Header().Values("Vary"), "Origin")
	})
}
