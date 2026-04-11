package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

func TestSurfaceClassifierMiddlewareIncrements(t *testing.T) {
	cases := []struct {
		name        string
		header      string
		wantSurface string
	}{
		{"empty header → unknown", "", "unknown"},
		{"tray header", "tray/v0.21.0", "tray"},
		{"cli header", "cli/v0.21.0", "cli"},
		{"webui header", "webui/v0.21.0", "webui"},
		{"unknown prefix", "spoof/v1.0", "unknown"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := telemetry.NewCounterRegistry()
			handler := SurfaceClassifierMiddleware(func() *telemetry.CounterRegistry { return reg })(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}),
			)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
			if c.header != "" {
				req.Header.Set(XMCPProxyClientHeader, c.header)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			snap := reg.Snapshot()
			if snap.SurfaceCounts[c.wantSurface] != 1 {
				t.Errorf("%s: surface counter %s = %d, want 1", c.name, c.wantSurface, snap.SurfaceCounts[c.wantSurface])
			}
		})
	}
}

func TestSurfaceClassifierMiddlewareNilRegistryNoOp(t *testing.T) {
	// Should not panic even when the getter returns nil.
	handler := SurfaceClassifierMiddleware(func() *telemetry.CounterRegistry { return nil })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set(XMCPProxyClientHeader, "tray/v1")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
}

func TestRESTEndpointHistogramMiddleware(t *testing.T) {
	reg := telemetry.NewCounterRegistry()

	router := chi.NewRouter()
	router.Use(RESTEndpointHistogramMiddleware(func() *telemetry.CounterRegistry { return reg }))
	router.Get("/api/v1/servers", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	router.Post("/api/v1/servers/{name}/enable", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	router.Get("/api/v1/fail", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil),
		httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/servers/my-secret-server/enable", nil),
		httptest.NewRequest(http.MethodGet, "/api/v1/fail", nil),
		httptest.NewRequest(http.MethodGet, "/api/v1/does-not-exist", nil),
	} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
	}

	snap := reg.Snapshot()
	if snap.RESTEndpointCalls["GET /api/v1/servers"]["2xx"] != 2 {
		t.Errorf("GET /api/v1/servers 2xx = %d, want 2", snap.RESTEndpointCalls["GET /api/v1/servers"]["2xx"])
	}
	if snap.RESTEndpointCalls["POST /api/v1/servers/{name}/enable"]["2xx"] != 1 {
		t.Errorf("POST templated 2xx wrong: %v", snap.RESTEndpointCalls)
	}
	if snap.RESTEndpointCalls["GET /api/v1/fail"]["5xx"] != 1 {
		t.Errorf("GET /api/v1/fail 5xx wrong: %v", snap.RESTEndpointCalls)
	}
	// Unmatched route counted under UNMATCHED, not by raw path.
	if snap.RESTEndpointCalls["GET UNMATCHED"]["4xx"] != 1 {
		t.Errorf("UNMATCHED 4xx = %v, want 1", snap.RESTEndpointCalls["GET UNMATCHED"]["4xx"])
	}

	// Privacy: secret server name must NOT appear anywhere in the snapshot.
	for k, v := range snap.RESTEndpointCalls {
		if contains(k, "my-secret-server") {
			t.Errorf("path parameter leaked into key: %s", k)
		}
		for kk := range v {
			if contains(kk, "my-secret-server") {
				t.Errorf("path parameter leaked into status: %s", kk)
			}
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
