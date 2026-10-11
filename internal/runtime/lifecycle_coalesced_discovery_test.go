package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestCoalescedDiscovery_BurstPastFollowUpCap_VerifiesFinalTrigger (Sol r4
// P2): an upstream sends one tools/list_changed during every discovery run —
// more runs than the follow-up cap allows one owner — and arms its contract
// with the last notification, then goes quiet. The owner stops at the cap,
// but the final trigger must still be verified by a deferred continuation,
// without any further connect or notification.
func TestCoalescedDiscovery_BurstPastFollowUpCap_VerifiesFinalTrigger(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "burst", Enabled: true}})
	rt.coalescedDiscoveryContinuationDelay = 20 * time.Millisecond

	// One owner performs 1 + maxCoalescedDiscoveryFollowUps runs; notify
	// during every one of them and once more during the first continued run.
	notifyingRuns := int32(maxCoalescedDiscoveryFollowUps + 2)
	var runs atomic.Int32
	var armed, verifiedArmed atomic.Bool
	var run func(bool) error
	run = func(bool) error {
		n := runs.Add(1)
		if armed.Load() {
			verifiedArmed.Store(true)
		}
		if n <= notifyingRuns {
			if n == notifyingRuns {
				armed.Store(true) // the contract changes after this run listed it
			}
			rt.runCoalescedDiscovery("burst", run) // marker held → coalesced
		}
		return nil
	}

	rt.runCoalescedDiscovery("burst", run)
	require.Equal(t, int32(maxCoalescedDiscoveryFollowUps+1), runs.Load(), "one owner stops at the follow-up cap")
	assert.False(t, verifiedArmed.Load(), "the armed contract was not listed by the capped owner")

	require.Eventually(t, verifiedArmed.Load, 5*time.Second, 10*time.Millisecond,
		"a deferred continuation must verify the final coalesced trigger")
	require.Eventually(t, func() bool {
		_, busy := rt.discoveryInProgress.Load("burst")
		_, pending := rt.discoveryRerunPending.Load("burst")
		return !busy && !pending
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, notifyingRuns+1, runs.Load(), "the continuation runs exactly what is pending, then stops")
}

// TestCoalescedDiscovery_ContinuationStopsOnShutdown: a deferred continuation
// never runs after the runtime closed.
func TestCoalescedDiscovery_ContinuationStopsOnShutdown(t *testing.T) {
	// Not setupQuarantineRuntime: this test closes the runtime itself.
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{{Name: "burst", Enabled: true}}}, "", zap.NewNop())
	require.NoError(t, err)
	rt.coalescedDiscoveryContinuationDelay = 50 * time.Millisecond
	var runs atomic.Int32
	var run func(bool) error
	run = func(bool) error {
		runs.Add(1)
		rt.runCoalescedDiscovery("burst", run) // notify during every run
		return nil
	}
	rt.runCoalescedDiscovery("burst", run)
	require.Equal(t, int32(maxCoalescedDiscoveryFollowUps+1), runs.Load())
	require.NoError(t, rt.Close())
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(maxCoalescedDiscoveryFollowUps+1), runs.Load(), "no continuation after shutdown")
}

// TestToolsListChangedFollowUp_GetsFreshContext (Sol r4 P2): the notification
// context carries one 30s deadline. A follow-up for a notification coalesced
// while the first run consumed that deadline must run with a fresh,
// runtime-scoped context — not the expired one, which would turn it into a
// silent no-op after the pending flag was cleared.
func TestToolsListChangedFollowUp_GetsFreshContext(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "notify", Enabled: true}})
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	var mu sync.Mutex
	var ctxErrs []error
	var calls atomic.Int32
	rt.notificationDiscoverHook = func(ctx context.Context, _ string) error {
		mu.Lock()
		ctxErrs = append(ctxErrs, ctx.Err())
		mu.Unlock()
		if calls.Add(1) == 1 {
			// A second notification arrives while the first run is in flight.
			require.NoError(t, rt.onUpstreamToolsListChanged(expired, "notify"))
		}
		return ctx.Err()
	}

	_ = rt.onUpstreamToolsListChanged(expired, "notify")
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, ctxErrs, 2, "the coalesced notification gets one follow-up run")
	assert.Error(t, ctxErrs[0], "the first run uses the notification's own (expired) context")
	assert.NoError(t, ctxErrs[1], "the follow-up runs with a fresh context")
}

