package scanner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 116 PR2 review round 1 (Sol): scan evidence stays bound to the
// incarnation of the job that produced it (FR-014a).

func newRowService(t *testing.T, rows map[string]int64) (*Service, *mockStorage, *rowStorage) {
	t.Helper()
	svc, base, _ := newTestService(t)
	store := &rowStorage{mockStorage: base, rows: rows}
	svc.storage = store
	svc.SetServerUnquarantiner(&rowUnquarantiner{store: store})
	return svc, base, store
}

// Pass 2 and its failed-source placeholder carry the ORIGINATING job's
// incarnation instead of recapturing the current one: a late Pass 1 of the
// removed server A must not schedule a Pass 2 that stores A-source evidence
// under the replacement B.
func TestPass2_R1_UsesOriginatingIncarnation(t *testing.T) {
	svc, base, _ := newRowService(t, map[string]int64{"acme": 3}) // B is incarnation 3

	svc.saveFailedPass2Job("acme", "source resolution failed", 1) // A's job was incarnation 1
	assert.Empty(t, base.jobs, "A's Pass 2 placeholder is not stored under B")

	svc.startPass2("acme", &ServerInfo{Name: "acme", Protocol: "stdio", Command: "true"}, 1)
	assert.Nil(t, svc.engine.GetActiveJob("acme"), "no Pass 2 runs for a replaced incarnation")
	assert.Empty(t, base.jobs)
	assert.Empty(t, base.reports)
}

// With a committed-view provider installed, a server that is not configured
// is not scanned: an unbound (incarnation 0) job would persist evidence a
// later same-name server inherits.
func TestStartScan_R1_MissingServerRejected(t *testing.T) {
	svc, base, _ := newRowService(t, map[string]int64{})
	_, err := svc.StartScan(context.Background(), "ghost", false, nil, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
	assert.Empty(t, base.jobs)
	assert.Nil(t, svc.engine.GetActiveJob("ghost"))
}

// An unbound job or report goes through the conditional writer too, so the
// storage decides (it refuses unbound writes once a commit exists).
func TestSaveJob_R1_UnboundUsesConditionalWriter(t *testing.T) {
	svc, base, _ := newRowService(t, map[string]int64{"acme": 3})
	svc.saveJob(&ScanJob{ID: "scan-acme-u", ServerName: "acme", Status: ScanJobStatusRunning})
	svc.saveReport(nil, &ScanReport{ID: "ru", ServerName: "acme"})
	assert.Empty(t, base.jobs)
	assert.Empty(t, base.reports)
}

// The purge observer detaches the removed server's active job, so the
// re-added server neither reports A's job and "scanning" status nor has its
// own scan rejected as already in progress; a job of the CURRENT incarnation
// is kept, and A's cleanup cannot evict B's job.
func TestInvalidateServerState_R1_DetachesObsoleteActiveJob(t *testing.T) {
	svc, _, store := newRowService(t, map[string]int64{"acme": 1})
	jobA := &ScanJob{ID: "scan-acme-A", ServerName: "acme", Status: ScanJobStatusRunning, StartedAt: time.Now(), Incarnation: 1}
	svc.engine.mu.Lock()
	svc.engine.activeScans["acme"] = jobA
	svc.engine.mu.Unlock()

	// Remove and re-add as incarnation 3, then the purge observer fires.
	store.setRow("acme", 3)
	svc.InvalidateServerState("acme")
	assert.Nil(t, svc.engine.GetActiveJob("acme"), "A's job no longer occupies the slot")

	jobB := &ScanJob{ID: "scan-acme-B", ServerName: "acme", Status: ScanJobStatusRunning, StartedAt: time.Now(), Incarnation: 3}
	svc.engine.mu.Lock()
	svc.engine.activeScans["acme"] = jobB
	svc.engine.mu.Unlock()
	svc.InvalidateServerState("acme")
	require.NotNil(t, svc.engine.GetActiveJob("acme"), "the current incarnation's job is kept")
	svc.engine.clearActiveJob("acme", jobA)
	active := svc.engine.GetActiveJob("acme")
	require.NotNil(t, active, "A's cleanup does not evict B's job")
	assert.Equal(t, "scan-acme-B", active.ID)
}
