package managed

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

func intPtrAdm(v int) *int { return &v }

func durPtrAdm(d time.Duration) *config.Duration {
	cd := config.Duration(d)
	return &cd
}

// newAdmissionClient builds a bare managed client wired to a registry built
// from cfg, exactly the way Manager.AddServerConfig wires a real one.
func newAdmissionClient(t *testing.T, cfg *config.Config, serverName string, observe limiter.Observer) (*Client, *limiter.Registry) {
	t.Helper()
	t.Setenv("CI", "")

	mc := newTestClientForHealth(t)
	sc := &config.ServerConfig{Name: serverName, Enabled: true}
	for _, s := range cfg.Servers {
		if s.Name == serverName {
			sc = s
		}
	}
	mc.SetConfig(sc)
	mc.SetGlobalConfig(cfg)

	reg := limiter.NewRegistry()
	servers := make(map[string]limiter.Limits)
	for _, s := range cfg.Servers {
		r := cfg.ResolveServerConcurrency(s)
		servers[s.Name] = limiter.Limits{Max: r.MaxConcurrentRequests, QueueSize: r.QueueSize, QueueTimeout: r.QueueTimeout}
	}
	g := cfg.ResolveGlobalConcurrency()
	reg.Apply(limiter.Limits{Max: g.MaxConcurrentRequests, QueueSize: g.QueueSize, QueueTimeout: g.QueueTimeout}, servers)

	mc.SetAdmissionControl(reg, observe)
	return mc, reg
}

// TestAcquireAdmission_NoRegistryIsPassthrough is the FR-006 guard.
func TestAcquireAdmission_NoRegistryIsPassthrough(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.SetGlobalConfig(&config.Config{})

	release, err := mc.acquireAdmission(context.Background(), "tool")
	require.NoError(t, err)
	require.NotNil(t, release)
	release()
}

// TestAcquireAdmission_QueueFullShedsImmediately covers FR-004 / SC-005: with
// the cap taken and no pending capacity, admission is refused without waiting.
func TestAcquireAdmission_QueueFullShedsImmediately(t *testing.T) {
	sc := &config.ServerConfig{
		Name:                  "db",
		Enabled:               true,
		MaxConcurrentRequests: intPtrAdm(1),
		QueueSize:             intPtrAdm(0),
		QueueTimeout:          durPtrAdm(30 * time.Second),
	}
	cfg := &config.Config{Servers: []*config.ServerConfig{sc}}

	var mu sync.Mutex
	var seen []limiter.Rejection
	mc, _ := newAdmissionClient(t, cfg, "db", func(_ context.Context, rej limiter.Rejection) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, rej)
	})

	first, err := mc.acquireAdmission(context.Background(), "query")
	require.NoError(t, err)

	start := time.Now()
	_, err = mc.acquireAdmission(context.Background(), "query")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.True(t, errors.Is(err, limiter.ErrQueueFull))
	assert.Less(t, elapsed, 100*time.Millisecond, "a queue-full shed must not wait (SC-005)")

	var limitErr *limiter.LimitError
	require.True(t, errors.As(err, &limitErr))
	assert.Equal(t, limiter.ScopeServer, limitErr.Scope)
	assert.Equal(t, "db", limitErr.Server)
	assert.Equal(t, 1, limitErr.Limit)
	assert.Equal(t, 30*time.Second, limitErr.RetryAfter)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 1, "the shed must reach the origin-independent observer (FR-012)")
	assert.Equal(t, "db", seen[0].Server)
	assert.Equal(t, "query", seen[0].Tool)
	assert.Equal(t, limiter.ReasonQueueFull, seen[0].Reason)
	assert.Equal(t, limiter.ScopeServer, seen[0].Scope)
	assert.Contains(t, seen[0].Message, "db")

	first()
}

