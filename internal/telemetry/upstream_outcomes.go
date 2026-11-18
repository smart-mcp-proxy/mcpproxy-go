package telemetry

import (
	"sync"
	"time"
)

// Schema v17 (Spec 126 FR-035, FR-036): per-window COMPLETION counters.
//
// upstream_tool_call_count_bucket (schema 14) counts DISPATCHES: requests the
// core client handed to the transport, so a session-recovery repeat counts
// twice. upstream_outcomes counts LOGICAL calls that dispatched at least one
// request, by outcome:
//
//   - succeeded: a usable result (no error, no isError:true, not withheld by an
//     output policy) — the predicate of activation.first_real_tool_call_ever;
//   - unusable: dispatched, but ended without a usable result;
//   - succeeded_non_starter: the subset of succeeded that is not on the
//     starter server (FR-037).
//
// A call refused before dispatch records no outcome. The counts share the
// registry's reset-on-accept window: Drain detaches them together with every
// other counter, so the window keyed by heartbeat_id is frozen until a 2xx and
// a retry re-sends it byte-identical (the receiver answers 409 otherwise).

// UpstreamOutcomes is the wire form of the completion window. Every count is a
// bucketUpstream label; Coverage is OutcomeCoverageFull or
// OutcomeCoveragePartial; WindowAge is the install age (an InstallAge* value)
// when the window OPENED, present only when install_origin is "fresh" (never
// on the server edition). Every field is frozen into the drained snapshot.
type UpstreamOutcomes struct {
	Succeeded           string `json:"succeeded"`
	Unusable            string `json:"unusable"`
	SucceededNonStarter string `json:"succeeded_non_starter"`
	Coverage            string `json:"coverage"`
	WindowAge           string `json:"window_age,omitempty"`
}

// Coverage values of upstream_outcomes (FR-036).
const (
	// OutcomeCoverageFull: the counts cover the whole time since the previous
	// accepted send (or, on a fresh install, since the store was created).
	OutcomeCoverageFull = "full"
	// OutcomeCoveragePartial: the first window of a process whose predecessor
	// left recorded outcomes undelivered (the persisted marker was still set),
	// or the first schema-17 start of an install whose origin is "existing",
	// where no marker exists yet. Those unsent counts are lost.
	OutcomeCoveragePartial = "partial"
)

// outcomeWindow is the live completion window and the undelivered-window
// marker it drives (FR-036). One small mutex guards the outcome increment,
// the "outcome since the last drain" state (counts > 0), Drain's swap and
// reset of the counts, and both marker writes, so:
//
//   - the marker is set (true) in the same critical section as, and before,
//     the first outcome after process start or after a Drain;
//   - the post-2xx clear (false) is written only when no outcome was counted
//     since the Drain whose snapshot was accepted, decided and written in one
//     critical section.
//
// No interleaving can therefore leave the marker false while an undelivered
// outcome exists. The mutex is NEVER held across network I/O: Drain runs at
// the start of a send and the clear after its 2xx, each taking the mutex only
// for the swap or the write. A marker write is a local bbolt commit, once per
// window, and is the only disk I/O under it. The accept decision itself is
// made under the service's deliveryMu; only the post-2xx clear takes this
// mutex, and nothing on the tool-call path ever waits on deliveryMu.
type outcomeWindow struct {
	mu                  sync.Mutex
	succeeded           int64
	unusable            int64
	succeededNonStarter int64
	// partial marks the CURRENT window's coverage (cleared by a Drain).
	partial bool
	// openedAt is when the current window opened: registry creation
	// (process start) or the last Drain. ageFn maps it to window_age; nil
	// omits window_age.
	openedAt time.Time
	ageFn    func(opened time.Time) string
	// markerWrite persists the undelivered-window marker; nil until the
	// telemetry service installs it (InitOutcomeWindow).
	markerWrite func(set bool)
}

func (w *outcomeWindow) pendingLocked() bool {
	return w.succeeded > 0 || w.unusable > 0 || w.succeededNonStarter > 0
}

func (w *outcomeWindow) pending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pendingLocked()
}

func (w *outcomeWindow) viewLocked() UpstreamOutcomes {
	coverage := OutcomeCoverageFull
	if w.partial {
		coverage = OutcomeCoveragePartial
	}
	windowAge := ""
	if w.ageFn != nil {
		windowAge = w.ageFn(w.openedAt)
	}
	return UpstreamOutcomes{
		Succeeded:           bucketUpstream(w.succeeded),
		Unusable:            bucketUpstream(w.unusable),
		SucceededNonStarter: bucketUpstream(w.succeededNonStarter),
		Coverage:            coverage,
		WindowAge:           windowAge,
	}
}

