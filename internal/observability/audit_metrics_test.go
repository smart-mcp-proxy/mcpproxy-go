package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type stubAuditSink struct{ n uint64 }

func (s *stubAuditSink) WriteFailures() uint64 { return s.n }

// TestRegisterAuditSink_MirrorsWriteFailures pins Spec 107 T109:
// mcpproxy_audit_write_failures_total exists on /metrics and tracks the
// sink's own counter live (a CounterFunc, so no separate bookkeeping can
// drift out of sync).
func TestRegisterAuditSink_MirrorsWriteFailures(t *testing.T) {
	mm := NewMetricsManager(zap.NewNop().Sugar())
	sink := &stubAuditSink{}
	mm.RegisterAuditSink(sink)

	assert.Equal(t, float64(0), gatherCounterValue(t, mm, "mcpproxy_audit_write_failures_total"))

	sink.n = 5
	assert.Equal(t, float64(5), gatherCounterValue(t, mm, "mcpproxy_audit_write_failures_total"))
}

func TestRegisterAuditSink_NilSink_NoPanic(t *testing.T) {
	mm := NewMetricsManager(zap.NewNop().Sugar())
	mm.RegisterAuditSink(nil) // must be a no-op, never a nil-interface panic
}

// gatherCounterValue scrapes the manager's registry and returns the single
// value reported for the named counter.
func gatherCounterValue(t *testing.T, mm *MetricsManager, name string) float64 {
	t.Helper()
	mfs, err := mm.registry.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		require.Len(t, mf.GetMetric(), 1)
		return mf.GetMetric()[0].GetCounter().GetValue()
	}
	t.Fatalf("metric %q not found", name)
	return 0
}
