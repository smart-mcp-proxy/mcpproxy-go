package scanner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 116 PR2 (FR-014a, T2.8): a scan that outlives a remove and re-add of
// its server leaves no job or report under the replacement, and an unforced
// approval of the replacement is refused for lack of a scan.

// rowStorage is mockStorage plus the stored-row incarnation the conditional
// saves check, the way storage.Manager checks the upstreams row.
type rowStorage struct {
	*mockStorage
	rowMu sync.Mutex
	rows  map[string]int64
}

func (r *rowStorage) setRow(name string, inc int64) {
	r.rowMu.Lock()
	defer r.rowMu.Unlock()
	r.rows[name] = inc
}

func (r *rowStorage) rowIs(name string, inc int64) bool {
	r.rowMu.Lock()
	defer r.rowMu.Unlock()
	cur, ok := r.rows[name]
	return ok && cur == inc
}

func (r *rowStorage) SaveScanJobIfIncarnation(job *ScanJob, inc int64) (bool, error) {
	if !r.rowIs(job.ServerName, inc) {
		return false, nil
	}
	return true, r.SaveScanJob(job)
}

func (r *rowStorage) SaveScanReportIfIncarnation(report *ScanReport, inc int64) (bool, error) {
	if !r.rowIs(report.ServerName, inc) {
		return false, nil
	}
	return true, r.SaveScanReport(report)
}

// rowUnquarantiner reports the row's incarnation as current.
type rowUnquarantiner struct{ store *rowStorage }

func (u *rowUnquarantiner) UnquarantineServer(string) error { return nil }
func (u *rowUnquarantiner) ServerIncarnation(name string) (int64, bool) {
	u.store.rowMu.Lock()
	defer u.store.rowMu.Unlock()
	inc, ok := u.store.rows[name]
	return inc, ok
}

func TestLateScanResult_T2_8_DroppedAfterReAdd(t *testing.T) {
	svc, base, _ := newTestService(t)
	store := &rowStorage{mockStorage: base, rows: map[string]int64{"acme": 1}}
	svc.storage = store
	svc.SetServerUnquarantiner(&rowUnquarantiner{store: store})

	job := &ScanJob{ID: "scan-acme-1", ServerName: "acme", Status: ScanJobStatusRunning, ScanPass: ScanPassSecurityScan,
		StartedAt: time.Now(), Incarnation: svc.serverIncarnation("acme")}
	require.Equal(t, int64(1), job.Incarnation, "the job records the incarnation it scans")
	adapter := &scanCallbackAdapter{service: svc, scanPass: ScanPassSecurityScan}
	adapter.OnScanStarted(job)
	require.Len(t, base.jobs, 1)

	// Remove (the commit purges the server's jobs) and re-add at incarnation 3.
	base.mu.Lock()
	delete(base.jobs, job.ID)
	base.mu.Unlock()
	store.setRow("acme", 3)

	// The old scan completes.
	done := *job
	done.Status = ScanJobStatusCompleted
	done.CompletedAt = time.Now()
	adapter.OnScannerCompleted(&done, "tpa-descriptions", &ScanReport{ID: "r1", JobID: job.ID, ServerName: "acme", ScannerID: "tpa-descriptions", ScannedAt: time.Now()})
	adapter.OnScanCompleted(&done, nil)

	assert.Empty(t, base.jobs, "no job of the old incarnation under the replacement")
	assert.Empty(t, base.reports, "no report of the old incarnation under the replacement")
	err := svc.ApproveServer(context.Background(), "acme", false, "api")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no scan results")
}

// A report saved before PR2 (no incarnation on its job) of the current server
// still counts; one of a different incarnation does not.
func TestReportIsForIncarnation(t *testing.T) {
	svc, base, _ := newTestService(t)
	require.NoError(t, base.SaveScanJob(&ScanJob{ID: "legacy", ServerName: "acme"}))
	require.NoError(t, base.SaveScanJob(&ScanJob{ID: "old", ServerName: "acme", Incarnation: 1}))
	require.NoError(t, base.SaveScanJob(&ScanJob{ID: "cur", ServerName: "acme", Incarnation: 2}))
	assert.True(t, svc.reportIsForIncarnation(&AggregatedReport{JobID: "legacy"}, 2))
	assert.False(t, svc.reportIsForIncarnation(&AggregatedReport{JobID: "old"}, 2))
	assert.True(t, svc.reportIsForIncarnation(&AggregatedReport{JobID: "cur"}, 2))
}
