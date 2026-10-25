package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.uber.org/zap"
)

// The Web UI's SSE stream and the tray send the admin key as ?apikey=, so the
// http.url span attribute must not carry it to the OTLP collector.
func TestHTTPMiddleware_MasksAPIKeyInSpanURL(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })

	tm := &TracingManager{
		logger:  zap.NewNop().Sugar(),
		enabled: true,
		tracer:  provider.Tracer("test"),
	}

	const key = "3f9a0c1d2e4b5a6978c0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6"
	handler := tm.HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/events?apikey="+key+"&limit=5", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d ended spans, want 1", len(spans))
	}
	var gotURL string
	for _, kv := range spans[0].Attributes() {
		if kv.Key == semconv.HTTPURLKey {
			gotURL = kv.Value.AsString()
		}
	}
	if gotURL == "" {
		t.Fatal("span has no http.url attribute")
	}
	if strings.Contains(gotURL, key) {
		t.Fatalf("http.url carries the API key: %q", gotURL)
	}
	if !strings.HasPrefix(gotURL, "/events?") || !strings.Contains(gotURL, "limit=5") {
		t.Fatalf("http.url lost its path or non-secret parameters: %q", gotURL)
	}
}
