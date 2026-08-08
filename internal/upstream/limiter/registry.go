package limiter

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

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
//     budget — is therefore resolved from ONE generation load.
//
//   - FR-021, shared occupancy: hot reload MUTATES the existing instance
//     (SetLimits) instead of swapping in a new one, and an instance exists for
//     every eligible scope even when that scope currently caps nothing. An
//     unlimited scope still counts its running calls, so enabling a cap later
//     sees the calls already in flight instead of starting from zero and
//     admitting a second full cap's worth on top of them.
//
//   - FR-009, no admit-after-disable: retiring a server TOMBSTONES its entry
//     rather than deleting it, so a call that snapshotted its client before the
//     state change is refused at admission instead of sailing through an absent
//     limiter. Outstanding holds keep draining into the retired instance; a
//     server re-added while they drain gets a fresh instance, so its capacity is
//     never double-counted against the tombstone.
type Registry struct {
	mu sync.Mutex

	// Live state, mutated under mu only and snapshotted into each generation.
	global  *Limiter
	servers map[string]*Limiter // includes retired tombstones

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
	r.setGlobalLocked(limits)
	r.publishLocked()
}

// setGlobalLocked updates (or creates) the global instance. The instance is
// created even when the limits are disabled: an unlimited global scope still
// tracks occupancy so a later hot-enable sees the in-flight calls (FR-021).
func (r *Registry) setGlobalLocked(limits Limits) {
	if r.global == nil {
		r.global = New(ScopeGlobal, "", limits)
		return
	}
	r.global.SetLimits(limits)
}

// SetServer publishes one server's limits and returns the live instance.
func (r *Registry) SetServer(name string, limits Limits) *Limiter {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	lim := r.setServerLocked(name, limits)
	r.publishLocked()
	return lim
}

// setServerLocked updates (or creates) one server's instance. Like the global
// scope, a server with no cap still gets an instance so its occupancy is known
// when a cap is enabled later. A RETIRED instance is replaced rather than
// revived: its outstanding holds belong to it alone (FR-009).
func (r *Registry) setServerLocked(name string, limits Limits) *Limiter {
	if r.servers == nil {
		r.servers = make(map[string]*Limiter)
	}
	if cur := r.servers[name]; cur != nil && !cur.Retired() {
		cur.SetLimits(limits)
		return cur
	}
	fresh := New(ScopeServer, name, limits)
	r.servers[name] = fresh
	return fresh
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
	r.publishLocked()
}

func (r *Registry) retireServerLocked(name string) {
	cur := r.servers[name]
	if cur == nil || cur.Retired() {
		return
	}
	cur.Retire()
}

// Apply publishes one atomic generation of limits for every scope (FR-021):
// the global aggregate plus the resolved per-server limits. Servers missing
// from the map are retired.
//
// Tombstones that have finished draining are dropped here rather than kept
// forever: a call still holding a pre-retirement client snapshot across a whole
// reload cycle is caught by the dispatch layer's own liveness checks, so the
// map stays bounded by the config plus the names retired since the last reload.
func (r *Registry) Apply(global Limits, servers map[string]Limits) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.setGlobalLocked(global)

	for name, lim := range r.servers {
		if _, ok := servers[name]; ok {
			continue
		}
		r.retireServerLocked(name)
		if st := lim.Stats(); st.Running == 0 && st.Queued == 0 {
			delete(r.servers, name)
		}
	}
	for name, limits := range servers {
		r.setServerLocked(name, limits)
	}

	r.publishLocked()
}

// publishLocked snapshots the live instances into one immutable generation and
// stores it. Callers must hold r.mu. The wait budget is baked in here so an
// admission cannot pair a new cap with a stale deadline.
func (r *Registry) publishLocked() {
	gen := &generation{global: r.global, servers: make(map[string]*serverScope, len(r.servers))}
	if r.global != nil {
		gen.globalLimits = r.global.Limits()
	}
	for name, lim := range r.servers {
		limits := lim.Limits()
		gen.servers[name] = &serverScope{
			lim:    lim,
			limits: limits,
			budget: queueBudget(limits, gen.globalLimits),
		}
	}
	r.gen.Store(gen)
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
// for.
func queueBudget(server, global Limits) time.Duration {
	budget := time.Duration(0)
	consider := func(l Limits) {
		if !l.Enabled() || l.QueueTimeout <= 0 {
			return
		}
		if budget == 0 || l.QueueTimeout < budget {
			budget = l.QueueTimeout
		}
	}
	consider(server)
	consider(global)
	return budget
}

// Acquire admits a call through both tiers of ONE published generation: the
// same generation supplies both limiters, both scopes' reported limits and the
// single absolute queue deadline (FR-004, FR-021). The per-server slot is taken
// first so a slow upstream's queue does not pin global capacity while waiting
// (FR-002); on a global rejection the per-server slot is released again.
//
// The returned release closure releases both tiers and is safe to call more
// than once.
func (r *Registry) Acquire(ctx context.Context, server string) (func(), error) {
	if r == nil {
		return noopRelease, nil
	}
	gen := r.gen.Load()
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
