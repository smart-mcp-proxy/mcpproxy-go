package managed

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// TestSetGlobalConfig_HealthIntervalHotReload is the client-level proof of Codex
// finding #2's fix: the background health loop re-resolves resolveHealthCheckInterval
// every cycle, so swapping the global config via SetGlobalConfig changes the
// effective cadence of a RUNNING client without a restart (spec 074, FR-012).
func TestSetGlobalConfig_HealthIntervalHotReload(t *testing.T) {
	mc := newTestClientForHealth(t)

	// Boot global: 45s. The server has no per-server override, so the resolved
	// interval is the global value.
	mc.SetGlobalConfig(&config.Config{HealthCheckInterval: durPtrHC(45 * time.Second)})
	assert.Equal(t, 45*time.Second, mc.resolveHealthCheckInterval(),
		"resolved interval must follow the boot global")

	// Operator edits the global cadence to 10s and ApplyConfig propagates it.
	mc.SetGlobalConfig(&config.Config{HealthCheckInterval: durPtrHC(10 * time.Second)})
	assert.Equal(t, 10*time.Second, mc.resolveHealthCheckInterval(),
		"a global hot-reload must change a running client's resolved cadence")

	// Disabling the global loop (0s) must propagate too.
	mc.SetGlobalConfig(&config.Config{HealthCheckInterval: durPtrHC(0)})
	assert.LessOrEqual(t, mc.resolveHealthCheckInterval(), time.Duration(0),
		"0s global must disable the running client's probe loop")
}

func durPtrHC(d time.Duration) *config.Duration {
	cd := config.Duration(d)
	return &cd
}

// TestSetGlobalConfig_QueueBudgetHotReload extends the same hot-reload contract
// to the spec-093 concurrency limits: the wait budget the NEXT admission gets is
// republished with every generation, and it is always the smallest positive
// queue_timeout among the enabled scopes (FR-004, FR-021).
//
// It asserts the budget the REGISTRY publishes rather than a config-layer
// re-derivation, because the registry's generation is what admission actually
// resolves — a deadline computed anywhere else could belong to another
// generation than the caps it is applied to.
func TestSetGlobalConfig_QueueBudgetHotReload(t *testing.T) {
	sc := &config.ServerConfig{
		Name:                  "flap-server",
		Enabled:               true,
		MaxConcurrentRequests: intPtrAdm(2),
		QueueTimeout:          durPtrHC(20 * time.Second),
	}

	publish := func(cfg *config.Config) *limiter.Registry {
		reg := limiter.NewRegistry()
		servers := make(map[string]limiter.Limits)
		for _, s := range cfg.Servers {
			r := cfg.ResolveServerConcurrency(s)
			servers[s.Name] = limiter.Limits{Max: r.MaxConcurrentRequests, QueueSize: r.QueueSize, QueueTimeout: r.QueueTimeout}
		}
		g := cfg.ResolveGlobalConcurrency()
		reg.Apply(limiter.Limits{Max: g.MaxConcurrentRequests, QueueSize: g.QueueSize, QueueTimeout: g.QueueTimeout}, servers)
		return reg
	}

	// Boot: only the per-server scope limits, so its timeout is the budget.
	reg := publish(&config.Config{Servers: []*config.ServerConfig{sc}})
	assert.Equal(t, 20*time.Second, reg.QueueBudget("flap-server"))

	// Operator adds a stricter global aggregate limiter: the shared absolute
	// deadline must follow the smaller of the two.
	reg = publish(&config.Config{
		MaxConcurrentRequests: intPtrAdm(50),
		QueueTimeout:          durPtrHC(3 * time.Second),
		Servers:               []*config.ServerConfig{sc},
	})
	assert.Equal(t, 3*time.Second, reg.QueueBudget("flap-server"))

	// Disabling both limiters leaves nothing to wait for.
	off := &config.ServerConfig{Name: "flap-server", Enabled: true, MaxConcurrentRequests: intPtrAdm(0)}
	reg = publish(&config.Config{Servers: []*config.ServerConfig{off}})
	assert.Equal(t, time.Duration(0), reg.QueueBudget("flap-server"))
}
