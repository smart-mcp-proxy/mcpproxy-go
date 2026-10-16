package runtime

import (
	"errors"
	"sync"
	"sync/atomic"
)

// commitTestHooks are the Spec 116 test seams (tasks.md T001). Every field is
// nil in production. Tests set them before driving the runtime and use them to
// park a real production path at an exact window, then release it once a
// competing writer has either returned or been observed blocked
// (quickstart.md "Two test shapes").
type commitTestHooks struct {
	// reconcileEntry runs where a reconcile of the server set against storage
	// starts: the entry of the reconcile step inside a commit (Spec 116), or
	// LoadConfiguredServers' entry before it.
	reconcileEntry func()
	// reconcileExit runs where that reconcile has finished writing storage.
	reconcileExit func()
	// discoveryBeforeIndex runs in a discovery pass after its approval check
	// and before its index write.
	discoveryBeforeIndex func(serverName string)
	// discoveryAfterList runs in a single-server discovery pass right after
	// its tools/list returned and before the result is committed (review
	// round 7: a list that outlives a connection-changing commit).
	discoveryAfterList func(serverName string)
	// toolBaselineFault injects a storage failure into the Spec 117 PR-1
	// baseline step of a discovery pass: op is "list" (the prior-record
	// listing), "read" (the marker read) or "write" (the marker write).
	toolBaselineFault func(serverName, op string) error
	// toolApprovalWriteFault injects a storage failure into an approval
	// record write of the runtime's write seam (op "save") or into a delete
	// of the absent-tool removal (op "delete-record", "delete-hash",
	// "delete-index"), for Spec 117 FR-018/FR-032a tests.
	toolApprovalWriteFault func(serverName, toolName, op string) error
	// toolIndexWriteFault injects a failure into a discovery pass's index
	// writes (BatchIndexTools), for Spec 117 FR-018 tests.
	toolIndexWriteFault func(serverName string) error
	// toolIndexReadFault injects a failure into a discovery pass's read of
	// the server's indexed tools (the full-reindex fallback).
	toolIndexReadFault func(serverName string) error
	// discoveryPublishFault replaces the StateView publish result of a
	// discovery pass (FR-018 publication failure or rejection).
	discoveryPublishFault func(serverName string) (published bool, err error)
	// reviewBindingValidated runs in a review-bound tool approval
	// (ApproveToolsReviewed, ApproveAllToolsReviewed) after the binding
	// validated and before the approval writes, with the server's
	// tool-approval lock held (Spec 117 PR-2 r2).
	reviewBindingValidated func(serverName string)
	// inventoryBeforeApplyLock runs in every inventory write path (sweep,
	// single-server discovery, quarantined review capture, lenient empty
	// observation) after the tools/list returned and before the apply takes
	// captureSnapMu and the server's approval lock, with the capture's
	// sequence (Spec 117 PR-3).
	inventoryBeforeApplyLock func(serverName string, seq uint64)

	// Commit pipeline seams (plan.md §Test seams). Each runs inside
	// configCommitMu with the reason of the commit.
	afterBuild   func(reason string)
	afterPrepare func(reason string)
	afterDisk    func(reason string)
	afterStorage func(reason string)
	afterAdvance func(reason string)
	// beforeAfterCommit runs in Commit after configCommitMu is released and
	// before the commit's after-commit work (review round 2).
	beforeAfterCommit func(reason string)
	// diskWriteFault / storageFault inject a failure into commit step 5 or
	// into the storage transaction of step 6.
	diskWriteFault func(reason string) error
	storageFault   func(reason string) error
	// storedReadFault injects a failure into the commit's read of the stored
	// view (ListStoredServers) for a commit with the given reason.
	storedReadFault func(reason string) error
	// skipAdvance stubs commit step 7 out, so a regression test can show it
	// detects the loss of the retained-instance update (T1.19).
	skipAdvance bool

	// Spec 116 PR2 approval seams (plan.md §Test seams).
	//
	// approvalAfterCapture runs in every approval writer right after it
	// captured the server's incarnation and before it takes the server's
	// approval lock (T2.4, T2.17).
	approvalAfterCapture func(serverName string, incarnation int64)
	// approvalAfterUnquarantineCommit runs in QuarantineServer(name, false)
	// after its commit returned and before the baseline promotion, with the
	// incarnation of that commit (T2.19, review R9.5).
	approvalAfterUnquarantineCommit func(serverName string, incarnation int64)
	// beforeRegisterIdentity runs in a post-commit connect worker before it
	// registers the server identity (T2.12).
	beforeRegisterIdentity func(serverName string)
	// serverViewAfterRead runs in GetServerReview and GetAllServers right
	// after they read the server data they display (review round 1: the
	// displayed config and the incarnation come from one snapshot).
	serverViewAfterRead func()
}

// workerActHooks are the Spec 116 PR3 worker seams (plan.md §Test seams).
// before runs in a post-commit connect or disconnect worker, and in
// RestartServer's asynchronous worker, before it acts on the manager, with
// the commit generation the worker is bound to (T3.1, T3.2, T3.4). after runs
// when that worker has finished acting. Either may be nil.
type workerActHooks struct {
	before func(serverName string, gen int64)
	after  func(serverName string, gen int64)
}

