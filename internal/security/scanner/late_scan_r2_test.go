package scanner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Spec 116 PR2 review round 2 (Sol chunk 3).

// An obsolete scan job that reaches engine registration after the removal's
// purge observer already ran (Pass 1 or Pass 2 paused in source resolution)
// is refused at registration: it never takes the active slot of a same-name
// replacement, and the replacement can start its own scan.
func TestStartScan_R2_ObsoleteJobRefusedAtRegistration(t *testing.T) {
	svc, base, store := newRowService(t, map[string]int64{"acme": 1})
	// In-process scanners only, so the replacement's scan really starts.
	svc.engine.registry = NewRegistry(t.TempDir(), zap.NewNop())
	svc.engine.docker = nil
	// A's preparation captured incarnation 1, then A was removed, B re-added
	// as incarnation 3, and the purge observer ran with no job registered.
	store.setRow("acme", 3)
	svc.InvalidateServerState("acme")

	_, err := svc.engine.StartScan(context.Background(),
		ScanRequest{ServerName: "acme", Incarnation: 1, SourceDir: t.TempDir(), ScanPass: ScanPassSecurityScan}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removed or replaced")
	assert.Nil(t, svc.engine.GetActiveJob("acme"), "the obsolete job holds no active slot")
	assert.Empty(t, base.jobs)

	// B's own scan is not "already in progress".
	job, err := svc.engine.StartScan(context.Background(),
		ScanRequest{ServerName: "acme", Incarnation: 3, SourceDir: t.TempDir(), ScanPass: ScanPassSecurityScan, DryRun: true}, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(3), job.Incarnation)
	require.Eventually(t, func() bool { return svc.engine.GetActiveJob("acme") == nil }, 10*time.Second, 20*time.Millisecond)
}

// purgingStorage simulates a config-watcher removal that purges a server's
// scan jobs right after the startup cleanup listed them.
type purgingStorage struct {
	*rowStorage
	purge       string
	purgeOnCall int // the ListScanJobs call after which the purge runs
	calls       int
}

func (p *purgingStorage) ListScanJobs(serverName string) ([]*ScanJob, error) {
	jobs, err := p.rowStorage.ListScanJobs(serverName)
	out := make([]*ScanJob, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.clone())
	}
	p.calls++
	if p.purge != "" && p.calls == p.purgeOnCall {
		_ = p.DeleteServerScanJobs(p.purge)
		p.rowMu.Lock()
		delete(p.rows, p.purge)
		p.rowMu.Unlock()
	}
	return out, err
}

// The startup stale-job cleanup saves through the incarnation-conditional
// writer: a removal that purged the server between the cleanup's list and its
// save is not undone, a legacy unbound (incarnation 0) job of a removed
// server is not recreated, and a job of a still-configured server is marked
// failed as before.
func TestCleanupStaleJobs_R2_DoesNotRecreatePurgedJobs(t *testing.T) {
	for _, call := range []int{1, 2} {
		t.Run(map[int]string{1: "purge_before_incarnation_read", 2: "purge_after_last_list"}[call], func(t *testing.T) {
			cleanupStaleJobsAfterPurge(t, call)
		})
	}
}

func cleanupStaleJobsAfterPurge(t *testing.T, purgeOnCall int) {
	svc, base, rows := newRowService(t, map[string]int64{"acme": 2, "keep": 4})
	store := &purgingStorage{rowStorage: rows, purge: "acme", purgeOnCall: purgeOnCall}
	svc.storage = store
	now := time.Now()
	require.NoError(t, base.SaveScanJob(&ScanJob{ID: "scan-acme-1", ServerName: "acme", Status: ScanJobStatusRunning, StartedAt: now, Incarnation: 2}))
	require.NoError(t, base.SaveScanJob(&ScanJob{ID: "scan-acme-legacy", ServerName: "acme", Status: ScanJobStatusPending, StartedAt: now}))
	require.NoError(t, base.SaveScanJob(&ScanJob{ID: "scan-keep-1", ServerName: "keep", Status: ScanJobStatusRunning, StartedAt: now, Incarnation: 4}))
	require.NoError(t, base.SaveScanJob(&ScanJob{ID: "scan-keep-legacy", ServerName: "keep", Status: ScanJobStatusRunning, StartedAt: now}))

	svc.CleanupStaleJobs()

	base.mu.Lock()
	defer base.mu.Unlock()
	assert.NotContains(t, base.jobs, "scan-acme-1", "the purged job is not recreated")
	assert.NotContains(t, base.jobs, "scan-acme-legacy", "the purged legacy job is not recreated")
	require.Contains(t, base.jobs, "scan-keep-1")
	assert.Equal(t, ScanJobStatusFailed, base.jobs["scan-keep-1"].Status)
	require.Contains(t, base.jobs, "scan-keep-legacy")
	assert.Equal(t, ScanJobStatusFailed, base.jobs["scan-keep-legacy"].Status)
	assert.Equal(t, int64(4), base.jobs["scan-keep-legacy"].Incarnation, "a legacy job is bound to the incarnation read before the list")
}
