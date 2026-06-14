package scanner

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestScannerJobStatusDuration verifies per-scanner wall-clock computation,
// including the degenerate cases (still running, never started, clock skew)
// where we must report 0 rather than a negative or bogus value.
func TestScannerJobStatusDuration(t *testing.T) {
	start := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		status ScannerJobStatus
		wantMs int64
	}{
		{
			name:   "normal duration",
			status: ScannerJobStatus{StartedAt: start, CompletedAt: start.Add(1500 * time.Millisecond)},
			wantMs: 1500,
		},
		{
			name:   "never started",
			status: ScannerJobStatus{CompletedAt: start},
			wantMs: 0,
		},
		{
			name:   "still running",
			status: ScannerJobStatus{StartedAt: start},
			wantMs: 0,
		},
		{
			name:   "completed before started (clock skew)",
			status: ScannerJobStatus{StartedAt: start, CompletedAt: start.Add(-time.Second)},
			wantMs: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.Duration().Milliseconds(); got != tt.wantMs {
				t.Errorf("Duration() = %d ms, want %d ms", got, tt.wantMs)
			}
		})
	}
}

// TestUpdateScannerStatusComputesDurationMs guards that DurationMs is populated
// as soon as both timestamps are known. The live scan path sets StartedAt and
// CompletedAt in two separate updateScannerStatus calls, so the duration must
// be recomputed from the stored StartedAt when CompletedAt arrives.
func TestUpdateScannerStatusComputesDurationMs(t *testing.T) {
	engine := NewEngine(nil, nil, t.TempDir(), zap.NewNop())
	job := &ScanJob{
		ID: "job-1",
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "scanner-a", Status: ScanJobStatusPending},
		},
	}
	start := time.Now()

	// Running: only StartedAt is set; CompletedAt stays zero → no duration yet.
	engine.updateScannerStatus(job, "scanner-a", ScanJobStatusRunning, start, time.Time{}, "", 0)
	if got := job.ScannerStatuses[0].DurationMs; got != 0 {
		t.Fatalf("DurationMs = %d while running, want 0", got)
	}

	// Completed: CompletedAt set; duration computed from the stored StartedAt.
	engine.updateScannerStatus(job, "scanner-a", ScanJobStatusCompleted, time.Time{}, start.Add(2*time.Second), "", 3)
	if got := job.ScannerStatuses[0].DurationMs; got != 2000 {
		t.Errorf("DurationMs = %d, want 2000", got)
	}
}
