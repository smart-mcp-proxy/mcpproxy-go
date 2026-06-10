package upstream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func durPtr(d time.Duration) *config.Duration {
	cd := config.Duration(d)
	return &cd
}

// TestShouldSweepServer covers the pure per-server cadence decision that makes a
// per-server tool_discovery_interval override actually take effect on the
// periodic sweep (spec 074 US3/SC-006/FR-005): a disabled interval (<=0) is
// never swept; a never-swept server is due; otherwise it is swept only once its
// own interval has elapsed.
func TestShouldSweepServer(t *testing.T) {
	now := time.Unix(1_000_000, 0)

	t.Run("disabled interval never sweeps", func(t *testing.T) {
		assert.False(t, shouldSweepServer(0, time.Time{}, false, now))
		assert.False(t, shouldSweepServer(-1, now.Add(-time.Hour), true, now))
	})

	t.Run("never swept is due", func(t *testing.T) {
		assert.True(t, shouldSweepServer(30*time.Second, time.Time{}, false, now))
	})

	t.Run("not yet elapsed is skipped", func(t *testing.T) {
		assert.False(t, shouldSweepServer(30*time.Second, now.Add(-10*time.Second), true, now))
	})

	t.Run("elapsed is due", func(t *testing.T) {
		assert.True(t, shouldSweepServer(30*time.Second, now.Add(-40*time.Second), true, now))
		assert.True(t, shouldSweepServer(30*time.Second, now.Add(-30*time.Second), true, now),
			"exactly at the interval boundary is due")
	})
}

// TestPerServerDiscoveryIntervalDrivesGate proves the override is resolved with
// per-server precedence and then drives shouldSweepServer — i.e. the override is
// no longer inert. A per-server "0s" disables that server's periodic sweep even
// when the global cadence is positive; a per-server short interval is honored.
func TestPerServerDiscoveryIntervalDrivesGate(t *testing.T) {
	gc := &config.Config{ToolDiscoveryInterval: durPtr(5 * time.Minute)}
	now := time.Unix(1_000_000, 0)

	disabled := &config.ServerConfig{Name: "off", ToolDiscoveryInterval: durPtr(0)}
	fast := &config.ServerConfig{Name: "fast", ToolDiscoveryInterval: durPtr(30 * time.Second)}
	inherits := &config.ServerConfig{Name: "default"} // no override -> global 5m

	// Disabled server: skipped regardless of when it was last swept.
	assert.False(t, shouldSweepServer(gc.ResolveToolDiscoveryInterval(disabled), time.Time{}, false, now),
		"per-server 0s must disable the periodic sweep for that server")

	// Fast server swept 31s ago -> due (its own 30s elapsed) even though the
	// global 5m has not.
	assert.True(t, shouldSweepServer(gc.ResolveToolDiscoveryInterval(fast), now.Add(-31*time.Second), true, now),
		"per-server short interval must be honored against the global default")

	// Inheriting server swept 31s ago -> NOT due (global 5m governs it).
	assert.False(t, shouldSweepServer(gc.ResolveToolDiscoveryInterval(inherits), now.Add(-31*time.Second), true, now),
		"a server without an override follows the global cadence")
}

// TestMinEnabledInterval covers the pure tick-selection logic: the smallest
// strictly-positive interval wins; all-disabled yields anyEnabled=false.
func TestMinEnabledInterval(t *testing.T) {
	tick, any := minEnabledInterval(5*time.Minute, 30*time.Second, 10*time.Minute)
	assert.True(t, any)
	assert.Equal(t, 30*time.Second, tick, "fastest enabled cadence wins")

	tick, any = minEnabledInterval(0, -1, 45*time.Second)
	assert.True(t, any)
	assert.Equal(t, 45*time.Second, tick, "disabled values are ignored")

	_, any = minEnabledInterval(0, -1, 0)
	assert.False(t, any, "all-disabled -> loop idles")

	_, any = minEnabledInterval()
	assert.False(t, any)
}

