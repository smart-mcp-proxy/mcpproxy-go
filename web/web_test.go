package web

import (
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

// serveWeb issues a GET against the handler (paths arrive with the /ui prefix
// already stripped, exactly as in production).
func serveWeb(t *testing.T, onIndexServe func(), path string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandlerWithIndexCallback(zap.NewNop().Sugar(), onIndexServe)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	h.ServeHTTP(rec, req)
	return rec
}

// TestIndexServeCallback_CountsIndexNotAssets asserts FR-006: serving the UI
// entrypoint (index document) fires the callback; asset requests do not, even
// when a missing asset falls back to the index document body.
func TestIndexServeCallback_CountsIndexNotAssets(t *testing.T) {
	var count atomic.Int64
	cb := func() { count.Add(1) }

	// Root → index document: counts.
	rec := serveWeb(t, cb, "/")
	if rec.Code != 200 {
		t.Fatalf("expected 200 for /, got %d", rec.Code)
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("expected 1 index serve after /, got %d", got)
	}

	// Explicit index.html: counts.
	serveWeb(t, cb, "/index.html")
	if got := count.Load(); got != 2 {
		t.Fatalf("expected 2 index serves after /index.html, got %d", got)
	}

	// SPA client-side route (no extension) → index fallback: counts. A user
	// deep-linking into the UI opened it.
	serveWeb(t, cb, "/servers")
	if got := count.Load(); got != 3 {
		t.Fatalf("expected 3 index serves after SPA route, got %d", got)
	}

	// Asset requests must never count — neither present assets nor missing
	// ones that fall back to the index body (stale hashed bundles after an
	// upgrade, favicons, source maps).
	for _, p := range []string{"/assets/app-abc123.js", "/assets/style.css", "/favicon.ico", "/logo.png"} {
		serveWeb(t, cb, p)
	}
	if got := count.Load(); got != 3 {
		t.Fatalf("expected asset requests uncounted (still 3), got %d", got)
	}
}

// TestIndexServeCallback_NilCallbackSafe asserts the handler works without a
// callback (and via the legacy constructor).
func TestIndexServeCallback_NilCallbackSafe(t *testing.T) {
	rec := serveWeb(t, nil, "/")
	if rec.Code != 200 {
		t.Fatalf("expected 200 with nil callback, got %d", rec.Code)
	}

	h := NewHandler(zap.NewNop().Sugar())
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/", nil))
	if rec2.Code != 200 {
		t.Fatalf("expected 200 via NewHandler, got %d", rec2.Code)
	}
}

// TestServerEditionHint_OnlyIndexes verifies that the non-secret edition hint
// is injected into every served SPA document (including deep links), while
// asset responses and the index-open telemetry callback retain their existing
// behavior.
func TestServerEditionHint_OnlyIndexes(t *testing.T) {
	var count atomic.Int64
	h := NewHandlerWithOptions(zap.NewNop().Sugar(), HandlerOptions{
		ServerEditionEnabled: true,
		OnIndexServe:         func() { count.Add(1) },
	})

	for _, requestPath := range []string{"/", "/servers"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", requestPath, nil))
		if rec.Code != 200 {
			t.Fatalf("expected 200 for %s, got %d", requestPath, rec.Code)
		}
		if got := rec.Body.String(); !strings.Contains(got, `name="mcpproxy-server-edition" content="true"`) {
			t.Fatalf("expected server-edition marker in %s response, got %q", requestPath, got)
		}
		if got := rec.Body.String(); strings.Count(got, "mcpproxy-server-edition") != 1 || !strings.Contains(got, "mcpproxy-server-edition\" content=\"true\">\n</head>") {
			t.Fatalf("expected exactly one marker inside a complete head in %s response, got %q", requestPath, got)
		}
	}
	if got := count.Load(); got != 2 {
		t.Fatalf("expected 2 index serves, got %d", got)
	}

	asset := httptest.NewRecorder()
	h.ServeHTTP(asset, httptest.NewRequest("GET", "/assets/app-abc123.js", nil))
	if got := asset.Body.String(); strings.Contains(got, "mcpproxy-server-edition") {
		t.Fatal("asset fallback must not be rewritten with the server-edition marker")
	}
	if got := count.Load(); got != 2 {
		t.Fatalf("expected asset request not to count as an index serve, got %d", got)
	}
}

func TestServerEditionHint_FalseDoesNotLeakConfiguration(t *testing.T) {
	h := NewHandlerWithOptions(zap.NewNop().Sugar(), HandlerOptions{ServerEditionEnabled: false})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `name="mcpproxy-server-edition" content="false"`) {
		t.Fatalf("expected explicit personal-edition marker, got %q", body)
	}
}
