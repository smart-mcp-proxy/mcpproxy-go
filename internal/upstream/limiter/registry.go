package limiter

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// defaultQueueBudget is the wait budget used when a scope caps concurrency but
// publishes no queue_timeout. The config layer already defaults an active scope
// to 30s, so this only guards limits published directly (API, tests): a queued
// call must never wait without a deadline, which is the one way a saturated
// scope could park a caller indefinitely.
const defaultQueueBudget = 30 * time.Second

// Registry owns the live limiter instances: one per configured upstream server
// plus the proxy-wide aggregate. Readers take a whole GENERATION through one
// atomic pointer (no lock on the hot path); writers (config apply / hot reload)
// serialize on the registry mutex and republish the generation as a unit.
//
// Three invariants drive the shape of this type:
//
//   - FR-021, atomic publication: an admission must never combine one scope's
//     new settings with another scope's old ones, nor a new cap with a queue
//     deadline derived from an older generation. Everything an admission needs
//     — both limiter instances, both scopes' limits, and the resolved wait
//     budget — is resolved from ONE generation load, and the limits themselves
//     live only in generations. Publishing therefore cannot disturb an
//     admission already in flight: the new generation is stored first and the
//     wait queues are re-evaluated against it afterwards.
//
//   - FR-021, shared occupancy: limiter instances SURVIVE a reload (only the
//     limits are republished), and an instance exists for every eligible scope
//     even when that scope currently caps nothing. An unlimited scope still
//     counts its running calls, so enabling a cap later sees the calls already
//     in flight instead of starting from zero and admitting a second cap's
//     worth on top of them.
//
//   - FR-009, no admit-after-disable: retiring a server TOMBSTONES its entry
//     and the tombstone is never removed. Absence of a scope means "unlimited",
//     so absence must never be able to follow retirement — a caller that
//     resolved its client before the server was disabled can reach admission at
//     any later time, and it has to be refused, not waved through. Outstanding
//     holds drain into the retired instance; a server re-added under the same
//     name replaces the tombstone with a fresh instance, so the map holds at
//     most one entry per distinct server name the process has ever configured.
type Registry struct {
	mu sync.Mutex

	// Live instances, mutated under mu only and snapshotted into each
	// generation. Includes retired tombstones.
	global  *Limiter
	servers map[string]*Limiter

	gen atomic.Pointer[generation]
}

// generation is one immutable published set of limits. Readers load it once per
// admission, so every value an admission observes belongs to the same publish.
type generation struct {
	global       *Limiter
	globalLimits Limits
	servers      map[string]*serverScope
}

// serverScope is one server's slot in a generation: its limiter instance, the
// limits published for it, and the wait budget that spans BOTH tiers (FR-004).
type serverScope struct {
	lim    *Limiter
	limits Limits
	budget time.Duration
}

// NewRegistry returns an empty registry: nothing is published, so every Acquire
// is a no-op passthrough (FR-006).
func NewRegistry() *Registry { return &Registry{} }

// Global returns the proxy-wide limiter, or nil when none was published.
func (r *Registry) Global() *Limiter {
	if r == nil {
		return nil
	}
	gen := r.gen.Load()
	if gen == nil {
		return nil
	}
	return gen.global
}

// Server returns the LIVE limiter for an upstream, or nil when that server has
// no limiter — unconfigured, or retired. A retired instance is deliberately
// invisible here (it is a tombstone, not a working scope); admission still
// finds it internally and refuses the call.
func (r *Registry) Server(name string) *Limiter {
	if r == nil {
		return nil
	}
	gen := r.gen.Load()
	if gen == nil {
		return nil
	}
	scope := gen.servers[name]
	if scope == nil || scope.lim.Retired() {
		return nil
	}
	return scope.lim
}

// SetGlobal publishes the global aggregate limiter's settings.
func (r *Registry) SetGlobal(limits Limits) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publishLocked(limits, r.serverLimitsLocked(map[string]Limits{}))
}

// SetServer publishes one server's limits and returns the live instance.
func (r *Registry) SetServer(name string, limits Limits) *Limiter {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	lim := r.ensureServerLocked(name)
	r.publishLocked(r.globalLimitsLocked(), r.serverLimitsLocked(map[string]Limits{name: limits}))
	return lim
}

