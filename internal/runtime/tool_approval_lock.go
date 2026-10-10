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

// toolApprovalServerLock is one server's tool-approval mutex plus two
// inventory tickets (see nextInventoryTicket), both guarded by mu:
//
//   - appliedInventory is the highest ticket a discovery pass has applied for
//     the server. The ticket is taken when the inventory is CAPTURED (by the
//     leader of that server's tools/list), so freshness follows capture order,
//     not the order in which passes happen to reach the lock: an older
//     inventory that acquires the lock after a newer one was applied is stale
//     and is dropped (UX-02 cross-review).
//   - publishedInventory is the highest ticket published to the StateView.
//     A pass publishes only while its ticket is still the newest applied and
//     published one, so a stale or overtaken inventory can never replace the
//     tool definitions and safety hints (which set the dispatch tier) of a
//     newer one (UX-02 cross-review).
//
// A discovery pass applies its inventory — the approval decisions AND the
// search-index writes derived from them — in ONE critical section under mu
// (applyInventory), so no operator approval, block or other pass can land
// between the decision and the index write and be undone by it.
type toolApprovalServerLock struct {
	mu                 sync.Mutex
	appliedInventory   uint64
	publishedInventory uint64
}

func (r *Runtime) toolApprovalLock(serverName string) *toolApprovalServerLock {
	v, _ := r.toolApprovalLocks.LoadOrStore(serverName, &toolApprovalServerLock{})
	return v.(*toolApprovalServerLock)
}

// nextInventoryTicket returns a new inventory ticket. Discovery passes do not
// call it themselves before listing: they pass it to
// managed.Client.ListToolsTicketed (the sweep through
// DiscoverToolsReportTicketed), which calls it from the caller that LEADS the
// actual upstream tools/list, once that caller owns the list — so a pass that
// stalls before reaching the list, or is coalesced onto another caller's list,
// carries the ticket of the capture it really returns (UX-02 cross-review r4).
// Hand the ticket to applyDifferentialToolUpdateCaptured /
// checkToolApprovalsCaptured with that inventory. Tickets are global and
// strictly increasing, and one client's lists are serialized, so a later
// capture always carries a larger ticket.
func (r *Runtime) nextInventoryTicket() uint64 {
	return r.inventoryTickets.Add(1)
}

// lockToolApprovals acquires the per-server tool-approval lock and returns
// the unlock function.
func (r *Runtime) lockToolApprovals(serverName string) func() {
	l := r.toolApprovalLock(serverName)
	l.mu.Lock()
	return l.mu.Unlock
}

// lockToolApprovalsForInventory is lockToolApprovals for a discovery pass
// applying the inventory captured under ticket. It reports whether the
// inventory is stale: a later-captured inventory was already applied for the
// server. A stale pass must not write anything (its view predates one already
// reconciled); a current one records its ticket as the newest applied
// inventory. The caller must call unlock either way.
func (r *Runtime) lockToolApprovalsForInventory(serverName string, ticket uint64) (unlock func(), stale bool) {
	l := r.toolApprovalLock(serverName)
	l.mu.Lock()
	if ticket < l.appliedInventory {
		stale = true
	} else {
		l.appliedInventory = ticket
	}
	return l.mu.Unlock, stale
}

// publishInventoryIfCurrent runs publish — the StateView write of the
// inventory captured under ticket — under the server's tool-approval lock,
// but only while that inventory is still the newest applied and published
// one. It reports whether publish ran. An inventory dropped as stale, or
// overtaken by a newer applied inventory before it reached this point, is
// not published: the newer pass publishes its own (UX-02 cross-review).
// publish must not call back into anything that takes the same lock.
func (r *Runtime) publishInventoryIfCurrent(serverName string, ticket uint64, publish func()) bool {
	l := r.toolApprovalLock(serverName)
	l.mu.Lock()
	defer l.mu.Unlock()
	if ticket < l.appliedInventory || ticket < l.publishedInventory {
		return false
	}
	l.publishedInventory = ticket
	publish()
	return true
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
//     and every enabled "approved" record that is not in blocked must be in
//     expected with an unchanged review fingerprint (definition hash + safety
//     hints); otherwise nothing is written and a
//     *storage.StaleToolReviewError names the changed or unreviewed tools. A
//     tool discovered after the operator's review, or a selected tool whose
//     definition changed since, therefore fails the approval instead of being
//     promoted unseen.
//  2. The server's tool-baseline decision is recorded durably
//     (storage.ToolBaselineDecisionsBucket), so a tool first served after
//     this approval is held for review even when the reviewed inventory was
//     empty and no record was approved.
//  3. commit runs: the scanner's integrity baseline (+ atomic tool blocks).
//  4. Every remaining "pending" record not in blocked is promoted to
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
			if exclude[record.ToolName] {
				continue
			}
			// Pending tools are promoted by this approval; approved, enabled
			// ones are activated by the unquarantine that follows it. Both
			// must still be what the operator reviewed — an approved tool
			// whose safety hints changed since (annotations are not part of
			// the approval hash, so it stays "approved") is out of date too
			// (UX-02 cross-review r4).
			activates := record.Status == storage.ToolApprovalStatusPending ||
				(record.Status == storage.ToolApprovalStatusApproved && !record.Disabled)
			if activates {
				current[record.ToolName] = record
				held = append(held, record.ToolName)
			}
		}
		if stale := staleReviewedRecords(serverName, held, current, expected); stale != nil {
			return nil, stale
		}
	}
	// Record the baseline decision durably before anything else is written:
	// an approval of an empty (or fully blocked) snapshot leaves no approved
	// record, and later tools must still be held rather than auto-baselined
	// (UX-02 cross-review r5). Marking first is fail-safe — a commit that
	// then fails only leaves the server more conservative.
	if err := r.storageManager.MarkToolBaselineDecided(serverName); err != nil {
		return nil, fmt.Errorf("record tool baseline decision for %s: %w", serverName, err)
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

// serverQuarantinedForIndexLocked reports whether the server must be kept out
// of the search index: quarantined in the runtime config or in storage
// (failing closed). The caller holds the server's tool-approval lock, under
// which every quarantine flip writes storage, so a pass cannot act on a
// trusted view older than the flip (UX-02 cross-review r5).
func (r *Runtime) serverQuarantinedForIndexLocked(serverName string) bool {
	return r.resolveToolQuarantineGate(serverName).serverQuarantined || r.serverQuarantinedInStorage(serverName)
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