// workerActEnter runs the before seam and returns the after seam to defer
// (a no-op when none is installed). Production: one atomic load.
func (r *Runtime) workerActEnter(serverName string, gen int64) func() {
	h := r.workerHooks.Load()
	if h == nil {
		return func() {}
	}
	if h.before != nil {
		h.before(serverName, gen)
	}
	if h.after == nil {
		return func() {}
	}
	return func() { h.after(serverName, gen) }
}

// SetWorkerActHooksForTest installs the PR3 worker seams. Test-only; packages
// outside runtime (internal/server) hold back a commit's worker with it.
func (r *Runtime) SetWorkerActHooksForTest(before, after func(serverName string, gen int64)) {
	if before == nil && after == nil {
		r.workerHooks.Store(nil)
		return
	}
	r.workerHooks.Store(&workerActHooks{before: before, after: after})
}

// commitWaitState carries the commit-lock and approval-lock waiter probes and
// the test hooks. Embedded in Runtime.
type commitWaitState struct {
	commitWaiting       atomic.Int32
	approvalLockWaiting sync.Map // server name -> *atomic.Int32
	testHooks           commitTestHooks
	// serversChangedHook observes every servers.changed submission (reason
	// and extra) before the coalescer merges them (review R2.5). Test seam,
	// nil in production; atomic because background emitters may run while a
	// test sets it, and outside commitTestHooks because that struct is copied.
	serversChangedHook atomic.Pointer[func(reason string, extra map[string]any)]
	// workerHooks holds the PR3 worker seams (atomic: workers read them
	// while a test installs them).
	workerHooks atomic.Pointer[workerActHooks]
}

// lockConfigCommit takes configCommitMu and counts the goroutines waiting for
// it, so exclusion tests can observe a competitor that is blocked on the
// commit lock instead of sleeping (commitWaiters).
func (r *Runtime) lockConfigCommit() {
	r.commitWaiting.Add(1)
	r.configCommitMu.Lock()
	r.commitWaiting.Add(-1)
}

// commitWaiters reports how many goroutines are blocked on configCommitMu.
// Test probe only.
func (r *Runtime) commitWaiters() int {
	return int(r.commitWaiting.Load())
}

// approvalLockWaiter returns the waiter counter for one server's approval lock.
func (r *Runtime) approvalLockWaiter(serverName string) *atomic.Int32 {
	v, _ := r.approvalLockWaiting.LoadOrStore(serverName, &atomic.Int32{})
	return v.(*atomic.Int32)
}

// approvalLockWaiters reports how many goroutines are blocked on serverName's
// approval lock. Test probe only.
func (r *Runtime) approvalLockWaiters(serverName string) int {
	return int(r.approvalLockWaiter(serverName).Load())
}

// SetReconcileEntryHookForTest installs the reconcileEntry seam. Test-only:
// packages outside runtime (internal/server) drive the same interleavings.
func (r *Runtime) SetReconcileEntryHookForTest(fn func()) {
	r.testHooks.reconcileEntry = fn
}

// SetReconcileExitHookForTest installs the reconcileExit seam. Test-only.
func (r *Runtime) SetReconcileExitHookForTest(fn func()) {
	r.testHooks.reconcileExit = fn
}

// SetDiscoveryBeforeIndexHookForTest installs the discoveryBeforeIndex seam.
// Test-only.
func (r *Runtime) SetDiscoveryBeforeIndexHookForTest(fn func(serverName string)) {
	r.testHooks.discoveryBeforeIndex = fn
}

// CommitWaitersForTest exposes commitWaiters to tests in other packages.
func (r *Runtime) CommitWaitersForTest() int { return r.commitWaiters() }

// ApprovalLockWaitersForTest exposes approvalLockWaiters to tests in other
// packages.
func (r *Runtime) ApprovalLockWaitersForTest(serverName string) int {
	return r.approvalLockWaiters(serverName)
}

// SetApprovalAfterCaptureHookForTest installs the approvalAfterCapture seam.
// Test-only: the prompt approval writers in internal/server capture through
// the runtime.
func (r *Runtime) SetApprovalAfterCaptureHookForTest(fn func(serverName string, incarnation int64)) {
	r.testHooks.approvalAfterCapture = fn
}

// MarkToolReviewDirtyForTest marks serverName review-dirty at seq, as a
// capture whose writes failed would (Spec 117 FR-018). Test-only: packages
// outside runtime (internal/server) assert the dispatch refusal with it.
func (r *Runtime) MarkToolReviewDirtyForTest(serverName string, seq uint64) {
	defer r.lockToolApprovals(serverName)()
	r.markReviewDirtyHeld(serverName, seq, errTestReviewDirty)
}

// ClearToolReviewDirtyForTest clears serverName's dirty mark as a complete
// capture newer than seq would. Test-only.
func (r *Runtime) ClearToolReviewDirtyForTest(serverName string, seq uint64) {
	defer r.lockToolApprovals(serverName)()
	r.clearReviewDirtyHeld(serverName, seq)
}

var errTestReviewDirty = errors.New("test: capture marked incomplete")
