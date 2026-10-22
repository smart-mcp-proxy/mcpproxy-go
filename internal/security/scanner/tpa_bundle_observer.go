package scanner

import (
	"sync"
	"time"
)

// Bundle observers (Spec 101 US3 tasks B17, B21).

// BundleActivation is the payload handed to activation observers after a
// history row was written (an activation or a rollback). It carries no bundle
// bytes and no patterns.
type BundleActivation struct {
	// Digest is the full SHA-256 of the newly serving bundle.
	Digest string
	// Sequence is the decimal-string manifest sequence ("" when none).
	Sequence string
	// Authority is the signing authority ("publisher" or "operator").
	Authority string
	// Source is BundleSourceFile or BundleSourceFetch.
	Source string
	// Rollback is true when the activation was an explicit rollback.
	Rollback bool
	// PreviousDigest is the digest that served before ("" for the embedded
	// default or a first activation).
	PreviousDigest string
	// ActivatedAt is the history row's timestamp.
	ActivatedAt time.Time
	// Seq orders activations of this process: assigned (1, 2, ...) in commit
	// order as the event is dispatched. Observers run on separate goroutines,
	// so arrival order is not commit order; an observer that must not act on a
	// stale event compares Seq. Zero means "not a dispatched event" (a
	// synthetic catch-up activation).
	Seq uint64
}

// observers is the process-wide observer registry. Dispatch counts in-flight
// calls so a test (and Server.Shutdown) can wait for them.
var observers struct {
	mu       sync.Mutex
	cond     *sync.Cond
	next     uint64
	seq      uint64
	inflight int
	activate map[uint64]func(BundleActivation)
	refresh  func(RefreshOutcomeClass)
}

func init() { observers.cond = sync.NewCond(&observers.mu) }

// AddBundleActivationObserver registers fn to be called, on its own goroutine
// and outside bundleRT.mu, after every activation or rollback that wrote a
// history row. It never fires on an LKG restore, an embedded fallback, an
// idempotent rerun or a refusal. The returned function unsubscribes; a dispatch
// already under way still completes.
func AddBundleActivationObserver(fn func(BundleActivation)) (unsubscribe func()) {
	if fn == nil {
		return func() {}
	}
	observers.mu.Lock()
	defer observers.mu.Unlock()
	if observers.activate == nil {
		observers.activate = make(map[uint64]func(BundleActivation))
	}
	observers.next++
	id := observers.next
	observers.activate[id] = fn
	return func() {
		observers.mu.Lock()
		delete(observers.activate, id)
		observers.mu.Unlock()
	}
}

// fireBundleActivation dispatches a to every registered observer, each on its
// own goroutine, and returns immediately. It is called after the new bundle was
// published, with bundleRT.mu possibly still held by the caller: the observers
// themselves never run under it.
func fireBundleActivation(a BundleActivation) {
	observers.mu.Lock()
	observers.seq++
	a.Seq = observers.seq
	fns := make([]func(BundleActivation), 0, len(observers.activate))
	for _, fn := range observers.activate {
		fns = append(fns, fn)
	}
	observers.inflight += len(fns)
	observers.mu.Unlock()

	for _, fn := range fns {
		go func(fn func(BundleActivation)) {
			defer func() {
				// An observer must never take the process down with it.
				_ = recover()
				observers.mu.Lock()
				observers.inflight--
				if observers.inflight == 0 {
					observers.cond.Broadcast()
				}
				observers.mu.Unlock()
			}()
			fn(a)
		}(fn)
	}
}

// SetBundleRefreshObserver registers fn to receive exactly one
// RefreshOutcomeClass per refresh cycle. A nil fn clears it. internal/server
// maps the class to the telemetry enum.
func SetBundleRefreshObserver(fn func(RefreshOutcomeClass)) {
	observers.mu.Lock()
	observers.refresh = fn
	observers.mu.Unlock()
}

// notifyRefreshObserver hands one cycle's class to the registered refresh
// observer, if any. The observer runs synchronously on the cycle's goroutine,
// so it must not block.
func notifyRefreshObserver(class RefreshOutcomeClass) {
	observers.mu.Lock()
	fn := observers.refresh
	observers.mu.Unlock()
	if fn != nil {
		fn(class)
	}
}

// drainBundleObserversForTest blocks until every in-flight activation-observer
// dispatch has returned, so a test can assert on its effects.
func drainBundleObserversForTest() {
	observers.mu.Lock()
	defer observers.mu.Unlock()
	for observers.inflight > 0 {
		observers.cond.Wait()
	}
}
