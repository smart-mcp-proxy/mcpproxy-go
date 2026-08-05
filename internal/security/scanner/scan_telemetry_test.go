package scanner

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// telemetrySamples extracts the anonymous scan-telemetry samples recorded by the
// mock emitter, in order. Each sample is (completed, findings).
func telemetrySamples(t *testing.T, em *mockEmitter) []mockEvent {
	t.Helper()
	em.mu.Lock()
	defer em.mu.Unlock()
	var out []mockEvent
	for _, ev := range em.events {
		if ev.eventType == "scan_telemetry" {
			out = append(out, ev)
		}
	}
	return out
}

// TestScanTelemetry_Pass1CompletionRecordsOnce pins the unit of measure: one
// terminal Pass-1 job produces exactly one completed sample carrying the
// aggregated per-severity counts (and nothing else).
func TestScanTelemetry_Pass1CompletionRecordsOnce(t *testing.T) {
	svc, _, em := newTestService(t)
	adapter := &scanCallbackAdapter{service: svc, scanPass: ScanPassSecurityScan}

	job := &ScanJob{ID: "job-1", ServerName: "my-private-server", ScanPass: ScanPassSecurityScan}
	reports := []*ScanReport{
		{Findings: []ScanFinding{{Severity: "high"}, {Severity: "high"}}},
		{Findings: []ScanFinding{{Severity: "low"}}},
	}
	adapter.OnScanCompleted(job, reports)

	samples := telemetrySamples(t, em)
	require.Len(t, samples, 1, "one Pass-1 job ⇒ exactly one telemetry sample")
	assert.Equal(t, true, samples[0].data["completed"])
	assert.Equal(t, map[string]int{"high": 2, "low": 1}, samples[0].data["findings"])
}

// TestScanTelemetry_Pass2DoesNotRecord is the regression guard for the
// double-counting bug: deep scan auto-starts a Pass-2 supply-chain job after
// every Pass 1, so counting Pass 2 would report ~2x scans for exactly the
// deep-scan cohort the counters exist to compare.
func TestScanTelemetry_Pass2DoesNotRecord(t *testing.T) {
	svc, _, em := newTestService(t)
	adapter := &scanCallbackAdapter{service: svc, scanPass: ScanPassSupplyChainAudit}

	job := &ScanJob{ID: "job-2", ServerName: "my-private-server", ScanPass: ScanPassSupplyChainAudit}
	adapter.OnScanCompleted(job, []*ScanReport{{Findings: []ScanFinding{{Severity: "critical"}}}})
	adapter.OnScanFailed(job, errors.New("all scanners failed"))

	assert.Empty(t, telemetrySamples(t, em), "Pass 2 must never record scan telemetry")
}

// TestScanTelemetry_PerScannerFailuresDoNotRecord pins the other half of the
// fix: OnScannerFailed fires once PER FAILING SCANNER, so it must not record.
// A job with three failing scanners is still one failed scan, counted once by
// the job-level OnScanFailed.
func TestScanTelemetry_PerScannerFailuresDoNotRecord(t *testing.T) {
	svc, _, em := newTestService(t)
	adapter := &scanCallbackAdapter{service: svc, scanPass: ScanPassSecurityScan}

	job := &ScanJob{ID: "job-3", ServerName: "my-private-server", ScanPass: ScanPassSecurityScan}
	adapter.OnScannerFailed(job, "test-scanner", errors.New("boom"))
	adapter.OnScannerFailed(job, "scanner-b", errors.New("boom"))
	adapter.OnScannerFailed(job, "scanner-c", errors.New("boom"))

	assert.Empty(t, telemetrySamples(t, em), "per-scanner failures must not record scan telemetry")

	// The job-level terminal failure records exactly one failed sample.
	adapter.OnScanFailed(job, errors.New("all scanners failed"))
	samples := telemetrySamples(t, em)
	require.Len(t, samples, 1)
	assert.Equal(t, false, samples[0].data["completed"])
	assert.Nil(t, samples[0].data["findings"], "a failure sample carries no findings")
}

// TestScanTelemetry_PartialFailureCountsOnlyAsCompletion pins the mixed case:
// a job that loses some scanners but still completes is a completion, not both
// a completion and a failure.
func TestScanTelemetry_PartialFailureCountsOnlyAsCompletion(t *testing.T) {
	svc, _, em := newTestService(t)
	adapter := &scanCallbackAdapter{service: svc, scanPass: ScanPassSecurityScan}

	job := &ScanJob{ID: "job-4", ServerName: "my-private-server", ScanPass: ScanPassSecurityScan}
	adapter.OnScannerFailed(job, "test-scanner", errors.New("boom"))
	adapter.OnScanCompleted(job, []*ScanReport{{Findings: []ScanFinding{{Severity: "medium"}}}})

	samples := telemetrySamples(t, em)
	require.Len(t, samples, 1)
	assert.Equal(t, true, samples[0].data["completed"])
}

// TestScanTelemetry_DryRunDoesNotRecord: dry-run jobs do not affect quarantine
// state and are not real scans, so they stay out of the counters.
func TestScanTelemetry_DryRunDoesNotRecord(t *testing.T) {
	svc, _, em := newTestService(t)
	adapter := &scanCallbackAdapter{service: svc, scanPass: ScanPassSecurityScan}

	job := &ScanJob{ID: "job-5", ServerName: "my-private-server", ScanPass: ScanPassSecurityScan, DryRun: true}
	adapter.OnScanCompleted(job, []*ScanReport{{Findings: []ScanFinding{{Severity: "high"}}}})
	adapter.OnScanFailed(job, errors.New("all scanners failed"))

	assert.Empty(t, telemetrySamples(t, em), "dry-run jobs must not record scan telemetry")
}