// TestResolveToolDiscoverySweepTick proves the loop tick is resolved from the
// manager's thread-safe client snapshot (not by iterating cfg.Servers, which
// raced in MCP-1189) and honors a short per-server override even when the global
// cadence is longer (US3/SC-006).
func TestResolveToolDiscoverySweepTick(t *testing.T) {
	gc := &config.Config{ToolDiscoveryInterval: durPtr(5 * time.Minute)}

	t.Run("fast per-server override drives the tick", func(t *testing.T) {
		fast := &config.ServerConfig{
			Name: "fast", URL: "http://127.0.0.1:0", Protocol: "http", Enabled: true,
			ToolDiscoveryInterval: durPtr(30 * time.Second),
		}
		manager, _ := createTestManagerWithClient(t, fast)
		tick, anyEnabled := manager.ResolveToolDiscoverySweepTick(gc)
		assert.True(t, anyEnabled)
		assert.Equal(t, 30*time.Second, tick, "min(global 5m, server 30s) == 30s")
	})

	t.Run("server without override follows the global cadence", func(t *testing.T) {
		inherit := &config.ServerConfig{
			Name: "inherit", URL: "http://127.0.0.1:0", Protocol: "http", Enabled: true,
		}
		manager, _ := createTestManagerWithClient(t, inherit)
		tick, anyEnabled := manager.ResolveToolDiscoverySweepTick(gc)
		assert.True(t, anyEnabled)
		assert.Equal(t, 5*time.Minute, tick)
	})

	t.Run("nil manager and nil config do not panic", func(t *testing.T) {
		tick, anyEnabled := (*Manager)(nil).ResolveToolDiscoverySweepTick(gc)
		assert.True(t, anyEnabled)
		assert.Equal(t, 5*time.Minute, tick, "nil manager -> global default only")

		tick, anyEnabled = (*Manager)(nil).ResolveToolDiscoverySweepTick(nil)
		assert.True(t, anyEnabled)
		assert.Equal(t, 5*time.Minute, tick, "nil config -> built-in default")
	})
}

// TestManager_SweptStateRoundTrip exercises the per-server sweep-time tracking
// seam (markSwept/lastSweptFor/pruneSweptState) on a real manager, including the
// lazy-init path for a hand-built manager with a nil lastSweptAt map.
func TestManager_SweptStateRoundTrip(t *testing.T) {
	m := &Manager{} // nil lastSweptAt — markSwept must lazy-init

	if _, ok := m.lastSweptFor("a"); ok {
		t.Fatal("expected no prior sweep for a fresh server")
	}

	now := time.Unix(1_000_000, 0)
	m.markSwept("a", now)
	m.markSwept("b", now)

	last, ok := m.lastSweptFor("a")
	assert.True(t, ok)
	assert.Equal(t, now, last)

	// Pruning drops servers no longer present.
	m.pruneSweptState(map[string]struct{}{"a": {}})
	if _, ok := m.lastSweptFor("b"); ok {
		t.Error("pruneSweptState should have dropped removed server b")
	}
	if _, ok := m.lastSweptFor("a"); !ok {
		t.Error("pruneSweptState must keep still-present server a")
	}
}

// TestManager_SetGlobalConfig_PropagatesToClients proves Codex finding #2's fix:
// a global config hot-reload reaches the upstream manager AND every running
// managed client, so the client's health loop will re-resolve the new global
// health_check_interval (FR-012/SC-002).
func TestManager_SetGlobalConfig_PropagatesToClients(t *testing.T) {
	serverConfig := &config.ServerConfig{
		Name:     "hot-reload-server",
		URL:      "http://127.0.0.1:0",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now(),
	}
	manager, client := createTestManagerWithClient(t, serverConfig)

	newGlobal := &config.Config{HealthCheckInterval: durPtr(12 * time.Second)}
	manager.SetGlobalConfig(newGlobal)

	// Manager view updated.
	assert.Same(t, newGlobal, manager.globalConfig.Load(), "manager must hold the new global config")

	// Existing client view updated -> its health loop will re-resolve to 12s.
	assert.Same(t, newGlobal, client.GetGlobalConfig(), "running client must receive the new global config")
}
