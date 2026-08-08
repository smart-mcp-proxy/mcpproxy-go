package limiter

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Registry owns the live limiter instances: one per configured upstream server
// plus the proxy-wide aggregate. Readers take the current instance through an
// atomic pointer (no lock on the hot path); writers (config apply / hot reload)
// serialize on the registry mutex.
//
// Hot reload MUTATES the existing instance (SetLimits) instead of swapping in a
// new one, because occupancy must be shared across generations (FR-021). A new
// instance is created only for a server that has no live limiter — including a
// server re-added after retirement, whose old holds keep draining into the
// retired instance so capacity is never double-counted (FR-009).
type Registry struct {
	mu      sync.Mutex
	global  atomic.Pointer[Limiter]
	servers sync.Map // string -> *atomic.Pointer[Limiter]
}

// NewRegistry returns an empty registry: every scope is unconfigured, so every
// Acquire is a no-op passthrough (FR-006).
func NewRegistry() *Registry { return &Registry{} }

// Global returns the proxy-wide limiter, or nil when none is configured.
func (r *Registry) Global() *Limiter {
	if r == nil {
		return nil
	}
	return r.global.Load()
}

// Server returns the limiter for an upstream, or nil when that server has no
// limiter (unconfigured, or retired).
func (r *Registry) Server(name string) *Limiter {
	if r == nil {
		return nil
	}
	v, ok := r.servers.Load(name)
	if !ok {
		return nil
	}
	ptr, _ := v.(*atomic.Pointer[Limiter])
	return ptr.Load()
}

// SetGlobal publishes the global aggregate limiter's settings. An existing
// instance is updated in place so running calls keep counting; when no
// instance exists and the limits are disabled, nothing is allocated.
func (r *Registry) SetGlobal(limits Limits) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setGlobalLocked(limits)
}

func (r *Registry) setGlobalLocked(limits Limits) {
	if cur := r.global.Load(); cur != nil {
		cur.SetLimits(limits)
		return
	}
	if !limits.Enabled() {
		return
	}
	r.global.Store(New(ScopeGlobal, "", limits))
}

// SetServer publishes one server's limits and returns the live instance (nil
// when the server has no instance and the limits are disabled).
func (r *Registry) SetServer(name string, limits Limits) *Limiter {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.setServerLocked(name, limits)
}

func (r *Registry) setServerLocked(name string, limits Limits) *Limiter {
	v, _ := r.servers.LoadOrStore(name, &atomic.Pointer[Limiter]{})
	ptr, _ := v.(*atomic.Pointer[Limiter])
	if cur := ptr.Load(); cur != nil {
		cur.SetLimits(limits)
		return cur
	}
	if !limits.Enabled() {
		return nil
	}
	fresh := New(ScopeServer, name, limits)
	ptr.Store(fresh)
	return fresh
}

// RetireServer tombstones a server's limiter: queued calls fail immediately
// with ErrServerUnavailable and later admissions are refused, even if the
// caller snapshotted the instance before the state change (FR-009,
// admit-after-disable race). Outstanding holds drain into the retired
// instance.
func (r *Registry) RetireServer(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retireServerLocked(name)
}

func (r *Registry) retireServerLocked(name string) {
	v, ok := r.servers.Load(name)
	if !ok {
		return
	}
	ptr, _ := v.(*atomic.Pointer[Limiter])
	if cur := ptr.Swap(nil); cur != nil {
		cur.Retire()
	}
	r.servers.Delete(name)
}

// Apply publishes one atomic generation of limits for every scope (FR-021):
// the global aggregate plus the resolved per-server limits. Servers missing
// from the map are retired.
func (r *Registry) Apply(global Limits, servers map[string]Limits) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.setGlobalLocked(global)

	var stale []string
	r.servers.Range(func(key, _ any) bool {
		name, _ := key.(string)
		if _, ok := servers[name]; !ok {
			stale = append(stale, name)
		}
		return true
	})
	for _, name := range stale {
		r.retireServerLocked(name)
	}
	for name, limits := range servers {
		r.setServerLocked(name, limits)
	}
}

// Acquire admits a call through both tiers under ONE absolute queue deadline
// (FR-004). The per-server slot is taken first so a slow upstream's queue does
// not pin global capacity while waiting (FR-002). On a global rejection the
// per-server slot is released again.
//
// The returned release closure releases both tiers and is safe to call more
// than once.
func (r *Registry) Acquire(ctx context.Context, server string, queueDeadline time.Time) (func(), error) {
	if r == nil {
		return noopRelease, nil
	}

	releaseServer, err := r.Server(server).Acquire(ctx, queueDeadline)
	if err != nil {
		return nil, err
	}
	releaseGlobal, err := r.Global().Acquire(ctx, queueDeadline)
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
// server name (FR-013: per-server queue depth for the metrics surface).
func (r *Registry) ServerStats() map[string]Stats {
	if r == nil {
		return nil
	}
	out := make(map[string]Stats)
	r.servers.Range(func(key, value any) bool {
		name, _ := key.(string)
		ptr, _ := value.(*atomic.Pointer[Limiter])
		if lim := ptr.Load(); lim != nil {
			out[name] = lim.Stats()
		}
		return true
	})
	return out
}
