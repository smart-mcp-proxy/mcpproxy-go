package runtime

import (
	"sort"
	"sync"

	"go.uber.org/zap"

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
	for name := range reports {
		delete(r.lastGoodTools, name)
		delete(r.lastCaptureAt, name)
	}
	r.lastGoodToolsMu.Unlock()
}

// afterServerPurge runs after the commit lock is released: it cancels the
// OAuth refresh schedule, revokes an inspection exemption, evicts signature
// cache entries no indexed tool backs any more, and notifies the purge
// observers (the scanner's verdict cache). Each step is idempotent.
func (r *Runtime) afterServerPurge(reports map[string]storage.PurgeReport) {
	if len(reports) == 0 {
		return
	}
	names := make([]string, 0, len(reports))
	for name := range reports {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if r.refreshManager != nil {
			r.refreshManager.OnTokenCleared(name)
		}
		if r.supervisor != nil {
			r.supervisor.RevokeInspectionExemption(name)
		}
		r.logger.Info("Removed server state purged",
			zap.String("server", name), zap.Any("deleted", reports[name].Deleted))
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
