package runtime

import (
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
}

// commitWaitState carries the commit-lock and approval-lock waiter probes and
// the test hooks. Embedded in Runtime.
type commitWaitState struct {
	commitWaiting       atomic.Int32
	approvalLockWaiting sync.Map // server name -> *atomic.Int32
	testHooks           commitTestHooks
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
