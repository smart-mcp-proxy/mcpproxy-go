package scanner

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sol round 2, finding 1 (reprotect): the quarantine decision and the job
// registration must be one step under the server's approval lock, the lock a
// quarantine mutation holds while it flips the stored flag. Checking before
// the contracts and peers are collected leaves a window in which a server
// quarantined meanwhile still gets a cached scan job (Spec 101 tasks B17).

// r2ApprovalLockUnquarantiner is a ServerUnquarantiner with the optional approval
// lock capability, backed by a real mutex standing in for the runtime's
// per-server approval lock.
type r2ApprovalLockUnquarantiner struct {
	mu   sync.Mutex
	held atomic.Bool
}

func (u *r2ApprovalLockUnquarantiner) UnquarantineServer(string) error { return nil }
func (u *r2ApprovalLockUnquarantiner) CaptureApprovalIncarnation(_ string, expected int64) (int64, error) {
	return expected, nil
}
func (u *r2ApprovalLockUnquarantiner) UnquarantineServerAt(string, int64) error { return nil }
func (u *r2ApprovalLockUnquarantiner) WithServerApprovalLockAt(_ string, _ int64, fn func() error) error {
	u.mu.Lock()
	u.held.Store(true)
	defer func() {
		u.held.Store(false)
		u.mu.Unlock()
	}()
	return fn()
}

// flipProvider reports a quarantine flag the test can flip, and records
// whether each quarantine read ran under the approval lock.
type flipProvider struct {
	*cachedScanProvider
	lock        *r2ApprovalLockUnquarantiner
	quarantined atomic.Bool
	onRead      func()
	readsInLock []bool
	mu          sync.Mutex
}

func (p *flipProvider) IsServerQuarantined(string) bool {
	p.mu.Lock()
	p.readsInLock = append(p.readsInLock, p.lock.held.Load())
	p.mu.Unlock()
	return p.quarantined.Load()
}

func (p *flipProvider) GetCurrentToolContracts(name string) ([]map[string]interface{}, error) {
	if p.onRead != nil {
		p.onRead()
	}
	return p.cachedScanProvider.GetCurrentToolContracts(name)
}

// lockObservingStorage records whether the job record was written (the engine
// registers the job and fires OnScanStarted, which saves it) with the approval
// lock held.
type lockObservingStorage struct {
	*mockStorage
	lock         *r2ApprovalLockUnquarantiner
	savedInLock  atomic.Int32
	savedOutside atomic.Int32
	first        atomic.Bool
}

func (s *lockObservingStorage) SaveScanJob(job *ScanJob) error {
	// Only the first save is the registration (OnScanStarted, synchronous in
	// the engine's startScan); later running-state saves come from the scan
	// goroutine.
	if job.Status == ScanJobStatusRunning && s.first.CompareAndSwap(false, true) {
		if s.lock.held.Load() {
			s.savedInLock.Add(1)
		} else {
			s.savedOutside.Add(1)
		}
	}
	return s.mockStorage.SaveScanJob(job)
}

func TestStartScanFromCachedContracts_R2_QuarantineFlippedWhileContractsAreReadGetsNoJob(t *testing.T) {
	lock := &r2ApprovalLockUnquarantiner{}
	base := &cachedScanProvider{info: &ServerInfo{Name: "srv", Protocol: "stdio"}, approved: approvedPair()}
	provider := &flipProvider{cachedScanProvider: base, lock: lock}
	svc, mock, _ := newCachedScanService(t, provider)
	svc.SetServerUnquarantiner(lock)
	// The operator quarantines the server after the early guard passed, while
	// the cached contracts are being collected. The incarnation is unchanged.
	provider.onRead = func() { provider.quarantined.Store(true) }

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)

	require.ErrorIs(t, err, ErrServerQuarantined, "a server quarantined before registration must not get a cached scan job")
	assert.Nil(t, job)
	jobs, _ := mock.ListScanJobs("srv")
	assert.Empty(t, jobs, "no job record for a server quarantined in the window")
}

func TestStartScanFromCachedContracts_R2_QuarantineCheckAndRegistrationShareTheApprovalLock(t *testing.T) {
	lock := &r2ApprovalLockUnquarantiner{}
	base := &cachedScanProvider{info: &ServerInfo{Name: "srv", Protocol: "stdio"}, approved: approvedPair()}
	provider := &flipProvider{cachedScanProvider: base, lock: lock}
	svc, mock, _ := newCachedScanService(t, provider)
	store := &lockObservingStorage{mockStorage: mock, lock: lock}
	svc.storage = store
	svc.SetServerUnquarantiner(lock)

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)
	require.NoError(t, err)
	waitCachedScan(t, mock, job.ID)

	provider.mu.Lock()
	reads := append([]bool(nil), provider.readsInLock...)
	provider.mu.Unlock()
	require.NotEmpty(t, reads)
	assert.True(t, reads[len(reads)-1], "the authoritative quarantine read runs under the approval lock")
	assert.Equal(t, int32(1), store.savedInLock.Load(), "the job is registered while the same lock is held")
	assert.Zero(t, store.savedOutside.Load())
}
