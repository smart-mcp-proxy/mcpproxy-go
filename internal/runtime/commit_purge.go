package runtime

import (
	"sort"
	"sync"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 116 PR2 (FR-012): the in-memory half of the removal purge. The storage
// step deleted the server's purge set in the row's bbolt transaction; this
// drops the per-server state the runtime keeps in memory, so a same-name
// re-add finds nothing of the old server anywhere.

// commitMemoryPurgeHeld runs in commit step 6 with the approval locks of the
// purged servers held (discovery writes the last-good snapshot under the same
// lock, D15): it drops the last-good tool snapshot and the capture stamp, so
// the sweep fallback can never reapply the removed server's tools to a
// replacement (T2.10).
func (r *Runtime) commitMemoryPurgeHeld(reports map[string]storage.PurgeReport) {
	if len(reports) == 0 {
		return
	}
	r.lastGoodToolsMu.Lock()
	if r.lastGoodPurgeSeq == nil {
		r.lastGoodPurgeSeq = make(map[string]uint64)
	}
	for name := range reports {
		delete(r.lastGoodTools, name)
		delete(r.lastCaptureAt, name)
		// A local reindex that already copied the snapshot sees the bump
		// under the approval lock and drops its copy (review round 3).
		r.lastGoodPurgeSeq[name]++
	}
	r.lastGoodToolsMu.Unlock()
}

// lastGoodSnapshotWithSeq returns a copy of serverName's last-good snapshot
// and the server's purge sequence, read together under lastGoodToolsMu.
func (r *Runtime) lastGoodSnapshotWithSeq(serverName string) ([]*config.ToolMetadata, uint64) {
	r.lastGoodToolsMu.RLock()
	defer r.lastGoodToolsMu.RUnlock()
	seq := r.lastGoodPurgeSeq[serverName]
	snapshot := r.lastGoodTools[serverName]
	if len(snapshot) == 0 {
		return nil, seq
	}
	cp := make([]*config.ToolMetadata, len(snapshot))
	copy(cp, snapshot)
	return cp, seq
}

// lastGoodPurgedSince reports whether serverName's snapshot was purged after
// the caller read purge sequence seq. Callers hold serverName's approval lock,
// so the answer cannot change before their index write.
func (r *Runtime) lastGoodPurgedSince(serverName string, seq uint64) bool {
	r.lastGoodToolsMu.RLock()
	defer r.lastGoodToolsMu.RUnlock()
	return r.lastGoodPurgeSeq[serverName] != seq
}

// commitDropPurgedSchedules runs in commit step 6, after the approval locks
// are released and still under configCommitMu (review round 2): it cancels
// the OAuth refresh schedule and drops the inspection exemption of every
// purged server. Doing it inside the commit, not after the commit lock is
// released, means no later commit can have re-added the name and given the
// replacement a fresh schedule or exemption that a name-keyed clear would
// destroy. Both are leaf locks (RefreshManager.mu, the supervisor's
// exemption mutex) that never call back into the runtime, so this adds only
// L0 → leaf edges. It reports whether an exemption was dropped; the
// supervisor's reconcile for it runs after the commit lock is released.
func (r *Runtime) commitDropPurgedSchedules(reports map[string]storage.PurgeReport) (exemptionsDropped bool) {
	for name := range reports {
		if r.refreshManager != nil {
			r.refreshManager.OnTokenCleared(name)
		}
		if r.supervisor != nil && r.supervisor.DropInspectionExemption(name) {
			exemptionsDropped = true
		}
	}
	return exemptionsDropped
}

// afterServerPurge runs after the commit lock is released: it reconciles the
// supervisor when the commit dropped an inspection exemption, evicts signature
// cache entries no indexed tool backs any more, and notifies the purge
// observers (the scanner's verdict cache, which is incarnation-aware). It
// clears no per-server state by name: a later commit may already have
// re-added the name (review round 2). Each step is idempotent.
func (r *Runtime) afterServerPurge(reports map[string]storage.PurgeReport, exemptionsDropped bool) {
	if len(reports) == 0 {
		return
	}
	names := make([]string, 0, len(reports))
	for name := range reports {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r.logger.Info("Removed server state purged",
			zap.String("server", name), zap.Any("deleted", reports[name].Deleted))
	}
	if exemptionsDropped && r.supervisor != nil {
		r.supervisor.ReconcileAfterExemptionChange()
	}
	r.reconcileSignatureCache()
	r.purgeObserversMu.RLock()
	observers := append([]func([]string){}, r.purgeObservers...)
	r.purgeObserversMu.RUnlock()
	for _, fn := range observers {
		fn(names)
	}
}

// purgeObserverState is embedded in Runtime.
type purgeObserverState struct {
	purgeObserversMu sync.RWMutex
	purgeObservers   []func(names []string)
}

// AddServerPurgeObserver registers fn to run after a commit purged servers
// (removed or replaced), outside every runtime lock. The security scanner uses
// it to drop its cached verdict of a removed server (FR-014a).
func (r *Runtime) AddServerPurgeObserver(fn func(names []string)) {
	if fn == nil {
		return
	}
	r.purgeObserversMu.Lock()
	r.purgeObservers = append(r.purgeObservers, fn)
	r.purgeObserversMu.Unlock()
}
