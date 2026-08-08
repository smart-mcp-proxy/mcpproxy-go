package upstream

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// toLimiterLimits converts a resolved config scope into the limiter's shape.
func toLimiterLimits(r config.ResolvedConcurrency) limiter.Limits {
	return limiter.Limits{
		Max:          r.MaxConcurrentRequests,
		QueueSize:    r.QueueSize,
		QueueTimeout: r.QueueTimeout,
	}
}

// limiterEligible reports whether a server should own a limiter instance at all.
// A disabled or quarantined server must not: FR-009 requires its queued calls to
// fail promptly with the server-unavailable semantics rather than sit until the
// queue deadline, which is exactly what retiring its instance does.
func limiterEligible(sc *config.ServerConfig) bool {
	return sc != nil && sc.Name != "" && sc.Enabled && !sc.Quarantined
}

// Limiters exposes the live limiter registry (metrics surface, tests).
func (m *Manager) Limiters() *limiter.Registry {
	if m == nil {
		return nil
	}
	return m.limiters
}

// SetRejectionObserver installs the origin-independent shed seam (FR-012): the
// runtime passes a callback that turns a rejection into a "rejected" activity
// record and a rejection metric, regardless of which surface originated the
// call. Existing clients are re-wired immediately.
func (m *Manager) SetRejectionObserver(observe limiter.Observer) {
	if m == nil {
		return
	}
	if observe == nil {
		m.rejectObserver.Store(nil)
	} else {
		m.rejectObserver.Store(&observe)
	}

	m.mu.RLock()
	clients := make([]*managed.Client, 0, len(m.clients))
	for _, client := range m.clients {
		if client != nil {
			clients = append(clients, client)
		}
	}
	m.mu.RUnlock()

	for _, client := range clients {
		client.SetAdmissionControl(m.limiters, m.currentRejectObserver())
	}
}

// currentRejectObserver returns the installed observer, or nil.
func (m *Manager) currentRejectObserver() limiter.Observer {
	if m == nil {
		return nil
	}
	if p := m.rejectObserver.Load(); p != nil {
		return *p
	}
	return nil
}

// ConcurrencyStats reports the current occupancy of the global aggregate
// limiter and of every live per-server limiter (FR-013: queue depth).
func (m *Manager) ConcurrencyStats() (global limiter.Stats, servers map[string]limiter.Stats) {
	if m == nil || m.limiters == nil {
		return limiter.Stats{}, nil
	}
	return m.limiters.Global().Stats(), m.limiters.ServerStats()
}

// applyConcurrencyLimits republishes ONE generation of limits for every scope
// (FR-021). Called at construction and on every config hot-reload
// (SetGlobalConfig). Servers that are absent, disabled or quarantined are
// retired, which promptly fails their queued calls (FR-009).
func (m *Manager) applyConcurrencyLimits(cfg *config.Config) {
	if m == nil || m.limiters == nil {
		return
	}
	if cfg == nil {
		cfg = &config.Config{}
	}

	servers := make(map[string]limiter.Limits)
	for _, sc := range cfg.Servers {
		if !limiterEligible(sc) {
			continue
		}
		servers[sc.Name] = toLimiterLimits(cfg.ResolveServerConcurrency(sc))
	}

	// Clients added out-of-band (AddServerConfig before the new config snapshot
	// reaches the manager) must keep their limiter across the generation swap.
	m.mu.RLock()
	clientConfigs := make([]*config.ServerConfig, 0, len(m.clients))
	for _, client := range m.clients {
		if client != nil {
			clientConfigs = append(clientConfigs, client.GetConfig())
		}
	}
	m.mu.RUnlock()

	for _, sc := range clientConfigs {
		if !limiterEligible(sc) {
			continue
		}
		if _, ok := servers[sc.Name]; ok {
			continue
		}
		servers[sc.Name] = toLimiterLimits(cfg.ResolveServerConcurrency(sc))
	}

	m.limiters.Apply(toLimiterLimits(cfg.ResolveGlobalConcurrency()), servers)
}

// applyServerConcurrency republishes one server's limits (add / update path).
// A disabled or quarantined server is retired instead (FR-009).
func (m *Manager) applyServerConcurrency(sc *config.ServerConfig) {
	if m == nil || m.limiters == nil || sc == nil || sc.Name == "" {
		return
	}
	if !limiterEligible(sc) {
		m.limiters.RetireServer(sc.Name)
		return
	}
	cfg := m.globalConfig.Load()
	if cfg == nil {
		cfg = &config.Config{}
	}
	m.limiters.SetServer(sc.Name, toLimiterLimits(cfg.ResolveServerConcurrency(sc)))
}

// retireServerConcurrency tombstones a removed server's limiter so its queued
// calls fail immediately instead of waiting out the queue deadline (FR-009).
func (m *Manager) retireServerConcurrency(name string) {
	if m == nil || m.limiters == nil || name == "" {
		return
	}
	m.limiters.RetireServer(name)
}