// RetireServer tombstones a server's limiter: queued calls fail immediately
// with ErrServerUnavailable and later admissions are refused, even if the
// caller snapshotted the client before the state change (FR-009,
// admit-after-disable race). Outstanding holds drain into the retired instance.
func (r *Registry) RetireServer(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retireServerLocked(name)
	r.publishLocked(r.globalLimitsLocked(), r.serverLimitsLocked(nil))
}

// Apply publishes one atomic generation of limits for every scope (FR-021):
// the global aggregate plus the resolved per-server limits. Servers missing
// from the map are retired (their tombstone stays, see the type comment).
func (r *Registry) Apply(global Limits, servers map[string]Limits) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	for name := range r.servers {
		if _, ok := servers[name]; !ok {
			r.retireServerLocked(name)
		}
	}
	for name := range servers {
		r.ensureServerLocked(name)
	}

	r.publishLocked(global, servers)
}

// ensureGlobalLocked creates the global instance if it does not exist yet. The
// instance exists even when the scope caps nothing: an unlimited scope is still
// an occupancy tracker (FR-021).
func (r *Registry) ensureGlobalLocked() {
	if r.global != nil {
		return
	}
	r.global = newPublished(ScopeGlobal, "", func() Limits {
		gen := r.gen.Load()
		if gen == nil {
			return Limits{}
		}
		return gen.globalLimits
	})
}

// ensureServerLocked creates a server's instance if it has none, or replaces a
// RETIRED one with a fresh instance: the tombstone's outstanding holds belong
// to it alone, so a re-added server never inherits them (FR-009).
func (r *Registry) ensureServerLocked(name string) *Limiter {
	if r.servers == nil {
		r.servers = make(map[string]*Limiter)
	}
	if cur := r.servers[name]; cur != nil && !cur.Retired() {
		return cur
	}
	fresh := newPublished(ScopeServer, name, func() Limits {
		gen := r.gen.Load()
		if gen == nil {
			return Limits{}
		}
		scope := gen.servers[name]
		if scope == nil {
			return Limits{}
		}
		return scope.limits
	})
	r.servers[name] = fresh
	return fresh
}

func (r *Registry) retireServerLocked(name string) {
	cur := r.servers[name]
	if cur == nil || cur.Retired() {
		return
	}
	cur.Retire()
}

// globalLimitsLocked returns the limits currently published for the global
// scope, so a single-scope update can republish the rest unchanged.
func (r *Registry) globalLimitsLocked() Limits {
	gen := r.gen.Load()
	if gen == nil {
		return Limits{}
	}
	return gen.globalLimits
}

// serverLimitsLocked returns the currently published per-server limits with
// `overrides` applied, so a single-scope update carries every other scope
// forward untouched into the new generation.
func (r *Registry) serverLimitsLocked(overrides map[string]Limits) map[string]Limits {
	out := make(map[string]Limits, len(r.servers))
	if gen := r.gen.Load(); gen != nil {
		for name, scope := range gen.servers {
			out[name] = scope.limits
		}
	}
	for name := range r.servers {
		if _, ok := out[name]; !ok {
			out[name] = Limits{}
		}
	}
	for name, limits := range overrides {
		out[name] = limits
	}
	return out
}

// publishLocked snapshots the live instances plus the given limits into one
// immutable generation, stores it, and only THEN re-evaluates every wait queue
// against it. Callers must hold r.mu.
//
// The order matters in both directions. Storing first means no admission can
// ever see a limiter whose cap has moved out from under the generation it
// resolved — there is no per-instance limits state to move. Re-granting after
// means a raise admits eligible waiters immediately and a lowered cap admits
// nothing until occupancy drains, which is FR-021's "takes effect within one
// reload cycle" in both directions.
func (r *Registry) publishLocked(global Limits, servers map[string]Limits) {
	r.ensureGlobalLocked()

	gen := &generation{global: r.global, globalLimits: global, servers: make(map[string]*serverScope, len(r.servers))}
	for name, lim := range r.servers {
		limits := servers[name] // absent from the new config = retired, no limits
		gen.servers[name] = &serverScope{
			lim:    lim,
			limits: limits,
			budget: queueBudget(limits, global),
		}
	}
	r.gen.Store(gen)

	// Re-evaluate the queues against what was just published.
	r.global.regrant()
	for _, lim := range r.servers {
		lim.regrant()
	}
}