// TestAcquireAdmission_QueueTimeoutUsesResolvedBudget covers FR-004's single
// absolute deadline: the wait budget comes from the resolved queue_timeout.
func TestAcquireAdmission_QueueTimeoutUsesResolvedBudget(t *testing.T) {
	sc := &config.ServerConfig{
		Name:                  "db",
		Enabled:               true,
		MaxConcurrentRequests: intPtrAdm(1),
		QueueSize:             intPtrAdm(2),
		QueueTimeout:          durPtrAdm(120 * time.Millisecond),
	}
	cfg := &config.Config{Servers: []*config.ServerConfig{sc}}

	rejections := make(chan limiter.Rejection, 4)
	mc, _ := newAdmissionClient(t, cfg, "db", func(_ context.Context, rej limiter.Rejection) {
		rejections <- rej
	})

	first, err := mc.acquireAdmission(context.Background(), "query")
	require.NoError(t, err)
	defer first()

	start := time.Now()
	_, err = mc.acquireAdmission(context.Background(), "query")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.True(t, errors.Is(err, limiter.ErrQueueTimeout))
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	assert.Less(t, elapsed, 3*time.Second)

	select {
	case rej := <-rejections:
		assert.Equal(t, limiter.ReasonQueueTimeout, rej.Reason)
		assert.GreaterOrEqual(t, rej.Waited, 100*time.Millisecond)
	case <-time.After(time.Second):
		t.Fatal("queue-timeout shed never reached the observer")
	}
}

// TestAcquireAdmission_GlobalScopeNeverBlamesAServer covers FR-010's rule that
// a proxy-wide rejection must not name an upstream.
func TestAcquireAdmission_GlobalScopeNeverBlamesAServer(t *testing.T) {
	sc := &config.ServerConfig{Name: "db", Enabled: true}
	cfg := &config.Config{
		MaxConcurrentRequests: intPtrAdm(1),
		QueueSize:             intPtrAdm(0),
		QueueTimeout:          durPtrAdm(5 * time.Second),
		Servers:               []*config.ServerConfig{sc},
	}

	rejections := make(chan limiter.Rejection, 4)
	mc, _ := newAdmissionClient(t, cfg, "db", func(_ context.Context, rej limiter.Rejection) {
		rejections <- rej
	})

	first, err := mc.acquireAdmission(context.Background(), "query")
	require.NoError(t, err)
	defer first()

	_, err = mc.acquireAdmission(context.Background(), "query")
	require.Error(t, err)

	var limitErr *limiter.LimitError
	require.True(t, errors.As(err, &limitErr))
	assert.Equal(t, limiter.ScopeGlobal, limitErr.Scope)
	assert.Empty(t, limitErr.Server, "a global rejection must not carry a server name")
	assert.NotContains(t, limitErr.Error(), "db")

	rej := <-rejections
	assert.Equal(t, limiter.ScopeGlobal, rej.Scope)
	assert.Equal(t, "db", rej.Server, "the metric/activity label still needs the target server")
	assert.NotContains(t, rej.Message, "db")
}

// TestAcquireAdmission_CallerCancelIsNotAShed covers the FR-005 edge case: a
// caller that goes away while queued is reported as cancelled and produces no
// rejection record.
func TestAcquireAdmission_CallerCancelIsNotAShed(t *testing.T) {
	sc := &config.ServerConfig{
		Name:                  "db",
		Enabled:               true,
		MaxConcurrentRequests: intPtrAdm(1),
		QueueSize:             intPtrAdm(2),
		QueueTimeout:          durPtrAdm(30 * time.Second),
	}
	cfg := &config.Config{Servers: []*config.ServerConfig{sc}}

	var mu sync.Mutex
	rejected := 0
	mc, reg := newAdmissionClient(t, cfg, "db", func(_ context.Context, _ limiter.Rejection) {
		mu.Lock()
		rejected++
		mu.Unlock()
	})

	first, err := mc.acquireAdmission(context.Background(), "query")
	require.NoError(t, err)
	defer first()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, aerr := mc.acquireAdmission(ctx, "query")
		done <- aerr
	}()

	require.Eventually(t, func() bool { return reg.Server("db").Stats().Queued == 1 },
		2*time.Second, 5*time.Millisecond)
	cancel()

	err = <-done
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.False(t, errors.Is(err, limiter.ErrQueueTimeout))

	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, rejected, "a caller cancellation is not a shed")
}

