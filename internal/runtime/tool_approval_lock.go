package runtime

import (
	"errors"
	"fmt"
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

// toolApprovalServerLock is one server's tool-approval mutex plus its
// inventory generation. generation is bumped on every acquisition through
// lockToolApprovals (every discovery pass and every operator/system write), so
// a decision taken under the lock at generation g is still current exactly
// when the generation still reads g. Both fields are guarded by mu.
type toolApprovalServerLock struct {
	mu         sync.Mutex
	generation uint64
}

func (r *Runtime) toolApprovalLock(serverName string) *toolApprovalServerLock {
	v, _ := r.toolApprovalLocks.LoadOrStore(serverName, &toolApprovalServerLock{})
	return v.(*toolApprovalServerLock)
}

// lockToolApprovals acquires the per-server tool-approval lock, bumps the
// server's inventory generation and returns the unlock function.
func (r *Runtime) lockToolApprovals(serverName string) func() {
	unlock, _ := r.lockToolApprovalsGen(serverName)
	return unlock
}

// lockToolApprovalsGen is lockToolApprovals returning the generation this
// acquisition established. checkToolApprovals records it in its result so the
// removal step of the same discovery pass can tell whether its inventory
// decision is still the latest one (see lockToolApprovalsIfCurrent).
func (r *Runtime) lockToolApprovalsGen(serverName string) (func(), uint64) {
	l := r.toolApprovalLock(serverName)
	l.mu.Lock()
	l.generation++
	return l.mu.Unlock, l.generation
}

// lockToolApprovalsIfCurrent acquires the server's tool-approval lock WITHOUT
// bumping the generation and reports whether the generation still equals gen,
// i.e. no discovery pass and no approval write ran since the pass that
// observed gen. The caller must call unlock either way.
func (r *Runtime) lockToolApprovalsIfCurrent(serverName string, gen uint64) (unlock func(), current bool) {
	l := r.toolApprovalLock(serverName)
	l.mu.Lock()
	return l.mu.Unlock, gen != 0 && l.generation == gen
}

// WithToolApprovalLock runs fn while holding serverName's tool-approval lock.
// fn must not call back into any runtime method that takes the same lock.
func (r *Runtime) WithToolApprovalLock(serverName string, fn func() error) error {
	defer r.lockToolApprovals(serverName)()
	return fn()
}

// CommitServerApprovalDecision is the tool-approval half of a security
// (server) approval, run as ONE operation under the server's tool-approval
// lock (UX-02 cross-review):
//
//  1. With expected non-nil (the review-bound form), every "pending" record
//     that is not in blocked must be in expected with an unchanged
//     CurrentHash; otherwise nothing is written and a
//     *storage.StaleToolReviewError names the changed or unreviewed tools. A
//     tool discovered after the operator's review, or a selected tool whose
//     definition changed since, therefore fails the approval instead of being
//     promoted unseen.
//  2. commit runs: the scanner's integrity baseline (+ atomic tool blocks).
//  3. Every remaining "pending" record not in blocked is promoted to
//     approved (pendingOnly: a "changed" record stays held).
//
// Promoting here — not after the unquarantine — closes the window in which a
// discovery pass between the block commit and the old post-unquarantine
// baseline promotion added a tool that was then promoted unseen. The caller
// must therefore unquarantine with UnquarantineServerKeepingToolDecisions,
// which skips that generic promotion; a tool filed after this commit stays
// pending for review. Returns the number of records promoted.
func (r *Runtime) CommitServerApprovalDecision(serverName string, blocked []string, expected map[string]string, approvedBy string, commit func() error) (int, error) {
	if r.storageManager == nil {
		if commit != nil {
			return 0, commit()
		}
		return 0, nil
	}
	exclude := make(map[string]bool, len(blocked))
	for _, name := range blocked {
		exclude[name] = true
	}
	unlock := r.lockToolApprovals(serverName)
	res, err := r.commitServerApprovalLocked(serverName, exclude, expected, approvedBy, commit)
	unlock()
	approved := 0
	if res != nil {
		approved = len(res.Approved)
	}
	r.notifyToolsApproved(serverName, approved, approvedBy)
	return approved, err
}

func (r *Runtime) commitServerApprovalLocked(serverName string, exclude map[string]bool, expected map[string]string, approvedBy string, commit func() error) (*storage.ToolApprovalApplyResult, error) {
	if expected != nil {
		records, err := r.storageManager.ListToolApprovals(serverName)
		if err != nil {
			return nil, fmt.Errorf("read tool approvals for %s: %w", serverName, err)
		}
		current := make(map[string]*storage.ToolApprovalRecord, len(records))
		var held []string
		for _, record := range records {
			if record.Status == storage.ToolApprovalStatusPending && !exclude[record.ToolName] {
				current[record.ToolName] = record
				held = append(held, record.ToolName)
			}
		}
		if stale := staleReviewedRecords(serverName, held, current, expected); stale != nil {
			return nil, stale
		}
	}
	if commit != nil {
		if err := commit(); err != nil {
			return nil, err
		}
	}
	return r.approvePendingExceptLocked(serverName, exclude, approvedBy)
}

// UnquarantineServerKeepingToolDecisions unquarantines the server exactly as
// QuarantineServer(serverName, false) does, except that it does NOT
// baseline-promote the server's pending tools: CommitServerApprovalDecision
// already made the tool decision for the reviewed snapshot, and anything filed
// since stays pending for review.
func (r *Runtime) UnquarantineServerKeepingToolDecisions(serverName string) error {
	return r.setServerQuarantine(serverName, false, false)
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