// QueueBudget reports the wait budget an admission for this server would get
// from the currently published generation. It is the single owner of the FR-004
// rule (the config layer resolves per-scope settings; combining them into one
// deadline happens here, where the generation guarantees both scopes come from
// the same publish).
func (r *Registry) QueueBudget(server string) time.Duration {
	if r == nil {
		return 0
	}
	gen := r.gen.Load()
	if gen == nil {
		return 0
	}
	if scope := gen.servers[server]; scope != nil {
		return scope.budget
	}
	return queueBudget(Limits{}, gen.globalLimits)
}

// queueBudget is the total wait budget for one call: the smallest positive
// queue_timeout among the scopes that actually limit it (FR-004 — one absolute
// deadline spanning the per-server and global admission steps combined, never
// one budget per step). 0 means no limiter applies, so there is nothing to wait
// for; a scope that DOES limit always yields a positive budget, so a queued
// call can never sit without a deadline.
func queueBudget(server, global Limits) time.Duration {
	budget := time.Duration(0)
	limited := false
	consider := func(l Limits) {
		if !l.Enabled() {
			return
		}
		limited = true
		if l.QueueTimeout <= 0 {
			return
		}
		if budget == 0 || l.QueueTimeout < budget {
			budget = l.QueueTimeout
		}
	}
	consider(server)
	consider(global)
	if limited && budget == 0 {
		return defaultQueueBudget
	}
	return budget
}

// Acquire admits a call through both tiers of ONE published generation: the
// same generation supplies both limiters, both scopes' limits — which are what
// the admission decision itself is made against — and the single absolute queue
// deadline (FR-004, FR-021). The per-server slot is taken first so a slow
// upstream's queue does not pin global capacity while waiting (FR-002); on a
// global rejection the per-server slot is released again.
//
// The returned release closure releases both tiers and is safe to call more
// than once.
func (r *Registry) Acquire(ctx context.Context, server string) (func(), error) {
	if r == nil {
		return noopRelease, nil
	}
	return r.acquireIn(r.gen.Load(), ctx, server)
}

// acquireIn is Acquire against an explicitly chosen generation. Production
// always passes the current one; taking it as a parameter is what makes "one
// generation governs one admission" testable, by letting a test hold a
// generation across a reload.
func (r *Registry) acquireIn(gen *generation, ctx context.Context, server string) (func(), error) {
	if gen == nil {
		return noopRelease, nil
	}

	var (
		serverLim    *Limiter
		serverLimits Limits
		budget       time.Duration
	)
	if scope := gen.servers[server]; scope != nil {
		serverLim, serverLimits, budget = scope.lim, scope.limits, scope.budget
	} else {
		budget = queueBudget(Limits{}, gen.globalLimits)
	}

	var deadline time.Time
	if budget > 0 {
		deadline = time.Now().Add(budget)
	}

	releaseServer, err := serverLim.acquire(ctx, serverLimits, deadline)
	if err != nil {
		return nil, err
	}
	releaseGlobal, err := gen.global.acquire(ctx, gen.globalLimits, deadline)
	if err != nil {
		releaseServer()
		return nil, err
	}
	return func() {
		releaseGlobal()
		releaseServer()
	}, nil
}

// ServerStats returns the occupancy of every live per-server limiter, keyed by
// server name (FR-013: per-server queue depth for the metrics surface). Retired
// tombstones are omitted: they belong to servers that are no longer configured.
func (r *Registry) ServerStats() map[string]Stats {
	if r == nil {
		return nil
	}
	gen := r.gen.Load()
	if gen == nil {
		return nil
	}
	out := make(map[string]Stats, len(gen.servers))
	for name, scope := range gen.servers {
		if scope.lim.Retired() {
			continue
		}
		out[name] = scope.lim.Stats()
	}
	return out
}