// TestAcquireAdmission_HotReloadRaisesCapMidQueue extends the global-config
// hot-reload coverage to concurrency limits (FR-021).
func TestAcquireAdmission_HotReloadRaisesCapMidQueue(t *testing.T) {
	sc := &config.ServerConfig{
		Name:                  "db",
		Enabled:               true,
		MaxConcurrentRequests: intPtrAdm(1),
		QueueSize:             intPtrAdm(2),
		QueueTimeout:          durPtrAdm(30 * time.Second),
	}
	cfg := &config.Config{Servers: []*config.ServerConfig{sc}}
	mc, reg := newAdmissionClient(t, cfg, "db", nil)

	first, err := mc.acquireAdmission(context.Background(), "query")
	require.NoError(t, err)
	defer first()

	done := make(chan error, 1)
	go func() {
		_, aerr := mc.acquireAdmission(context.Background(), "query")
		done <- aerr
	}()
	require.Eventually(t, func() bool { return reg.Server("db").Stats().Queued == 1 },
		2*time.Second, 5*time.Millisecond)

	// Operator raises the cap: the queued call must be admitted immediately.
	reg.SetServer("db", limiter.Limits{Max: 3, QueueSize: 2, QueueTimeout: 30 * time.Second})

	select {
	case aerr := <-done:
		require.NoError(t, aerr)
	case <-time.After(2 * time.Second):
		t.Fatal("raising the cap must admit an eligible queued call immediately (FR-021)")
	}
}

// agentCtx is the request context of a call made with an agent token.
func agentCtx(name string, rate *auth.TokenRateLimit) context.Context {
	tok := &auth.AgentToken{Name: name, TokenPrefix: "mcp_agt_" + name[:1] + "xxx", RateLimit: rate}
	return auth.WithAuthContext(context.Background(), tok.AuthContext())
}

// newRateLimitedClient is newAdmissionClient with a default per-token budget
// and a fake clock, plus the rejections the observer saw.
func newRateLimitedClient(t *testing.T, def limiter.RateSpec) (*Client, *limiter.Registry, *time.Time, func() []limiter.Rejection) {
	t.Helper()
	var mu sync.Mutex
	var seen []limiter.Rejection
	mc, reg := newAdmissionClient(t, &config.Config{}, "db", func(_ context.Context, rej limiter.Rejection) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, rej)
	})
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	reg.TokenRates().SetClock(func() time.Time { return now })
	reg.TokenRates().SetDefault(def)
	return mc, reg, &now, func() []limiter.Rejection {
		mu.Lock()
		defer mu.Unlock()
		return append([]limiter.Rejection(nil), seen...)
	}
}

// TestAcquireAdmission_TokenRate covers issue #1699 at the shared seam: burst
// allowed, then refused with a typed, observable rate_limited rejection, and
// the budget refills with time.
func TestAcquireAdmission_TokenRate(t *testing.T) {
	mc, _, now, rejections := newRateLimitedClient(t, limiter.RateSpec{PerMinute: 60, Burst: 2})
	ctx := agentCtx("looper", nil)

	for i := 0; i < 2; i++ {
		release, err := mc.acquireAdmission(ctx, "query")
		require.NoError(t, err, "call %d is inside the burst", i+1)
		release()
	}
	require.Empty(t, rejections())

	_, err := mc.acquireAdmission(ctx, "query")
	require.Error(t, err)
	var limitErr *limiter.LimitError
	require.True(t, errors.As(err, &limitErr))
	assert.Equal(t, limiter.ScopeToken, limitErr.Scope)
	assert.Equal(t, limiter.ReasonRateLimited, limitErr.Reason)
	assert.True(t, errors.Is(err, limiter.ErrRateLimited))
	assert.Equal(t, 60, limitErr.Limit)
	assert.Equal(t, 2, limitErr.Burst)
	assert.Equal(t, time.Second, limitErr.RetryAfter)

	seen := rejections()
	require.Len(t, seen, 1, "the refusal is reported exactly once")
	assert.Equal(t, "db", seen[0].Server)
	assert.Equal(t, "query", seen[0].Tool)
	assert.Equal(t, limiter.ScopeToken, seen[0].Scope)
	assert.Equal(t, limiter.ReasonRateLimited, seen[0].Reason)
	assert.Equal(t, "looper", seen[0].Token.Name)
	assert.Equal(t, "mcp_agt_lxxx", seen[0].Token.Prefix)
	assert.Equal(t, time.Second, seen[0].RetryAfter)
	assert.Equal(t, time.Duration(0), seen[0].Waited)

	*now = now.Add(time.Second)
	release, err := mc.acquireAdmission(ctx, "query")
	require.NoError(t, err, "a second later one call has refilled")
	release()
}

