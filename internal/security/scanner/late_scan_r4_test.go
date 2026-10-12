package scanner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 116 PR2 review round 4 (Sol, finding 2): the removed server's active
// scan is not the replacement's. Until the delayed purge observer detaches
// it, A's job still holds the name's activeScans slot; B (a same-name re-add,
// incarnation 3) must neither be shown A's job nor be refused its own scan.
func TestActiveJob_R4_ReplacementDoesNotInheritRemovedScan(t *testing.T) {
	svc, _, store := newRowService(t, map[string]int64{"acme": 1})
	jobA := &ScanJob{ID: "scan-acme-A", ServerName: "acme", Status: ScanJobStatusRunning,
		ScanPass: ScanPassSecurityScan, StartedAt: time.Now(), Incarnation: 1}
	svc.engine.mu.Lock()
	svc.engine.activeScans["acme"] = jobA
	svc.engine.mu.Unlock()
	require.NotNil(t, svc.engine.GetActiveJob("acme"), "A's own scan is active while A is current")

	// The removal and re-add committed; A's purge observer has not run.
	store.setRow("acme", 3)

	assert.Nil(t, svc.engine.GetActiveJob("acme"), "B must not see A's active job")
	if sum := svc.GetScanSummary(context.Background(), "acme"); sum != nil {
		assert.NotEqual(t, "scanning", sum.Status, "B's summary must not show A's scan")
	}
	if st, err := svc.GetScanStatus(context.Background(), "acme"); err == nil && st != nil {
		assert.NotEqual(t, "scan-acme-A", st.ID, "B's status must not return A's job")
	}
	assert.Error(t, svc.engine.CancelScan("acme"), "cancelling B's scan must not cancel A's")
	assert.Equal(t, ScanJobStatusRunning, jobA.Status)

	// B's registration gets past the slot check (the test registry has no
	// runnable scanner, so it stops later, at scanner resolution).
	_, err := svc.engine.StartScan(context.Background(), ScanRequest{
		ServerName: "acme", Incarnation: 3, ScanPass: ScanPassSecurityScan, SourceDir: t.TempDir(),
	}, nil)
	if err != nil {
		assert.NotContains(t, err.Error(), "already in progress", "B must be able to register its own scan")
	}
	svc.engine.mu.Lock()
	held := svc.engine.activeScans["acme"] == jobA
	svc.engine.mu.Unlock()
	assert.False(t, held, "A's job must not keep the slot B registers in")

	// B's own scan holds the slot; A's later cleanup leaves it alone.
	jobB := &ScanJob{ID: "scan-acme-B", ServerName: "acme", Status: ScanJobStatusRunning,
		ScanPass: ScanPassSecurityScan, StartedAt: time.Now(), Incarnation: 3}
	svc.engine.mu.Lock()
	svc.engine.activeScans["acme"] = jobB
	svc.engine.mu.Unlock()
	svc.engine.clearActiveJob("acme", jobA)
	svc.InvalidateServerState("acme")
	active := svc.engine.GetActiveJob("acme")
	require.NotNil(t, active, "A's cleanup must not evict B's scan")
	assert.Equal(t, "scan-acme-B", active.ID)
}
