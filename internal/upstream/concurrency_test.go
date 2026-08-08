package upstream

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

func intPtr(v int) *int { return &v }

func durPtrConc(d time.Duration) *config.Duration {
	cd := config.Duration(d)
	return &cd
}

// limitedServerConfig is a server with a 1-at-a-time limit and one queue slot.
func limitedServerConfig(name string) *config.ServerConfig {
	return &config.ServerConfig{
		Name:                  name,
		URL:                   "http://127.0.0.1:1",
		Protocol:              "http",
		Enabled:               true,
		MaxConcurrentRequests: intPtr(1),
		QueueSize:             intPtr(1),
		QueueTimeout:          durPtrConc(30 * time.Second),
		Created:               time.Now(),
	}
}

// newConcurrencyManager builds a real Manager (limiter registry included) with
// one Ready client, so tool calls reach the admission seam without a live
// upstream transport.
func newConcurrencyManager(t *testing.T, cfg *config.Config, serverCfg *config.ServerConfig) *Manager {
	t.Helper()
	t.Setenv("CI", "")

	m := NewManager(zap.NewNop(), cfg, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })

	require.NoError(t, m.AddServerConfig(serverCfg.Name, serverCfg))
	client, ok := m.GetClient(serverCfg.Name)
	require.True(t, ok)
	client.StateManager.TransitionTo(types.StateConnecting)
	client.StateManager.TransitionTo(types.StateReady)
	require.True(t, client.IsConnected())
	return m
}

// TestManagerCallTool_QueuedCallDoesNotBlockServerManagement is the FR-008
// regression test. Manager.CallTool used to hold m.mu.RLock for the entire
// call, so a call parked in the limiter queue would stall every AddServer /
// RemoveServer / config reload behind the manager lock.
func TestManagerCallTool_QueuedCallDoesNotBlockServerManagement(t *testing.T) {
	serverCfg := limitedServerConfig("slow-server")
	cfg := &config.Config{Servers: []*config.ServerConfig{serverCfg}}
	m := newConcurrencyManager(t, cfg, serverCfg)

	lim := m.Limiters().Server("slow-server")
	require.NotNil(t, lim, "a per-server limit must produce a limiter instance")

	// Occupy the single slot so the tool call below has to queue.
	release, err := lim.Acquire(context.Background(), time.Time{})
	require.NoError(t, err)

	callDone := make(chan error, 1)
	go func() {
		_, callErr := m.CallTool(context.Background(), "slow-server:some_tool", map[string]interface{}{})
		callDone <- callErr
	}()

	require.Eventually(t, func() bool { return lim.Stats().Queued == 1 },
		2*time.Second, 5*time.Millisecond, "call must park in the limiter queue")

	// With the lock held across the call, this would block until the queued
	// call finished. It must complete promptly instead.
	mgmtDone := make(chan error, 1)
	go func() {
		other := &config.ServerConfig{Name: "other", URL: "http://127.0.0.1:2", Protocol: "http", Enabled: true}
		mgmtDone <- m.AddServerConfig("other", other)
	}()
	select {
	case err := <-mgmtDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("AddServerConfig blocked behind a queued tool call (FR-008 regression)")
	}

	// FR-009: removing the server must fail the queued call immediately rather
	// than leaving it to hit the 30s queue timeout.
	m.RemoveServer("slow-server")
	select {
	case callErr := <-callDone:
		require.Error(t, callErr)
		assert.True(t, errors.Is(callErr, limiter.ErrServerUnavailable),
			"queued call must fail with server-unavailable, got: %v", callErr)
	case <-time.After(2 * time.Second):
		t.Fatal("removing a server did not promptly fail its queued call (FR-009)")
	}

	release()
}

// TestManagerCallTool_ConcurrentWithServerChurn is the -race deadlock guard for
// the restructured lock scope: dispatch must interleave freely with
// add/remove/config-reload.
func TestManagerCallTool_ConcurrentWithServerChurn(t *testing.T) {
	serverCfg := limitedServerConfig("churn-server")
	cfg := &config.Config{Servers: []*config.ServerConfig{serverCfg}}
	m := newConcurrencyManager(t, cfg, serverCfg)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				_, _ = m.CallTool(ctx, "churn-server:tool", map[string]interface{}{})
				cancel()
			}
		}()
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := fmt.Sprintf("churn-%d", id)
			for n := 0; n < 25; n++ {
				select {
				case <-stop:
					return
				default:
				}
				sc := &config.ServerConfig{Name: name, URL: "http://127.0.0.1:3", Protocol: "http", Enabled: true}
				_ = m.AddServerConfig(name, sc)
				m.RemoveServer(name)
				m.SetGlobalConfig(cfg)
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		time.Sleep(750 * time.Millisecond)
		close(stop)
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("deadlock: dispatch and server churn did not complete")
	}
}

// TestApplyConcurrencyLimits_HotReload covers FR-021 at the manager level: a new
// config generation republishes limits into the SAME limiter instance, so
// occupancy survives the swap.
func TestApplyConcurrencyLimits_HotReload(t *testing.T) {
	serverCfg := limitedServerConfig("hot")
	cfg := &config.Config{Servers: []*config.ServerConfig{serverCfg}}
	m := newConcurrencyManager(t, cfg, serverCfg)

	lim := m.Limiters().Server("hot")
	require.NotNil(t, lim)
	assert.Equal(t, 1, lim.Limits().Max)

	release, err := lim.Acquire(context.Background(), time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 1, lim.Stats().Running)

	raised := limitedServerConfig("hot")
	raised.MaxConcurrentRequests = intPtr(4)
	m.SetGlobalConfig(&config.Config{Servers: []*config.ServerConfig{raised}})

	assert.Same(t, lim, m.Limiters().Server("hot"), "hot reload must mutate the live instance, not replace it")
	assert.Equal(t, 4, lim.Limits().Max)
	assert.Equal(t, 1, lim.Stats().Running, "occupancy must be shared across generations")
	release()
}

