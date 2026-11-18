package telemetry

import "time"

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
// when the window OPENED, present only on a fresh personal-edition install.
// Every field is frozen into the drained snapshot.
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
	// left an undelivered window behind (the persisted marker was still set),
	// or the first start of this build on an existing store, where no marker
	// exists yet. Those unsent counts are lost.
	OutcomeCoveragePartial = "partial"
)

// RecordUpstreamOutcome counts one logical upstream call that dispatched at
// least one request. succeeded is true for a usable result; starter is true
// when the starter classifier marks the call's server as the starter. The
// server name never reaches the registry.
//
// The first outcome of a window (the first since the last Drain) calls the
// undelivered-window hook outside the registry lock, with the window's drain
// epoch; the hook persists the marker FR-036 reads at the next start.
func (r *CounterRegistry) RecordUpstreamOutcome(succeeded, starter bool) {
	r.mu.Lock()
	first := !r.upstreamOutcomesPendingLocked()
	if succeeded {
		r.outcomeSucceeded++
		if !starter {
			r.outcomeSucceededNonStarter++
		}
	} else {
		r.outcomeUnusable++
	}
	hook := r.outcomeWindowHook
	epoch := r.outcomeDrainEpoch
	r.mu.Unlock()
	if first && hook != nil {
		hook(epoch)
	}
}

// SetUndeliveredWindowHook installs the function called when a window records
// its first outcome, with that window's drain epoch. The telemetry service
// installs it to persist the undelivered-window marker.
func (r *CounterRegistry) SetUndeliveredWindowHook(fn func(epoch uint64)) {
	r.mu.Lock()
	r.outcomeWindowHook = fn
	r.mu.Unlock()
}

// SetOutcomeWindowAgeFunc installs the window_age mapping: the install age
// bucket of the time a window opened (registry creation, i.e. process start,
// or the last Drain). fn must be pure and cheap (it runs under the registry
// lock); nil omits window_age. Drain freezes the value into the detached
// snapshot, so a retry carries the age its window opened at.
func (r *CounterRegistry) SetOutcomeWindowAgeFunc(fn func(opened time.Time) string) {
	r.mu.Lock()
	r.outcomeWindowAgeFn = fn
	r.mu.Unlock()
}

// HasPendingUpstreamOutcomes reports whether the current (undrained) window
// holds any outcome.
func (r *CounterRegistry) HasPendingUpstreamOutcomes() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.upstreamOutcomesPendingLocked()
}

// MarkOutcomeWindowPartial marks the CURRENT counter window's
// upstream_outcomes as partial. Service.InitOutcomeWindow calls it once at
// startup when the predecessor left an undelivered window. The flag travels with the window: Drain
// moves it into the detached snapshot (kept unchanged across retries) and the
// next window starts full.
func (r *CounterRegistry) MarkOutcomeWindowPartial() {
	r.mu.Lock()
	r.outcomeWindowPartial = true
	r.mu.Unlock()
}

// RecordUpstreamOutcomeOn calls reg.RecordUpstreamOutcome if reg is non-nil.
func RecordUpstreamOutcomeOn(reg *CounterRegistry, succeeded, starter bool) {
	if reg == nil {
		return
	}
	reg.RecordUpstreamOutcome(succeeded, starter)
}

// upstreamOutcomesLocked builds the wire form of the current window. The
// caller holds r.mu (read or write).
func (r *CounterRegistry) upstreamOutcomesLocked() UpstreamOutcomes {
	coverage := OutcomeCoverageFull
	if r.outcomeWindowPartial {
		coverage = OutcomeCoveragePartial
	}
	windowAge := ""
	if r.outcomeWindowAgeFn != nil {
		windowAge = r.outcomeWindowAgeFn(r.outcomeWindowOpenedAt)
	}
	return UpstreamOutcomes{
		Succeeded:           bucketUpstream(r.outcomeSucceeded),
		Unusable:            bucketUpstream(r.outcomeUnusable),
		SucceededNonStarter: bucketUpstream(r.outcomeSucceededNonStarter),
		Coverage:            coverage,
		WindowAge:           windowAge,
	}
}

// resetUpstreamOutcomesLocked zeroes the window's counts and its coverage
// flag. The caller holds r.mu and sets the next window's open time.
func (r *CounterRegistry) resetUpstreamOutcomesLocked() {
	r.outcomeSucceeded = 0
	r.outcomeUnusable = 0
	r.outcomeSucceededNonStarter = 0
	r.outcomeWindowPartial = false
}

// upstreamOutcomesPendingLocked reports whether the window holds any outcome.
// The coverage flag alone is not usage data. The caller holds r.mu.
func (r *CounterRegistry) upstreamOutcomesPendingLocked() bool {
	return r.outcomeSucceeded > 0 || r.outcomeUnusable > 0 || r.outcomeSucceededNonStarter > 0
}

// UpstreamOutcomeTotals returns the raw counts of the current (undrained)
// window. Never transmitted (the wire carries buckets only); for tests and
// internal observability, like AnonymityViolationsTotal.
func (r *CounterRegistry) UpstreamOutcomeTotals() (succeeded, unusable, succeededNonStarter int64) {
	if r == nil {
		return 0, 0, 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.outcomeSucceeded, r.outcomeUnusable, r.outcomeSucceededNonStarter
}
