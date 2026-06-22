package observability

import (
	"strings"
	"testing"

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
