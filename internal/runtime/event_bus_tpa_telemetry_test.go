package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// newTPATelemetryRuntime builds the minimal Runtime seam used by the scan
// notification tests, plus a real telemetry service so the counter registry is
// reachable through Runtime.TelemetryRegistry().
func newTPATelemetryRuntime(t *testing.T) *Runtime {
	t.Helper()
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	rt := &Runtime{
		logger:    zap.NewNop(),
		eventSubs: make(map[chan Event]struct{}),
	}
	rt.telemetryService = telemetry.New(&config.Config{}, "", "v0.0.0-test", "personal", zap.NewNop())
	return rt
}

// TestEmitSecurityScanTelemetry_RecordsCounters asserts the schema-v8 hook:
// each call from the scanner's job-level seam increments the anonymous counters
// — completions, failures, findings-by-severity — and nothing else.
func TestEmitSecurityScanTelemetry_RecordsCounters(t *testing.T) {
	rt := newTPATelemetryRuntime(t)
	rt.scanNotify = newScanNotifyDebouncer(rt, 10*time.Millisecond)

	rt.EmitSecurityScanTelemetry(true, map[string]int{"high": 2, "low": 1})
	rt.EmitSecurityScanTelemetry(true, nil)
	rt.EmitSecurityScanTelemetry(false, nil)

	reg := rt.TelemetryRegistry()
	require.NotNil(t, reg, "telemetry registry must be reachable from the runtime")
	snap := reg.Snapshot()

	assert.Equal(t, int64(2), snap.TPAScansCompleted)
	assert.Equal(t, int64(1), snap.TPAScansFailed)
	assert.Equal(t, int64(1), snap.TPAScansWithFindings)
	assert.Equal(t, int64(2), snap.TPAFindings["high"])
	assert.Equal(t, int64(1), snap.TPAFindings["low"])
	assert.Equal(t, int64(0), snap.TPAFindings["critical"])

	// Privacy: only the fixed severity enum reaches the registry — no server
	// names, scanner ids, or error text.
	for k := range snap.TPAFindings {
		assert.True(t, telemetry.IsTPASeverity(k), "unexpected findings key %q", k)
	}
}

// TestEmitSecurityScanEvents_DoNotRecordTelemetry pins the fix for the
// over-counting bug: the UI-facing scan events fire per SCANNER (failures) and
// per PASS (completions), so they must record nothing. Only the dedicated
// job-level hook feeds the counters.
func TestEmitSecurityScanEvents_DoNotRecordTelemetry(t *testing.T) {
	rt := newTPATelemetryRuntime(t)
	rt.scanNotify = newScanNotifyDebouncer(rt, 10*time.Millisecond)

	// A single scan whose three scanners all fail, plus the Pass-1 and Pass-2
	// completions that follow a deep scan.
	rt.EmitSecurityScanFailed("my-private-server", "tpa-descriptions", "boom: /Users/algis/secret")
	rt.EmitSecurityScanFailed("my-private-server", "trivy", "boom")
	rt.EmitSecurityScanFailed("my-private-server", "cisco", "boom")
	rt.EmitSecurityScanCompleted("my-private-server", map[string]int{"high": 2})
	rt.EmitSecurityScanCompleted("my-private-server", map[string]int{"critical": 1})

	reg := rt.TelemetryRegistry()
	require.NotNil(t, reg)
	snap := reg.Snapshot()

	assert.Equal(t, int64(0), snap.TPAScansCompleted, "UI completion events must not record telemetry")
	assert.Equal(t, int64(0), snap.TPAScansFailed, "per-scanner failures must not record telemetry")
	assert.Equal(t, int64(0), snap.TPAScansWithFindings)
	for sev, n := range snap.TPAFindings {
		assert.Equal(t, int64(0), n, "severity %q must be untouched by UI events", sev)
	}
}

// TestEmitSecurityScan_NilTelemetryIsSafe pins the nil-safe path: a Runtime
// without a telemetry service (telemetry disabled or not yet initialized) must
// still emit scan events without panicking.
func TestEmitSecurityScan_NilTelemetryIsSafe(t *testing.T) {
	rt := &Runtime{
		logger:    zap.NewNop(),
		eventSubs: make(map[chan Event]struct{}),
	}
	require.Nil(t, rt.TelemetryRegistry())

	assert.NotPanics(t, func() {
		rt.EmitSecurityScanCompleted("srv", map[string]int{"high": 1})
		rt.EmitSecurityScanFailed("srv", "tpa-descriptions", "err")
		rt.EmitSecurityScanTelemetry(true, map[string]int{"high": 1})
		rt.EmitSecurityScanTelemetry(false, nil)
	})
}
