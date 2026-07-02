package server

import (
	"context"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestScanSummaryEnricherAdapterCarriesDeepScan is the Spec 077 US3 contract
// regression: the REST/SSE adapter must NOT drop the opt-in deep-scan descriptor
// when mapping the scanner-internal ScanSummary onto contracts.SecurityScanSummary
// (Finding #3). With deep scan enabled and a failed deep scanner, the wire-shape
// summary must expose deep_scan.enabled and the per-scanner failure. When deep
// scan is off, the descriptor stays nil (omitted).
func TestScanSummaryEnricherAdapterCarriesDeepScan(t *testing.T) {
	logger := zap.NewNop()

	// seed builds a fresh scanner service (own storage + summary cache) with a
	// completed baseline job carrying one succeeded and one failed (deep) scanner.
	seed := func(t *testing.T) *scanner.Service {
		t.Helper()
		dir := t.TempDir()
		db := setupTestStorage(t)
		svc := scanner.NewService(db, scanner.NewRegistry(dir, logger), scanner.NewDockerRunner(logger), dir, logger)
		now := time.Now()
		require.NoError(t, db.SaveScanJob(&scanner.ScanJob{
			ID:         "j-deep",
			ServerName: "server-a",
			Status:     scanner.ScanJobStatusCompleted,
			Scanners:   []string{"s1", "s2"},
			StartedAt:  now,
			ScannerStatuses: []scanner.ScannerJobStatus{
				{ScannerID: "s1", Status: scanner.ScanJobStatusCompleted, FindingsCount: 0},
				{ScannerID: "s2", Status: scanner.ScanJobStatusFailed, Error: "image not found"},
			},
		}))
		require.NoError(t, db.SaveScanReport(&scanner.ScanReport{
			ID: "r1", JobID: "j-deep", ServerName: "server-a", ScannerID: "s1",
			Findings: []scanner.ScanFinding{}, ScannedAt: now,
		}))
		return svc
	}

	// Deep scan OFF (default): descriptor omitted on the wire summary.
	offSvc := seed(t)
	off := (&scanSummaryEnricherAdapter{scanner: offSvc}).GetSecurityScanSummary(context.Background(), "server-a")
	require.NotNil(t, off)
	require.Nil(t, off.DeepScan, "deep_scan must be omitted while deep scan is off")

	// Deep scan ON: descriptor must be carried through, not dropped.
	onSvc := seed(t)
	onSvc.SetDeepScan(true, nil)
	on := (&scanSummaryEnricherAdapter{scanner: onSvc}).GetSecurityScanSummary(context.Background(), "server-a")
	require.NotNil(t, on)
	require.NotNil(t, on.DeepScan, "adapter must carry summary.DeepScan onto the contract")
	require.True(t, on.DeepScan.Enabled, "deep_scan.enabled must be true")
	require.Len(t, on.DeepScan.ScannersFailed, 1)
	require.Equal(t, "s2", on.DeepScan.ScannersFailed[0].ID)
	require.Equal(t, "image not found", on.DeepScan.ScannersFailed[0].Reason)
	// The baseline verdict itself is unaffected by the deep-scan failure.
	require.NotEqual(t, "degraded", on.Status, "baseline verdict must not be degraded by a deep-scan failure")
}
