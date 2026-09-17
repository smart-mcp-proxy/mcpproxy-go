package management

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// stubAuditFailureSink is the minimal double for internal/server's audit
// write-failure doctor source: only WriteFailures() matters here (T109).
type stubAuditFailureSink struct {
	failures uint64
}

func (s *stubAuditFailureSink) WriteFailures() uint64 { return s.failures }

// TestDoctor_AuditWriteFailuresThroughRuntimeWarningSource pins the T109
// doctor seam: a registered runtime-warning source reading the sink's
// always-on write-failure counter surfaces as a `mcpproxy doctor` finding
// (works with metrics disabled - the counter itself is not a Prometheus
// metric), and clears once the source reports zero again.
func TestDoctor_AuditWriteFailuresThroughRuntimeWarningSource(t *testing.T) {
	sink := &stubAuditFailureSink{}
	live := &config.Config{Listen: "127.0.0.1:8080"}

	svc := NewService(newMockRuntime(), live, "", &mockEventEmitter{}, nil, zap.NewNop().Sugar())
	svc.AddRuntimeWarningSource(func() []string {
		if n := sink.WriteFailures(); n > 0 {
			return []string{"audit_log: 3 write failures since start"}
		}
		return nil
	})

	diag, err := svc.Doctor(context.Background())
	require.NoError(t, err)
	assert.Empty(t, diag.RuntimeWarnings, "no failures yet, no finding")

	sink.failures = 3
	diag, err = svc.Doctor(context.Background())
	require.NoError(t, err)
	require.Len(t, diag.RuntimeWarnings, 1)
	assert.Contains(t, diag.RuntimeWarnings[0], "audit_log")
	assert.Contains(t, diag.RuntimeWarnings[0], "3 write failures")
	assert.Equal(t, 1, diag.TotalIssues)
}

// stubAuditSanitizerSink is the minimal double for internal/server's
// defence-in-depth sanitizer-hit doctor source (FR-015).
type stubAuditSanitizerSink struct {
	hits uint64
}

func (s *stubAuditSanitizerSink) SanitizerHits() uint64 { return s.hits }

// TestDoctor_AuditSanitizerHitsThroughRuntimeWarningSource pins the
// FR-015 doctor seam: a registered runtime-warning source reading the
// sink's always-on defence-in-depth sanitizer-hit counter surfaces as a
// `mcpproxy doctor` finding, and clears once the source reports zero again.
func TestDoctor_AuditSanitizerHitsThroughRuntimeWarningSource(t *testing.T) {
	sink := &stubAuditSanitizerSink{}
	live := &config.Config{Listen: "127.0.0.1:8080"}

	svc := NewService(newMockRuntime(), live, "", &mockEventEmitter{}, nil, zap.NewNop().Sugar())
	svc.AddRuntimeWarningSource(func() []string {
		if n := sink.SanitizerHits(); n > 0 {
			return []string{"audit_log: 2 defence-in-depth sanitizer hits since start"}
		}
		return nil
	})

	diag, err := svc.Doctor(context.Background())
	require.NoError(t, err)
	assert.Empty(t, diag.RuntimeWarnings, "no hits yet, no finding")

	sink.hits = 2
	diag, err = svc.Doctor(context.Background())
	require.NoError(t, err)
	require.Len(t, diag.RuntimeWarnings, 1)
	assert.Contains(t, diag.RuntimeWarnings[0], "audit_log")
	assert.Contains(t, diag.RuntimeWarnings[0], "sanitizer hits")
	assert.Equal(t, 1, diag.TotalIssues)
}