// TestAcquireAdmission_TokenRateIsPerToken: one runaway token does not slow
// another, and each token's own rate_limit replaces the default.
func TestAcquireAdmission_TokenRateIsPerToken(t *testing.T) {
	mc, _, _, _ := newRateLimitedClient(t, limiter.RateSpec{PerMinute: 60, Burst: 1})

	a, b := agentCtx("aaa", nil), agentCtx("bbb", nil)
	release, err := mc.acquireAdmission(a, "q")
	require.NoError(t, err)
	release()
	_, err = mc.acquireAdmission(a, "q")
	require.True(t, errors.Is(err, limiter.ErrRateLimited), "aaa is spent")

	release, err = mc.acquireAdmission(b, "q")
	require.NoError(t, err, "bbb has its own bucket")
	release()

	generous := agentCtx("ccc", &auth.TokenRateLimit{PerMinute: 60, Burst: 3})
	for i := 0; i < 3; i++ {
		release, err = mc.acquireAdmission(generous, "q")
		require.NoError(t, err, "ccc's own burst of 3 replaces the default of 1")
		release()
	}
	_, err = mc.acquireAdmission(generous, "q")
	require.True(t, errors.Is(err, limiter.ErrRateLimited))

	exempt := agentCtx("ddd", &auth.TokenRateLimit{PerMinute: 0})
	for i := 0; i < 50; i++ {
		release, err = mc.acquireAdmission(exempt, "q")
		require.NoError(t, err, "per_minute 0 opts a token out of the default")
		release()
	}
}

// TestAcquireAdmission_TokenRateOnlyLimitsAgentTokens: the admin API key, an
// unauthenticated /mcp caller and a call with no request identity at all are
// never rate limited, however small the default.
func TestAcquireAdmission_TokenRateOnlyLimitsAgentTokens(t *testing.T) {
	mc, _, _, rejections := newRateLimitedClient(t, limiter.RateSpec{PerMinute: 1, Burst: 1})

	callers := map[string]context.Context{
		"no auth context": context.Background(),
		"admin":           auth.WithAuthContext(context.Background(), auth.AdminContext()),
		"anonymous":       auth.WithAuthContext(context.Background(), auth.AnonymousContext()),
		"agent without a name": auth.WithAuthContext(context.Background(),
			&auth.AuthContext{Type: auth.AuthTypeAgent}),
	}
	for name, ctx := range callers {
		for i := 0; i < 20; i++ {
			release, err := mc.acquireAdmission(ctx, "q")
			require.NoError(t, err, "%s call %d", name, i+1)
			release()
		}
	}
	assert.Empty(t, rejections())
}

// TestAcquireAdmission_TokenRateOffByDefault: with nothing configured an agent
// token is not limited and no state is kept for it.
func TestAcquireAdmission_TokenRateOffByDefault(t *testing.T) {
	mc, reg, _, rejections := newRateLimitedClient(t, limiter.RateSpec{})

	ctx := agentCtx("busy", nil)
	for i := 0; i < 500; i++ {
		release, err := mc.acquireAdmission(ctx, "q")
		require.NoError(t, err)
		release()
	}
	assert.Empty(t, rejections())
	assert.Equal(t, 0, reg.TokenRates().Len())
}

// TestAcquireAdmission_RateLimitedCallTakesNoConcurrencySlot: a refused call is
// refused before it queues, so it neither holds nor waits for a slot.
func TestAcquireAdmission_RateLimitedCallTakesNoConcurrencySlot(t *testing.T) {
	sc := &config.ServerConfig{
		Name:                  "db",
		Enabled:               true,
		MaxConcurrentRequests: intPtrAdm(1),
		QueueSize:             intPtrAdm(5),
		QueueTimeout:          durPtrAdm(30 * time.Second),
	}
	var mu sync.Mutex
	var seen []limiter.Rejection
	mc, reg := newAdmissionClient(t, &config.Config{Servers: []*config.ServerConfig{sc}}, "db",
		func(_ context.Context, rej limiter.Rejection) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, rej)
		})
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	reg.TokenRates().SetClock(func() time.Time { return now })
	reg.TokenRates().SetDefault(limiter.RateSpec{PerMinute: 60, Burst: 1})

	ctx := agentCtx("looper", nil)
	hold, err := mc.acquireAdmission(ctx, "q")
	require.NoError(t, err)
	defer hold()

	// The only slot is held. The call is over budget, so it must answer at
	// once with rate_limited, not wait 30s in the queue for a slot.
	start := time.Now()
	_, err = mc.acquireAdmission(ctx, "q")
	require.True(t, errors.Is(err, limiter.ErrRateLimited), "got %v", err)
	assert.Less(t, time.Since(start), 5*time.Second)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 1)
	assert.Equal(t, limiter.ReasonRateLimited, seen[0].Reason)
	assert.Equal(t, 0, reg.Server("db").Stats().Queued, "nothing was queued")
}
