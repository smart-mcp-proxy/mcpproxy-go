package runtime

import (
	"errors"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Per-server serialization of tool-approval read-modify-write (UX-02).
//
// Several discovery triggers (reactive connect, tools/list_changed, the boot
// and post-reload sweeps, HandleUpstreamServerChange, the approval reindex and
// the quarantined-capture path) run checkToolApprovals for the same server at
// once, and operator approvals, blocks and toggles write the same records.
// Each writer reads records, decides, and writes whole records back, so
// without mutual exclusion a stale writer overwrites a fresher record: a
// baseline approval is turned back into "pending", or an operator approval is
// reverted by a pass's "stays pending" write-back. The stranded records then
// persist across reload and restart because no later pass is a baseline pass.
//
// Lock discipline:
//   - One mutex per server, never a global one, so a large server's pass
//     cannot stall approvals on other servers.
//   - The lock covers approval-record reads/writes and the decisions made from
//     them, and (Spec 116 D15) a discovery pass's final eligibility check and
//     its index writes. It is never held across a config commit,
//     HandleUpstreamServerChange, SaveConfiguration, UnquarantineServer or
//     any call that can synchronously reach checkToolApprovals again.
//   - A config commit takes the locks of the servers it changes, sorted, after
//     configCommitMu and before the storage step, and releases them before it
//     touches the upstream manager (Spec 116 plan §Lock order).
//   - It is released before spawning reindexServerToolsAfterApprovalChange,
//     which re-enters checkToolApprovals and takes the lock itself.
//   - Helpers called inside a locked section (recordScanHold,
//     adopt*, stamp*, *Locked) never take it again.
//   - Storage calls inside take the storage manager's own lock; storage never
//     calls back into the runtime, so the order approval lock -> storage lock
//     cannot invert.

// toolApprovalServerLock is one server's tool-approval lock plus the capture
// order state it guards (Spec 117 PR-3, #1565; data-model.md §7).
//
// applied is the highest capture sequence that entered an inventory apply for
// the server (records, deletes, eviction, index, last-good snapshot, StateView
// publish). Every admitted capture advances it, including one whose apply
// later fails and a lenient-empty observation, so it is a fence: an older
// capture that reaches the apply afterwards writes nothing.
//
// definitions is the sequence of the newest admitted capture that carried
// definitions (a non-empty inventory, an authoritative empty one, or a
// quarantined review capture). The retained last-good snapshot may be
// re-applied by operator reconciliation (the approval reindex, the sweep
// fallback) only while its capture is that one (FR-033a, FR-037).
//
// Both fields are guarded by mu. Sequences are process-global (managed.
// InventoryCapture), so a removed and re-added server needs no reset: every
// capture of the new instance is newer.
//
// dirty and dirtyAt are the FR-018 review-dirty state (also guarded by mu):
// set when a capture admitted at dirtyAt failed part-way through its writes,
// cleared only by a complete capture with a newer sequence. dirtyMirror is
// its lock-free copy for dispatch, written only under mu. writeFailures
// counts approval-record write failures of the pass that holds mu.
type toolApprovalServerLock struct {
	mu          sync.Mutex
	applied     uint64
	definitions uint64

	dirty         bool
	dirtyAt       uint64
	dirtyMirror   atomic.Bool
	writeFailures atomic.Int64
}

// toolApprovalServerState returns serverName's lock state, creating it.
func (r *Runtime) toolApprovalServerState(serverName string) *toolApprovalServerLock {
	v, _ := r.toolApprovalLocks.LoadOrStore(serverName, &toolApprovalServerLock{})
	return v.(*toolApprovalServerLock)
}

// lockToolApprovals acquires the per-server tool-approval lock and returns its
// unlock function.
func (r *Runtime) lockToolApprovals(serverName string) func() {
	st := r.toolApprovalServerState(serverName)
	waiting := r.approvalLockWaiter(serverName)
	waiting.Add(1)
	st.mu.Lock()
	waiting.Add(-1)
	return st.mu.Unlock
}

// WithToolApprovalLock runs fn while holding serverName's tool-approval lock.
// It lets writers outside the runtime that touch approval records directly —
// the security scanner's atomic baseline+blocks commit — serialize with
// discovery passes and operator approvals. fn must not call back into any
// runtime method that takes the same lock.
func (r *Runtime) WithToolApprovalLock(serverName string, fn func() error) error {
	defer r.lockToolApprovals(serverName)()
	return fn()
}

// serverQuarantinedInStorage reports whether the server's storage record is
// quarantined. QuarantineServer flips storage (under the approval lock) before
// it republishes the runtime config, so a pass reading only the config could
// act on a stale "trusted" decision and auto-baseline a server that was just
// quarantined. It fails closed: a record that cannot be read or decoded counts
// as quarantined. Only a missing record (storage.ErrUpstreamNotFound — e.g. a
// server known to the config before storage syncs) reports false, leaving the
// config decision to stand.
func (r *Runtime) serverQuarantinedInStorage(serverName string) bool {
	if r.storageManager == nil {
		return false
	}
	sc, err := r.storageManager.GetUpstreamServer(serverName)
	switch {
	case err == nil:
		return sc != nil && sc.Quarantined
	case errors.Is(err, storage.ErrUpstreamNotFound):
		return false
	default:
		r.logger.Warn("Cannot read server quarantine state from storage; treating the server as quarantined for tool approval",
			zap.String("server", serverName), zap.Error(err))
		return true
	}
}