// TestApplyConcurrencyLimits_GlobalAndDisabledServers verifies scope wiring:
// the global aggregate limiter is created from the top-level settings, and a
// disabled or quarantined server never owns a limiter (FR-009).
func TestApplyConcurrencyLimits_GlobalAndDisabledServers(t *testing.T) {
	t.Setenv("CI", "")

	enabled := limitedServerConfig("on")
	disabled := limitedServerConfig("off")
	disabled.Enabled = false
	quarantined := limitedServerConfig("quar")
	quarantined.Quarantined = true

	cfg := &config.Config{
		MaxConcurrentRequests: intPtr(10),
		QueueSize:             intPtr(5),
		QueueTimeout:          durPtrConc(2 * time.Second),
		Servers:               []*config.ServerConfig{enabled, disabled, quarantined},
	}

	m := NewManager(zap.NewNop(), cfg, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })

	global := m.Limiters().Global()
	require.NotNil(t, global)
	assert.Equal(t, 10, global.Limits().Max)
	assert.Equal(t, 5, global.Limits().QueueSize)
	assert.Equal(t, 2*time.Second, global.Limits().QueueTimeout)

	assert.NotNil(t, m.Limiters().Server("on"))
	assert.Nil(t, m.Limiters().Server("off"), "a disabled server must not own a limiter")
	assert.Nil(t, m.Limiters().Server("quar"), "a quarantined server must not own a limiter")
}

// TestZeroConfig_AdmissionIsPassthroughButCounted is the FR-006 guard paired
// with FR-021's shared occupancy. With no limits configured nothing is capped,
// queued or rejected — but the scopes still COUNT their running calls, because
// a cap enabled by a later hot reload has to see the calls already in flight.
func TestZeroConfig_AdmissionIsPassthroughButCounted(t *testing.T) {
	t.Setenv("CI", "")

	sc := &config.ServerConfig{Name: "plain", URL: "http://127.0.0.1:1", Protocol: "http", Enabled: true}
	cfg := &config.Config{Servers: []*config.ServerConfig{sc}}
	m := NewManager(zap.NewNop(), cfg, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })

	assert.Equal(t, 0, m.Limiters().Global().Limits().Max, "no global cap must be published")
	plain := m.Limiters().Server("plain")
	require.NotNil(t, plain)
	assert.Equal(t, 0, plain.Limits().Max, "no per-server cap must be published")

	release, err := m.Limiters().Acquire(context.Background(), "plain")
	require.NoError(t, err, "an unlimited scope never sheds")
	assert.Equal(t, 1, plain.Stats().Running, "occupancy is tracked even with no cap")
	assert.Equal(t, 0, plain.Stats().Queued, "an unlimited scope never queues")
	release()
	assert.Equal(t, 0, plain.Stats().Running)
}

// TestHotEnableSeesGrandfatheredCalls is the FR-021 regression test for the
// shared-occupancy rule at the point it is easiest to get wrong: enabling a cap
// on a scope that was previously UNLIMITED. Before the fix the new limiter
// started at running==0 while unlimited calls were still in flight, so the cap
// was exceeded by exactly the grandfathered count for as long as they ran.
func TestHotEnableSeesGrandfatheredCalls(t *testing.T) {
	t.Setenv("CI", "")

	sc := &config.ServerConfig{Name: "late", URL: "http://127.0.0.1:1", Protocol: "http", Enabled: true}
	cfg := &config.Config{Servers: []*config.ServerConfig{sc}}
	m := NewManager(zap.NewNop(), cfg, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })

	// Three unlimited calls in flight.
	const inFlight = 3
	releases := make([]func(), 0, inFlight)
	for i := 0; i < inFlight; i++ {
		release, err := m.Limiters().Acquire(context.Background(), "late")
		require.NoError(t, err)
		releases = append(releases, release)
	}

	// Operator hot-enables a cap BELOW the number already running.
	capped := &config.ServerConfig{
		Name: "late", URL: "http://127.0.0.1:1", Protocol: "http", Enabled: true,
		MaxConcurrentRequests: intPtr(2),
		QueueSize:             intPtr(0),
		QueueTimeout:          durPtrConc(30 * time.Second),
	}
	m.SetGlobalConfig(&config.Config{Servers: []*config.ServerConfig{capped}})

	lim := m.Limiters().Server("late")
	require.NotNil(t, lim)
	assert.Equal(t, inFlight, lim.Stats().Running,
		"the newly enabled cap must inherit the grandfathered occupancy")

	_, err := m.Limiters().Acquire(context.Background(), "late")
	require.Error(t, err, "no new admission until occupancy drains below the new cap")
	assert.True(t, errors.Is(err, limiter.ErrQueueFull))

	// Drain to one below the cap; the next call is admitted again.
	releases[0]()
	releases[1]()
	release, err := m.Limiters().Acquire(context.Background(), "late")
	require.NoError(t, err, "admission resumes once occupancy drops below the cap")
	release()
	releases[2]()
}