func (w *outcomeWindow) view() UpstreamOutcomes {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.viewLocked()
}

// drain detaches the window (frozen, coverage and window_age included) and
// opens the next one at now.
func (w *outcomeWindow) drain(now time.Time) UpstreamOutcomes {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.viewLocked()
	w.succeeded, w.unusable, w.succeededNonStarter = 0, 0, 0
	w.partial = false
	w.openedAt = now
	return out
}

// reset zeroes the window like drain, discarding it.
func (w *outcomeWindow) reset(now time.Time) {
	_ = w.drain(now)
}

// RecordUpstreamOutcome counts one logical upstream call that dispatched at
// least one request. succeeded is true for a usable result; starter is true
// when the starter classifier marks the call's server as the starter. The
// server name never reaches the registry. The first outcome of a window (the
// first since process start or the last Drain) sets the undelivered-window
// marker in the same critical section.
func (r *CounterRegistry) RecordUpstreamOutcome(succeeded, starter bool) {
	w := &r.outcomes
	w.mu.Lock()
	defer w.mu.Unlock()
	// The marker is set BEFORE the increment, inside the same critical
	// section, so there is never an undelivered outcome with the marker
	// still false.
	if !w.pendingLocked() && w.markerWrite != nil {
		w.markerWrite(true)
	}
	if succeeded {
		w.succeeded++
		if !starter {
			w.succeededNonStarter++
		}
	} else {
		w.unusable++
	}
}

// RecordUpstreamOutcomeOn calls reg.RecordUpstreamOutcome if reg is non-nil.
func RecordUpstreamOutcomeOn(reg *CounterRegistry, succeeded, starter bool) {
	if reg == nil {
		return
	}
	reg.RecordUpstreamOutcome(succeeded, starter)
}

// MarkOutcomeWindowPartial marks the CURRENT window's upstream_outcomes as
// partial. Service.InitOutcomeWindow calls it once at startup when the
// predecessor left an undelivered window. The flag travels with the window:
// Drain moves it into the detached snapshot (kept unchanged across retries)
// and the next window starts full.
func (r *CounterRegistry) MarkOutcomeWindowPartial() {
	r.outcomes.mu.Lock()
	r.outcomes.partial = true
	r.outcomes.mu.Unlock()
}

// SetOutcomeWindowAgeFunc installs the window_age mapping: the install age
// bucket of the time a window opened (registry creation, i.e. process start,
// or the last Drain). fn must be pure and cheap (it runs under the window
// mutex); nil omits window_age. Drain freezes the value into the detached
// snapshot, so a retry carries the age its window opened at.
func (r *CounterRegistry) SetOutcomeWindowAgeFunc(fn func(opened time.Time) string) {
	r.outcomes.mu.Lock()
	r.outcomes.ageFn = fn
	r.outcomes.mu.Unlock()
}

// SetOutcomeMarkerWriter installs the persistence of the undelivered-window
// marker. When the live window already holds outcomes (recorded before the
// writer existed) the marker is set at once.
func (r *CounterRegistry) SetOutcomeMarkerWriter(write func(set bool)) {
	w := &r.outcomes
	w.mu.Lock()
	defer w.mu.Unlock()
	w.markerWrite = write
	if write != nil && w.pendingLocked() {
		write(true)
	}
}

// ClearOutcomeMarkerIfIdle runs after an accepted send (the shutdown flush
// included): it clears the marker unless an outcome was counted since the
// Drain whose snapshot was just accepted. Those outcomes are still
// undelivered, and the marker their first outcome set must stay set.
func (r *CounterRegistry) ClearOutcomeMarkerIfIdle() {
	if r == nil {
		return
	}
	w := &r.outcomes
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.markerWrite != nil && !w.pendingLocked() {
		w.markerWrite(false)
	}
}

// HasPendingUpstreamOutcomes reports whether the current (undrained) window
// holds any outcome.
func (r *CounterRegistry) HasPendingUpstreamOutcomes() bool {
	if r == nil {
		return false
	}
	return r.outcomes.pending()
}

// UpstreamOutcomeTotals returns the raw counts of the current (undrained)
// window. Never transmitted (the wire carries buckets only); for tests and
// internal observability, like AnonymityViolationsTotal.
func (r *CounterRegistry) UpstreamOutcomeTotals() (succeeded, unusable, succeededNonStarter int64) {
	if r == nil {
		return 0, 0, 0
	}
	r.outcomes.mu.Lock()
	defer r.outcomes.mu.Unlock()
	return r.outcomes.succeeded, r.outcomes.unusable, r.outcomes.succeededNonStarter
}
