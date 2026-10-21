package scanner

import "time"

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
}

// AddBundleActivationObserver registers fn to be called, on its own goroutine
// and outside bundleRT.mu, after every activation or rollback that wrote a
// history row. It never fires on an LKG restore, an embedded fallback, an
// idempotent rerun or a refusal. The returned function unsubscribes.
//
// STUB (Spec 101 US3 wave 0): fn is never called and the unsubscribe is a
// no-op until T330.
func AddBundleActivationObserver(fn func(BundleActivation)) (unsubscribe func()) {
	_ = fn
	return func() {}
}

// SetBundleRefreshObserver registers fn to receive exactly one
// RefreshOutcomeClass per refresh cycle. A nil fn clears it. internal/server
// maps the class to the telemetry enum.
//
// STUB (Spec 101 US3 wave 0): fn is never called until T331.
func SetBundleRefreshObserver(fn func(RefreshOutcomeClass)) {
	_ = fn
}

// notifyRefreshObserver hands one cycle's class to the registered refresh
// observer, if any.
//
// STUB (Spec 101 US3 wave 1): never delivers until T331.
//
//nolint:unused // called by the refresher once T331 lands
func notifyRefreshObserver(class RefreshOutcomeClass) { _ = class }

// drainBundleObserversForTest blocks until every in-flight activation-observer
// dispatch has returned, so a test can assert on its effects.
//
// STUB (Spec 101 US3 wave 0): returns immediately until T330.
//
//nolint:unused // referenced by the T328 tests
func drainBundleObserversForTest() {}
