package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// MCP-32: quarantine state changes are exported as a counter.

func TestRecordQuarantineEvent(t *testing.T) {
	mm := NewMetricsManager(zap.NewNop().Sugar())

	mm.RecordQuarantineEvent("server", "quarantined")
	mm.RecordQuarantineEvent("server", "quarantined")
	mm.RecordQuarantineEvent("tool", "changed")

	assert.InDelta(t, 2.0, testutil.ToFloat64(mm.quarantineEvents.WithLabelValues("server", "quarantined")), 1e-9)
	assert.InDelta(t, 1.0, testutil.ToFloat64(mm.quarantineEvents.WithLabelValues("tool", "changed")), 1e-9)
}

// The quarantine counter is exposed on the /metrics scrape output.
func TestQuarantineEvents_Scraped(t *testing.T) {
	mm := NewMetricsManager(zap.NewNop().Sugar())
	mm.RecordQuarantineEvent("tool", "pending")

	body := scrapeMetrics(t, mm)
	require.Contains(t, body, "mcpproxy_quarantine_events_total")
	require.Contains(t, body, `scope="tool"`)
	require.Contains(t, body, `action="pending"`)
}

// MCP-3207: the tool-call metric must stay low-cardinality — it carries only
// {server,tool,status} and never per-user / per-profile labels (user_id and
// profile are OTLP span attributes instead; see
// internal/server/observability_edition_server.go). This locks the design so a
// future change can't silently push per-tenant cardinality into the metric, in
// either edition. (Acceptance: metric-layer negative control.)
func TestToolCallMetric_NoPerUserCardinalityLabels(t *testing.T) {
	mm := NewMetricsManager(zap.NewNop().Sugar())
	mm.RecordToolCall("github", "create_issue", StatusSuccess, time.Millisecond)

	body := scrapeMetrics(t, mm)
	require.Contains(t, body, "mcpproxy_tool_calls_total")
	require.Contains(t, body, `server="github"`)
	require.Contains(t, body, `tool="create_issue"`)
	require.Contains(t, body, `status="success"`)

	assert.NotContains(t, body, "user_id=", "tool-call metric must not carry a user_id label")
	assert.NotContains(t, body, "profile=", "tool-call metric must not carry a profile label")
}

// The HTTP middleware runs before authentication, so its path label must be
// the matched route, not the raw request path: otherwise every distinct path
// anyone sends adds a counter and a histogram series that is never freed.
func TestHTTPMiddleware_PathLabelIsRoutePattern(t *testing.T) {
	mm := NewMetricsManager(zap.NewNop().Sugar())
	r := chi.NewRouter()
	r.Use(mm.HTTPMiddleware())
	r.Get("/api/v1/servers/{name}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, p := range []string{"/api/v1/servers/a", "/api/v1/servers/b", "/api/v1/nope-1", "/api/v1/nope-2"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}

	assert.Equal(t, 2, testutil.CollectAndCount(mm.httpRequests), "one series per route plus one for unmatched paths")
	assert.InDelta(t, 2.0, testutil.ToFloat64(mm.httpRequests.WithLabelValues(http.MethodGet, "/api/v1/servers/{name}", "OK")), 1e-9)
	assert.InDelta(t, 2.0, testutil.ToFloat64(mm.httpRequests.WithLabelValues(http.MethodGet, "unmatched", "Not Found")), 1e-9)
}

func scrapeMetrics(t *testing.T, mm *MetricsManager) string {
	t.Helper()
	families, err := mm.registry.Gather()
	require.NoError(t, err)
	var sb strings.Builder
	for _, mf := range families {
		sb.WriteString(mf.GetName())
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				sb.WriteString(" ")
				sb.WriteString(l.GetName())
				sb.WriteString("=\"")
				sb.WriteString(l.GetValue())
				sb.WriteString("\"")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
