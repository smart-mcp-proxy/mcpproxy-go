package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// Spec 093 (GH #955) end-to-end coverage against a REAL slow upstream.
//
// The synthetic tests in concurrency_shed_test.go / admission_test.go prove the
// limiter's own semantics; this file proves the whole chain — config resolution
// → registry generation → managed-client admission → live streamable-HTTP
// upstream — behaves as User Story 1 and 2 describe:
//
//   - US1 independent test: max_concurrent_requests=1, queue_size=1, three
//     concurrent calls ⇒ one runs, one queues then runs, one is shed, and the
//     UPSTREAM never observes two simultaneous requests (SC-001).
//   - US2 scenario 1 / SC-005: the shed is immediate (<100ms) and carries the
//     typed ErrQueueFull identity that the MCP/REST shed seams key off.
//   - FR-003: the sandboxed code-execution origin (upstreamToolCaller, which
//     never traverses handleCallToolVariant) is bounded by the same limiter.
//   - US2 scenario 2 / FR-004: a queued call whose wait exceeds queue_timeout is
//     shed with ErrQueueTimeout.

const slowUpstreamName = "slowsrv"

// slowUpstream is an in-process streamable-HTTP MCP server whose single tool
// blocks until released, while recording the peak number of simultaneous
// invocations it observed.
type slowUpstream struct {
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	entered     chan struct{}
	release     chan struct{}
	addr        string
}

// startSlowUpstream boots the fake upstream and returns it. Every invocation of
// its "slow_op" tool announces itself on `entered` and then waits for a token on
// `release`, so the test controls exactly how long a call occupies its slot.
func startSlowUpstream(t *testing.T) *slowUpstream {
	t.Helper()

	up := &slowUpstream{
		entered: make(chan struct{}, 16),
		release: make(chan struct{}, 16),
	}

	mcpSrv := mcpserver.NewMCPServer("slow", "1.0.0-test", mcpserver.WithToolCapabilities(true))
	tool := mcp.Tool{
		Name:        "slow_op",
		Description: "Blocks until the test releases it",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]any{}},
	}
	mcpSrv.AddTool(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cur := up.inFlight.Add(1)
		for {
			peak := up.maxInFlight.Load()
			if cur <= peak || up.maxInFlight.CompareAndSwap(peak, cur) {
				break
			}
		}
		defer up.inFlight.Add(-1)

		up.entered <- struct{}{}
		select {
		case <-up.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Second):
		}
		return mcp.NewToolResultText("done"), nil
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	up.addr = fmt.Sprintf("http://%s", ln.Addr().String())

	httpSrv := &http.Server{Handler: mcpserver.NewStreamableHTTPServer(mcpSrv), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Shutdown(context.Background()) })

	return up
}

// waitEntered blocks until n calls have reached the upstream tool body.
func (u *slowUpstream) waitEntered(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-u.entered:
		case <-time.After(15 * time.Second):
			t.Fatalf("timed out waiting for upstream invocation %d/%d", i+1, n)
		}
	}
}

// releaseN unblocks n in-flight upstream calls.
func (u *slowUpstream) releaseN(n int) {
	for i := 0; i < n; i++ {
		u.release <- struct{}{}
	}
}

// newLimitedManager wires the slow upstream into a real upstream.Manager with
// the given per-server concurrency limits and waits for it to connect.
func newLimitedManager(t *testing.T, up *slowUpstream, maxConcurrent, queueSize int, queueTimeout time.Duration) *upstream.Manager {
	t.Helper()

	maxCopy, queueCopy := maxConcurrent, queueSize
	timeoutCopy := config.Duration(queueTimeout)
	serverCfg := &config.ServerConfig{
		Name:                  slowUpstreamName,
		URL:                   up.addr,
		Protocol:              "streamable-http",
		Enabled:               true,
		MaxConcurrentRequests: &maxCopy,
		QueueSize:             &queueCopy,
		QueueTimeout:          &timeoutCopy,
	}
	cfg := &config.Config{
		Servers:         []*config.ServerConfig{serverCfg},
		CallToolTimeout: config.Duration(60 * time.Second),
		ToolsLimit:      15,
	}

	// Validation is part of the contract under test: these limits must be a
	// legal configuration (FR-023).
	require.Empty(t, cfg.ValidateDetailed(), "concurrency limits must pass config validation")

	um := upstream.NewManager(zap.NewNop(), cfg, nil, secret.NewResolver(), nil)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = um.ShutdownAll(shutdownCtx)
	})

	require.NoError(t, um.AddServerConfig(slowUpstreamName, serverCfg))
	require.NoError(t, um.ConnectAll(context.Background()))
	require.Eventually(t, func() bool {
		client, ok := um.GetClient(slowUpstreamName)
		return ok && client.IsConnected()
	}, 15*time.Second, 50*time.Millisecond, "slow upstream must connect")

	lim := um.Limiters().Server(slowUpstreamName)
	require.NotNil(t, lim, "a limited server must own a limiter instance")
	require.Equal(t, maxConcurrent, lim.Limits().Max)

	return um
}

// callResult carries the outcome of one asynchronous dispatch.
type callResult struct {
	err error
}

// dispatchAsync fires manager.CallTool — the single entry point every MCP,
// REST and direct-routing origin funnels through — on its own goroutine.
func dispatchAsync(um *upstream.Manager, wg *sync.WaitGroup, out *callResult) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := um.CallTool(context.Background(), slowUpstreamName+":slow_op", map[string]interface{}{})
		out.err = err
	}()
}

