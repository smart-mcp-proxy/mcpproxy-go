// Package limiter implements the bounded-concurrency admission control used by
// the upstream tool-call choke point (spec 093, GH #955).
//
// A Limiter caps the number of concurrently *running* calls in one scope (a
// single upstream server, or the proxy-wide aggregate) and parks excess calls
// in a bounded FIFO wait queue. Callers acquire with an absolute queue
// deadline that is shared across tiers (FR-004): waiting for a per-server slot
// and then for a global slot must never grant two full queue timeouts.
//
// Implementation note: the queue is a hand-rolled FIFO waiter list guarded by
// one mutex rather than golang.org/x/sync/semaphore. A semaphore's capacity is
// fixed at construction, but FR-021 requires occupancy to be *shared across
// generations* on hot reload — lowering a cap must not grant new capacity
// until the grandfathered running calls drain, and raising it must admit
// eligible waiters immediately. Both need a resizable cap over a live
// occupancy counter, which the waiter list gives us directly.
package limiter

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"
)

// Limits is one scope's configured concurrency settings, already resolved from
// the config tri-states.
type Limits struct {
	// Max is the maximum number of concurrently running calls. <= 0 means the
	// scope does not limit anything (occupancy is still tracked so a later
	// hot-reload that enables the limit sees the in-flight calls).
	Max int
	// QueueSize is the number of calls allowed to wait for a slot. 0 means no
	// pending capacity: calls arriving at the cap are shed immediately.
	QueueSize int
	// QueueTimeout is the scope's configured wait budget. The limiter itself
	// takes an absolute deadline from the caller; this value is reported as the
	// Retry-After hint on rejections produced by this scope.
	QueueTimeout time.Duration
}

// Enabled reports whether this scope actually caps concurrency.
func (l Limits) Enabled() bool { return l.Max > 0 }

// Stats is a point-in-time view of one scope's occupancy, used by the metrics
// surface (FR-013).
type Stats struct {
	Running int
	Queued  int
}

// noopRelease is returned by every admission path that did not take a slot.
func noopRelease() {}

type waiter struct {
	ch      chan struct{}
	el      *list.Element
	granted bool
	retired bool
}

// Limiter guards one scope. The zero value is not usable; use New. A nil
// *Limiter is a valid no-op passthrough so callers can skip nil checks.
type Limiter struct {
	scope  Scope
	server string

	mu      sync.Mutex
	limits  Limits
	running int
	waiters *list.List // of *waiter, FIFO
	retired bool

	// waiterWoke runs on a granted waiter's goroutine between its channel
	// closing and the post-wake re-check below. It exists so a test can land a
	// Retire() precisely inside that window (the grant-vs-retire interleaving
	// FR-009 turns on); nil everywhere else.
	waiterWoke func()
}

// New builds a limiter for a scope. server is the upstream name for
// ScopeServer and must be empty for ScopeGlobal (a global rejection must never
// blame a server, FR-010).
func New(scope Scope, server string, limits Limits) *Limiter {
	if scope == ScopeGlobal {
		server = ""
	}
	return &Limiter{
		scope:   scope,
		server:  server,
		limits:  limits,
		waiters: list.New(),
	}
}

