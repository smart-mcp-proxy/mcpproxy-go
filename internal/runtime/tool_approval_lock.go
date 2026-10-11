package runtime

import (
	"errors"
	"sync"

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
//     them, nothing else. It is never held across LoadConfiguredServers,
//     HandleUpstreamServerChange, SaveConfiguration, UnquarantineServer or
//     any call that can synchronously reach checkToolApprovals again.
//   - It is released before spawning reindexServerToolsAfterApprovalChange,
//     which re-enters checkToolApprovals and takes the lock itself.
//   - Helpers called inside a locked section (recordScanHold,
//     scanApproveChange, adopt*, stamp*, *Locked) never take it again.
//   - Storage calls inside take the storage manager's own lock; storage never
//     calls back into the runtime, so the order approval lock -> storage lock
//     cannot invert.

// lockToolApprovals acquires the per-server tool-approval lock and returns its
// unlock function.
func (r *Runtime) lockToolApprovals(serverName string) func() {
	v, _ := r.toolApprovalLocks.LoadOrStore(serverName, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
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

// deleteToolApprovalLocked deletes a removed tool's approval record under the
// server's tool-approval lock, so the delete cannot interleave with another
// writer's read-modify-write of the same record.
func (r *Runtime) deleteToolApprovalLocked(serverName, toolName string) error {
	defer r.lockToolApprovals(serverName)()
	return r.storageManager.DeleteToolApproval(serverName, toolName)
}
