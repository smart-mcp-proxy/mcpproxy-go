package scanner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newBareEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	logger := zap.NewNop()
	return NewEngine(nil, NewRegistry(dir, logger), dir, logger)
}

// TestClearActiveJobOnlyEvictsItsOwnJob pins the activeScans slot invariant.
//
// executeScan's cleanup used to delete by server name alone. CancelScan drops a
// job from activeScans immediately while its scanner goroutines are still
// running, so a replacement scan for the same server can take the slot before
// the cancelled scan's cleanup runs — and that cleanup then evicted the
// REPLACEMENT. With the slot empty, StartScan would accept a third concurrent
// scan of the same server and GetScanStatus would report "no active scan" while
// one was running.
func TestClearActiveJobOnlyEvictsItsOwnJob(t *testing.T) {
	e := newBareEngine(t)

	jobA := &ScanJob{ID: "job-a", ServerName: "srv", Status: ScanJobStatusRunning}
	jobB := &ScanJob{ID: "job-b", ServerName: "srv", Status: ScanJobStatusRunning}

	e.mu.Lock()
	e.activeScans["srv"] = jobA
	e.mu.Unlock()

	// A user cancels scan A. Its goroutines keep running for now.
	require.NoError(t, e.CancelScan("srv"))
	require.Nil(t, e.GetActiveJob("srv"))

	// A replacement scan takes the freed slot.
	e.mu.Lock()
	e.activeScans["srv"] = jobB
	e.mu.Unlock()

	// Scan A finally unwinds and runs its cleanup.
	e.clearActiveJob("srv", jobA)

	active := e.GetActiveJob("srv")
	require.NotNil(t, active, "the replacement scan must still hold the slot")
	assert.Equal(t, "job-b", active.ID)

	// B's own cleanup does free the slot.
	e.clearActiveJob("srv", jobB)
	assert.Nil(t, e.GetActiveJob("srv"))
}

// TestCancelScanRejectsAlreadyFinishedJob covers the window between the
// terminal-status write in executeScan and the job leaving activeScans (the
// completion callback persists the report inside it). A cancel arriving there
// used to report success and flip Status to cancelled while the already-cloned
// COMPLETED job was still persisted and emitted — the API told the user the
// scan was cancelled, and the stored result said it completed.
func TestCancelScanRejectsAlreadyFinishedJob(t *testing.T) {
	for _, status := range []string{ScanJobStatusCompleted, ScanJobStatusFailed} {
		t.Run(status, func(t *testing.T) {
			e := newBareEngine(t)
			job := &ScanJob{ID: "job-1", ServerName: "srv", Status: status}
			e.mu.Lock()
			e.activeScans["srv"] = job
			e.mu.Unlock()

			err := e.CancelScan("srv")
			require.Error(t, err, "cancelling a settled scan must not report success")

			// The job stays in activeScans until its own cleanup runs, so the
			// status endpoint keeps answering for the completion callback window.
			active := e.GetActiveJob("srv")
			require.NotNil(t, active)
			assert.Equal(t, status, active.Status, "the terminal status must be left intact")
		})
	}
}

// TestCancelScanCancelsRunningJob keeps the happy path honest.
func TestCancelScanCancelsRunningJob(t *testing.T) {
	e := newBareEngine(t)
	job := &ScanJob{ID: "job-1", ServerName: "srv", Status: ScanJobStatusRunning}
	e.mu.Lock()
	e.activeScans["srv"] = job
	e.mu.Unlock()

	require.NoError(t, e.CancelScan("srv"))
	assert.Nil(t, e.GetActiveJob("srv"))

	e.mu.Lock()
	gotStatus := job.Status
	e.mu.Unlock()
	assert.Equal(t, ScanJobStatusCancelled, gotStatus)
}