// Limits returns the currently published limits for this scope.
func (l *Limiter) Limits() Limits {
	if l == nil {
		return Limits{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limits
}

// Stats returns the current occupancy and queue depth.
func (l *Limiter) Stats() Stats {
	if l == nil {
		return Stats{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return Stats{Running: l.running, Queued: l.waiters.Len()}
}

// SetLimits publishes a new generation of limits for this scope (FR-021).
// Running calls are never interrupted but keep counting against the new cap,
// so lowering the cap admits nothing until occupancy drains; raising it admits
// eligible queued calls immediately. Queued calls keep their original absolute
// deadline.
func (l *Limiter) SetLimits(limits Limits) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.limits = limits
	l.grantLocked()
	l.mu.Unlock()
}

// Retire marks the scope dead (server disabled, quarantined or removed) and
// fails every queued call promptly with ErrServerUnavailable instead of
// letting them sit until the queue deadline (FR-009). Outstanding holds keep
// draining into this instance; a re-added server gets a fresh instance so its
// capacity is never double-counted against the retired one.
func (l *Limiter) Retire() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.retired = true
	for e := l.waiters.Front(); e != nil; {
		next := e.Next()
		w, _ := e.Value.(*waiter)
		w.retired = true
		w.el = nil
		close(w.ch)
		l.waiters.Remove(e)
		e = next
	}
	l.mu.Unlock()
}

// Retired reports whether this instance has been retired.
func (l *Limiter) Retired() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.retired
}

// Acquire admits one call into the scope, waiting in the bounded FIFO queue if
// the cap is reached. queueDeadline is the ABSOLUTE deadline for the whole
// admission (shared across tiers, FR-004); the zero time means "wait until the
// caller's context ends".
//
// It returns an idempotent release closure bound to this instance, or:
//   - *LimitError{Reason: queue_full} when there is no pending capacity,
//   - *LimitError{Reason: queue_timeout} when the deadline expired while queued,
//   - *LimitError{Reason: server_unavailable} when the scope was retired,
//   - the caller's context error (context.Canceled / DeadlineExceeded) when the
//     CALLER's context ended while queued — never reported as shedding (FR-005).
func (l *Limiter) Acquire(ctx context.Context, queueDeadline time.Time) (func(), error) {
	if l == nil {
		return noopRelease, nil
	}
	return l.acquire(ctx, l.Limits(), queueDeadline)
}

// acquire is Acquire with the REPORTED limits supplied by the caller. The
// registry passes the values it published in the generation this admission was
// resolved from, so a rejection never describes itself with one scope's new cap
// and another's old queue deadline (FR-021). The admission DECISION still reads
// the instance's live limits under its own lock: a cap lowered a microsecond
// ago must bind immediately, and a cap raised a microsecond ago must admit.
func (l *Limiter) acquire(ctx context.Context, reportLimits Limits, queueDeadline time.Time) (func(), error) {
	if l == nil {
		return noopRelease, nil
	}

	l.mu.Lock()
	if l.retired {
		l.mu.Unlock()
		return nil, l.unavailableError()
	}
	// Fast path: unlimited scope, or free capacity.
	if l.limits.Max <= 0 || l.running < l.limits.Max {
		l.running++
		l.mu.Unlock()
		return l.releaseFunc(), nil
	}
	// Saturated: is there pending capacity?
	if l.waiters.Len() >= l.limits.QueueSize {
		l.mu.Unlock()
		return nil, l.limitError(ReasonQueueFull, reportLimits)
	}
	w := &waiter{ch: make(chan struct{})}
	w.el = l.waiters.PushBack(w)
	l.mu.Unlock()

	var timerC <-chan time.Time
	if !queueDeadline.IsZero() {
		timer := time.NewTimer(time.Until(queueDeadline))
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case <-w.ch:
		// Granted or retired — both close the channel. The re-check runs under
		// the limiter's own lock, which is what makes grant-vs-retire atomic
		// (FR-009): Retire flips l.retired under that same lock, so a waiter
		// granted a slot moments before retirement observes the retirement here
		// and hands the slot straight back instead of running against a server
		// that is no longer admitting work.
		if l.waiterWoke != nil {
			l.waiterWoke()
		}
		l.mu.Lock()
		retired := w.retired || l.retired
		if retired && w.granted {
			l.releaseLocked()
		}
		l.mu.Unlock()
		if retired {
			return nil, l.unavailableError()
		}
		return l.releaseFunc(), nil

	case <-timerC:
		// A grant that raced the deadline wins: abandon returns nil and the
		// caller keeps the slot rather than losing already-granted capacity.
		if err := l.abandon(w, l.limitError(ReasonQueueTimeout, reportLimits), true); err != nil {
			return nil, err
		}
		return l.releaseFunc(), nil

	case <-ctx.Done():
		cause := context.Cause(ctx)
		if cause == nil {
			cause = ctx.Err()
		}
		return nil, l.abandon(w, fmt.Errorf("call cancelled while waiting for a concurrency slot: %w", cause), false)
	}
}

// abandon removes a waiter that stopped waiting. If the waiter was granted a
// slot in the meantime, the slot is either kept (grantWins: the grant raced the
// deadline, so honour it) or handed to the next waiter (caller is gone).
// It returns the error to report, or nil when the grant is honoured.
func (l *Limiter) abandon(w *waiter, reportErr error, grantWins bool) error {
	l.mu.Lock()
	switch {
	case w.retired || l.retired:
		// Same atomicity rule as the wake path: a slot granted just before
		// retirement is given back rather than honoured.
		if w.granted {
			l.releaseLocked()
		}
		l.mu.Unlock()
		return l.unavailableError()
	case w.granted && grantWins:
		l.mu.Unlock()
		return nil // caller keeps the slot; see Acquire's callers below
	case w.granted:
		// Caller is gone: give the slot straight back to the queue.
		l.releaseLocked()
		l.mu.Unlock()
		return reportErr
	default:
		if w.el != nil {
			l.waiters.Remove(w.el)
			w.el = nil
		}
		l.mu.Unlock()
		return reportErr
	}
}

func (l *Limiter) limitError(reason Reason, limits Limits) *LimitError {
	return &LimitError{
		Scope:      l.scope,
		Reason:     reason,
		Server:     l.server,
		Limit:      limits.Max,
		RetryAfter: limits.QueueTimeout,
	}
}

func (l *Limiter) unavailableError() *LimitError {
	return &LimitError{Scope: l.scope, Reason: ReasonServerUnavailable, Server: l.server}
}

// releaseFunc returns the idempotent release closure bound to this instance.
// Binding matters across hot-swaps and retirement: a hold taken on a retired
// instance must drain into that instance, never into the fresh one.
func (l *Limiter) releaseFunc() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.releaseLocked()
			l.mu.Unlock()
		})
	}
}

func (l *Limiter) releaseLocked() {
	if l.running > 0 {
		l.running--
	}
	l.grantLocked()
}

// grantLocked hands free capacity to the head of the FIFO queue.
func (l *Limiter) grantLocked() {
	for l.waiters.Len() > 0 && (l.limits.Max <= 0 || l.running < l.limits.Max) {
		e := l.waiters.Front()
		w, _ := e.Value.(*waiter)
		l.waiters.Remove(e)
		w.el = nil
		w.granted = true
		l.running++
		close(w.ch)
	}
}
