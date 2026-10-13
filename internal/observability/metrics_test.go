package observability

import (
	"strings"
	"testing"
	"time"

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