// TestToolsListChanged_CappedContinuation_NoDataRace (Sol r5 P2): a burst of
// tools/list_changed notifications reaches the follow-up cap through the real
// notification handler, so the remaining trigger runs in a deferred worker
// after the handler returned. That worker must not write the handler's state
// (run with -race).
func TestToolsListChanged_CappedContinuation_NoDataRace(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "burst", Enabled: true}})
	rt.coalescedDiscoveryContinuationDelay = 5 * time.Millisecond
	notifyingRuns := int32(maxCoalescedDiscoveryFollowUps + 2)
	var runs atomic.Int32
	rt.notificationDiscoverHook = func(ctx context.Context, server string) error {
		if runs.Add(1) <= notifyingRuns {
			_ = rt.onUpstreamToolsListChanged(ctx, server) // coalesced
		}
		return assert.AnError
	}

	err := rt.onUpstreamToolsListChanged(context.Background(), "burst")
	// No synchronising operation until the continuation ran: an atomic or
	// sync.Map access here would order the handler's read of its error
	// before the worker's write and hide the race from the detector.
	time.Sleep(200 * time.Millisecond)
	assert.ErrorIs(t, err, assert.AnError, "the handler reports its own run's error")
	require.Eventually(t, func() bool { return runs.Load() == notifyingRuns+1 }, 5*time.Second, 5*time.Millisecond,
		"the deferred continuation runs the pending trigger")
	require.Eventually(t, func() bool {
		_, busy := rt.discoveryInProgress.Load("burst")
		_, pending := rt.discoveryRerunPending.Load("burst")
		return !busy && !pending
	}, 5*time.Second, 5*time.Millisecond)
}

// TestCoalescedDiscovery_OwnerReachesCapAfterClose_NoContinuation (Sol r5
// P2): Close replaces the app context with context.Background(). An owner
// that is paused in a run while the runtime closes and reaches the cap only
// afterwards must not schedule a continuation that runs after closure.
func TestCoalescedDiscovery_OwnerReachesCapAfterClose_NoContinuation(t *testing.T) {
	// Not setupQuarantineRuntime: this test closes the runtime itself.
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{{Name: "burst", Enabled: true}}}, "", zap.NewNop())
	require.NoError(t, err)
	rt.coalescedDiscoveryContinuationDelay = 10 * time.Millisecond

	capRun := int32(maxCoalescedDiscoveryFollowUps + 1)
	paused := make(chan struct{})
	release := make(chan struct{})
	var runs atomic.Int32
	var run func(bool) error
	run = func(bool) error {
		n := runs.Add(1)
		rt.runCoalescedDiscovery("burst", run) // notify during every run
		if n == capRun {
			close(paused)
			<-release
		}
		return nil
	}
	ownerDone := make(chan struct{})
	go func() {
		defer close(ownerDone)
		rt.runCoalescedDiscovery("burst", run)
	}()

	<-paused
	require.NoError(t, rt.Close())
	close(release)
	<-ownerDone
	require.Equal(t, capRun, runs.Load())
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, capRun, runs.Load(), "no continuation runs after closure")
}

// TestToolsListChangedFollowUp_ContextCancelledAfterClose: a follow-up that
// starts after Close gets a cancelled context, not one derived from the
// replacement context.Background().
func TestToolsListChangedFollowUp_ContextCancelledAfterClose(t *testing.T) {
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{{Name: "notify", Enabled: true}}}, "", zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, rt.Close())
	assert.Error(t, rt.lifetimeContext().Err(), "the lifetime context stays cancelled after Close")
	assert.NoError(t, rt.AppContext().Err(), "precondition: Close replaced the app context")
}
