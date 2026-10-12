package scanner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 116 PR2 review round 3 (Sol): the summary cache and the purge
// observer's active-job check are bound to the server's incarnation.

// A cached verdict of the removed server is not served for a same-name
// replacement in the window before the purge observer runs: the commit
// already purged the stored scan jobs, and the cache entry belongs to the old
// incarnation.
func TestScanSummary_R3_CacheBoundToIncarnation(t *testing.T) {
	svc, base, store := newRowService(t, map[string]int64{"acme": 1})
	now := time.Now()
	base.mu.Lock()
	base.jobs["scan-acme-A"] = &ScanJob{ID: "scan-acme-A", ServerName: "acme", Status: ScanJobStatusCompleted,
		ScanPass: ScanPassSecurityScan, StartedAt: now, CompletedAt: now, Incarnation: 1}
	base.mu.Unlock()
	first := svc.GetScanSummary(context.Background(), "acme")
	require.NotNil(t, first, "the old server's verdict is computed and cached")

	// The removal commit purged A's jobs and a re-add committed B as
	// incarnation 3; the purge observer has not run yet.
	base.mu.Lock()
	delete(base.jobs, "scan-acme-A")
	base.mu.Unlock()
	store.setRow("acme", 3)

	assert.Nil(t, svc.GetScanSummary(context.Background(), "acme"),
		"the replacement must not be shown the removed server's cached verdict")
}

// staleOnceProvider answers its first ServerIncarnation call with the view a
// delayed purge observer had (the server removed) and, when the caller does
// not hold the engine lock, lets the re-add and the replacement's scan
// registration land before the caller proceeds.
type staleOnceProvider struct {
	*rowUnquarantiner
	once    sync.Once
	onStale func()
}

func (p *staleOnceProvider) ServerIncarnation(name string) (int64, bool) {
	stale := false
	p.once.Do(func() { stale = true })
	if stale {
		p.onStale()
		return 0, false
	}
	return p.rowUnquarantiner.ServerIncarnation(name)
}

// A delayed purge observer must not detach the replacement's own active scan:
// it decides obsolescence from the incarnation current while it holds the
// engine lock, not from one read before it. Otherwise the replacement's scan
// runs invisible to GetActiveJob and CancelScan, and a second scan can start.
func TestInvalidateServerState_R3_KeepsReplacementScanRegisteredLate(t *testing.T) {
	svc, _, store := newRowService(t, map[string]int64{})
	jobB := &ScanJob{ID: "scan-acme-B", ServerName: "acme", Status: ScanJobStatusRunning, StartedAt: time.Now(), Incarnation: 3}
	registerB := func() {
		store.setRow("acme", 3)
		svc.engine.mu.Lock()
		svc.engine.activeScans["acme"] = jobB
		svc.engine.mu.Unlock()
	}
	registered := false
	svc.SetServerUnquarantiner(&staleOnceProvider{
		rowUnquarantiner: &rowUnquarantiner{store: store},
		onStale: func() {
			if svc.engine.mu.TryLock() {
				svc.engine.mu.Unlock()
				registerB()
				registered = true
			}
		},
	})

	svc.InvalidateServerState("acme")
	if !registered {
		// The observer read nothing outside the engine lock; the re-add
		// lands after it.
		registerB()
	}
	active := svc.engine.GetActiveJob("acme")
	require.NotNil(t, active, "the replacement's scan keeps its active slot")
	assert.Equal(t, "scan-acme-B", active.ID)
}