// TestE2EConcurrencyLimits_QueueThenShed is the User Story 1 + 2 independent
// test against a live upstream (FR-001..FR-004, FR-010, SC-001, SC-005).
func TestE2EConcurrencyLimits_QueueThenShed(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	up := startSlowUpstream(t)
	um := newLimitedManager(t, up, 1, 1, 10*time.Second)
	lim := um.Limiters().Server(slowUpstreamName)

	var wg sync.WaitGroup
	var first, second callResult

	// Call 1 takes the only slot and blocks inside the upstream.
	dispatchAsync(um, &wg, &first)
	up.waitEntered(t, 1)
	require.Equal(t, 1, lim.Stats().Running, "call 1 must hold the single slot")

	// Call 2 finds the cap saturated and parks in the one-deep FIFO queue.
	dispatchAsync(um, &wg, &second)
	require.Eventually(t, func() bool {
		return lim.Stats().Queued == 1
	}, 5*time.Second, 10*time.Millisecond, "call 2 must be queued, not running")
	assert.Equal(t, int64(1), up.inFlight.Load(), "the upstream must still see exactly one request")

	// Call 3 arrives at a full queue: shed IMMEDIATELY (SC-005 <100ms) with the
	// typed identity the shed seams key off (FR-010/FR-011).
	shedStart := time.Now()
	_, thirdErr := um.CallTool(context.Background(), slowUpstreamName+":slow_op", map[string]interface{}{})
	shedElapsed := time.Since(shedStart)
	require.Error(t, thirdErr, "call 3 must be shed, not admitted")
	require.True(t, errors.Is(thirdErr, limiter.ErrQueueFull), "call 3 must be a queue_full shed, got: %v", thirdErr)
	assert.Less(t, shedElapsed, 100*time.Millisecond, "a full-queue shed must not wait (SC-005)")

	var limitErr *limiter.LimitError
	require.True(t, errors.As(thirdErr, &limitErr))
	assert.Equal(t, limiter.ScopeServer, limitErr.Scope)
	assert.Equal(t, slowUpstreamName, limitErr.Server)
	assert.Contains(t, limitErr.UserMessage(), limiter.RetryAdvice)

	// FR-003: the sandboxed code-execution origin never reaches
	// handleCallToolVariant, yet the same limiter bounds it.
	caller := &upstreamToolCaller{
		upstreamManager: um,
		logger:          zap.NewNop(),
		executionID:     "exec-concurrency-e2e",
	}
	_, codeExecErr := caller.CallTool(context.Background(), slowUpstreamName, "slow_op", map[string]interface{}{})
	require.Error(t, codeExecErr, "code_execution must not bypass the limiter")
	assert.True(t, errors.Is(codeExecErr, limiter.ErrQueueFull),
		"code_execution must be shed by the same limiter, got: %v", codeExecErr)

	// Drain: releasing call 1 hands the slot to the queued call 2, which then
	// runs against the upstream and succeeds.
	up.releaseN(1)
	up.waitEntered(t, 1)
	up.releaseN(1)
	wg.Wait()

	require.NoError(t, first.err, "call 1 must succeed")
	require.NoError(t, second.err, "call 2 must queue and then succeed")
	assert.Equal(t, int64(1), up.maxInFlight.Load(),
		"the upstream must never observe more than max_concurrent_requests simultaneous calls (SC-001)")
	assert.Equal(t, 0, lim.Stats().Running, "every slot must be released")
	assert.Equal(t, 0, lim.Stats().Queued, "the queue must be empty")
}

// TestE2EConcurrencyLimits_QueueTimeoutSheds covers US2 scenario 2 / FR-004: a
// queued call that waits past the configured queue_timeout is shed with the
// timeout-flavoured typed error rather than hanging.
func TestE2EConcurrencyLimits_QueueTimeoutSheds(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	up := startSlowUpstream(t)
	um := newLimitedManager(t, up, 1, 1, 250*time.Millisecond)
	lim := um.Limiters().Server(slowUpstreamName)

	var wg sync.WaitGroup
	var first callResult

	dispatchAsync(um, &wg, &first)
	up.waitEntered(t, 1)

	// This call queues behind the blocked one and must be shed once its
	// absolute queue deadline passes — it must not wait for the upstream.
	queueStart := time.Now()
	_, queuedErr := um.CallTool(context.Background(), slowUpstreamName+":slow_op", map[string]interface{}{})
	queueElapsed := time.Since(queueStart)

	require.Error(t, queuedErr)
	require.True(t, errors.Is(queuedErr, limiter.ErrQueueTimeout),
		"a queued call past its deadline must be a queue_timeout shed, got: %v", queuedErr)
	assert.GreaterOrEqual(t, queueElapsed, 200*time.Millisecond, "the call must actually have waited in the queue")
	assert.Less(t, queueElapsed, 10*time.Second, "the call must not wait for the upstream to finish")

	var limitErr *limiter.LimitError
	require.True(t, errors.As(queuedErr, &limitErr))
	assert.Equal(t, limiter.ReasonQueueTimeout, limitErr.Reason)
	assert.Equal(t, 250*time.Millisecond, limitErr.RetryAfter, "Retry-After derives from the shedding scope's queue_timeout")

	up.releaseN(1)
	wg.Wait()
	require.NoError(t, first.err)
	assert.Equal(t, int64(1), up.maxInFlight.Load())
	assert.Equal(t, 0, lim.Stats().Queued)
}
