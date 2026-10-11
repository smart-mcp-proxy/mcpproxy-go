package managed

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

func newTestReadyClient(t *testing.T) *Client {
	t.Helper()
	mc := &Client{
		logger: zap.NewNop(),
	}
	mc.SetConfig(&config.ServerConfig{Name: "test-server"})
	mc.StateManager = types.NewStateManager()
	mc.StateManager.TransitionTo(types.StateConnecting)
	mc.StateManager.TransitionTo(types.StateReady)
	return mc
}

// TestListTools_CoalescesWaiters verifies that when a ListTools operation is already
// in progress, additional callers wait for and receive the shared result instead of
// failing with an in-progress error.
func TestListTools_CoalescesWaiters(t *testing.T) {
	mc := newTestReadyClient(t)

	shared := []*config.ToolMetadata{
		{ServerName: "test-server", Name: "tool_a"},
		{ServerName: "test-server", Name: "tool_b"},
	}

	mc.listToolsInProgress = true
	mc.listToolsCur = &listToolsCall{done: make(chan struct{}), tools: shared, published: true}

	close(mc.listToolsCur.done)

	tools, err := mc.ListTools(context.Background())
	require.NoError(t, err)
	assert.Equal(t, shared, tools)
}

// TestListTools_CoalescesWaitersError verifies waiting callers get the same error
// returned by the in-flight ListTools operation.
func TestListTools_CoalescesWaitersError(t *testing.T) {
	mc := newTestReadyClient(t)

	mc.listToolsInProgress = true
	mc.listToolsCur = &listToolsCall{done: make(chan struct{}), err: assert.AnError, published: true}

	close(mc.listToolsCur.done)

	tools, err := mc.ListTools(context.Background())
	require.Error(t, err)
	assert.Nil(t, tools)
	assert.Contains(t, err.Error(), "ListTools failed")
}

// TestListTools_MultipleWaitersUnblockedByLeader simulates a real leader/waiter
// interaction without going through coreClient: the test acquires the
// in-progress flag (just like the health-check or tool-count paths do), spawns
// N goroutines that should park in the wait branch, and verifies that
// publishing a result + releasing wakes all of them with the same data.
func TestListTools_MultipleWaitersUnblockedByLeader(t *testing.T) {
	mc := newTestReadyClient(t)

	_, release, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)

	const waiters = 5
	results := make([][]*config.ToolMetadata, waiters)
	errs := make([]error, waiters)
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tools, err := mc.ListTools(ctx)
			results[idx] = tools
			errs[idx] = err
		}(i)
	}

	// Give waiters a beat to enter the wait branch.
	time.Sleep(50 * time.Millisecond)

	shared := []*config.ToolMetadata{
		{ServerName: "test-server", Name: "tool_x"},
		{ServerName: "test-server", Name: "tool_y"},
	}
	mc.publishListToolsResult(shared, nil)
	release()

	wg.Wait()

	for i := 0; i < waiters; i++ {
		require.NoErrorf(t, errs[i], "waiter %d", i)
		assert.Equalf(t, shared, results[i], "waiter %d", i)
	}
}

// TestListTools_HealthCheckLeaderDoesNotDeadlockWaiters is the regression test
// for the nil-channel bug: when a non-ListTools caller (here emulated as a
// health-check style leader using acquireListToolsContext) holds the
// in-progress flag, an arriving ListTools must not block on a nil channel.
// Previously acquireListToolsContext did not allocate a wait channel, so
// ListTools waiters hung until ctx.Done(). After the fix the channel is always
// allocated and waiters wake up promptly.
func TestListTools_HealthCheckLeaderDoesNotDeadlockWaiters(t *testing.T) {
	mc := newTestReadyClient(t)

	_, release, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)

	mc.listToolsMu.Lock()
	require.NotNil(t, mc.listToolsCur, "acquireListToolsContext must allocate a call to wait on")
	mc.listToolsMu.Unlock()

	done := make(chan struct{})
	var (
		gotTools []*config.ToolMetadata
		gotErr   error
	)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		gotTools, gotErr = mc.ListTools(ctx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	expected := []*config.ToolMetadata{{ServerName: "test-server", Name: "ping"}}
	mc.publishListToolsResult(expected, nil)
	release()

	select {
	case <-done:
		require.NoError(t, gotErr)
		assert.Equal(t, expected, gotTools)
	case <-time.After(3 * time.Second):
		t.Fatal("ListTools waiter did not wake up after leader released; coalescing is broken")
	}
}

// TestListTools_LeaderErrorPropagatedToWaiters verifies an error from the
// upstream ListTools call performed by the leader is forwarded to all waiters.
func TestListTools_LeaderErrorPropagatedToWaiters(t *testing.T) {
	mc := newTestReadyClient(t)

	_, release, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)

	const waiters = 3
	wErrs := make([]error, waiters)
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, wErrs[idx] = mc.ListTools(ctx)
		}(i)
	}

	time.Sleep(50 * time.Millisecond)

	leaderErr := errors.New("upstream boom")
	mc.publishListToolsResult(nil, leaderErr)
	release()

	wg.Wait()

	for i := 0; i < waiters; i++ {
		require.Errorf(t, wErrs[i], "waiter %d", i)
		assert.Containsf(t, wErrs[i].Error(), "ListTools failed", "waiter %d", i)
		assert.Containsf(t, wErrs[i].Error(), "upstream boom", "waiter %d", i)
	}
}

// TestListTools_AcquireContextResetsCachedResult ensures results from a
// previous call are never visible through a new leader's call, so any waiter
// that arrives can never observe a result from an earlier round.
func TestListTools_AcquireContextResetsCachedResult(t *testing.T) {
	mc := newTestReadyClient(t)

	_, release1, ok := mc.acquireListToolsContext(context.Background(), time.Second)
	require.True(t, ok)
	mc.publishListToolsResult([]*config.ToolMetadata{{ServerName: "test-server", Name: "stale"}}, errors.New("stale error"))
	release1()

	_, release, ok := mc.acquireListToolsContext(context.Background(), time.Second)
	require.True(t, ok)
	defer release()

	mc.listToolsMu.Lock()
	defer mc.listToolsMu.Unlock()
	require.NotNil(t, mc.listToolsCur)
	assert.Nil(t, mc.listToolsCur.tools, "leader acquisition must not carry stale results")
	assert.Nil(t, mc.listToolsCur.err, "leader acquisition must not carry a stale error")
	assert.False(t, mc.listToolsCur.published)
}

// TestListTools_WaiterReadsItsOwnLeadersResultAfterReplacement (Sol r6 P1):
// a waiter woken by leader 1 must read leader 1's result even if leader 2 has
// already been elected (which used to clear the shared last-result) before the
// waiter got to read it. It must never get (nil, nil).
func TestListTools_WaiterReadsItsOwnLeadersResultAfterReplacement(t *testing.T) {
	mc := newTestReadyClient(t)
	want := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a"}}

	_, release1, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)
	mc.listToolsMu.Lock()
	call1 := mc.listToolsCur
	mc.listToolsMu.Unlock()

	// Leader 1 publishes and releases; leader 2 is elected immediately (no
	// result yet) — the window in which the old shared fields were cleared.
	mc.publishListToolsResult(want, nil)
	release1()
	_, release2, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)
	defer release2()

	mc.listToolsMu.Lock()
	tools, err, published := call1.tools, call1.err, call1.published
	mc.listToolsMu.Unlock()
	require.True(t, published)
	require.NoError(t, err)
	assert.Equal(t, want, tools, "a waiter on call 1 reads call 1's result, not leader 2's empty slot")
}

// TestListTools_UnpublishedLeaderIsNotAnEmptySuccess: a leader that releases
// without publishing must not hand its waiters (nil, nil) — they list again
// and take the next leader's real result.
func TestListTools_UnpublishedLeaderIsNotAnEmptySuccess(t *testing.T) {
	mc := newTestReadyClient(t)
	want := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a"}}

	_, release1, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)
	mc.listToolsMu.Lock()
	call1 := mc.listToolsCur
	mc.listToolsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan []*config.ToolMetadata, 1)
	errCh := make(chan error, 1)
	go func() {
		tools, err := mc.ListTools(ctx)
		errCh <- err
		done <- tools
	}()
	time.Sleep(50 * time.Millisecond)

	// Hand off atomically from unpublished leader 1 to leader 2, so the
	// waiter cannot become a leader itself.
	mc.listToolsMu.Lock()
	mc.listToolsLeaderSeq++
	call2 := &listToolsCall{done: make(chan struct{}), seq: mc.listToolsLeaderSeq}
	mc.listToolsCur = call2
	close(call1.done)
	mc.listToolsMu.Unlock()
	_ = release1 // leader 1 was handed off by hand above

	select {
	case got := <-done:
		t.Fatalf("waiter returned for an unpublished leader: %v (err %v)", got, <-errCh)
	case <-time.After(50 * time.Millisecond):
	}

	mc.publishListToolsResult(want, nil)
	mc.listToolsMu.Lock()
	mc.listToolsCur = nil
	mc.listToolsInProgress = false
	close(call2.done)
	mc.listToolsMu.Unlock()

	select {
	case err := <-errCh:
		require.NoError(t, err)
		assert.Equal(t, want, <-done)
	case <-ctx.Done():
		t.Fatal("waiter did not return")
	}
}

// TestListToolsFresh_DoesNotJoinLeaderThatPredatesTheCall (Sol r5 P2): a
// tools/list_changed verification must not join a tools/list that was already
// in flight when it was called — that request may have been answered before
// the change. It waits for that request and accepts only a request that
// started after the call.
func TestListToolsFresh_DoesNotJoinLeaderThatPredatesTheCall(t *testing.T) {
	mc := newTestReadyClient(t)
	stale := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a", Description: "pre-change"}}
	fresh := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a", Description: "post-change"}}

	// Leader 1 is in flight before the verification is called.
	_, release1, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)
	mc.publishListToolsResult(stale, nil)
	mc.listToolsMu.Lock()
	entrySeq := mc.listToolsLeaderSeq
	call1 := mc.listToolsCur
	mc.listToolsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	freshDone := make(chan []*config.ToolMetadata, 1)
	go func() {
		tools, err := mc.listTools(ctx, true, entrySeq)
		assert.NoError(t, err)
		freshDone <- tools
	}()
	time.Sleep(50 * time.Millisecond)

	// Hand off atomically from leader 1 to leader 2 (started after the call),
	// so the fresh caller cannot become a leader itself.
	mc.listToolsMu.Lock()
	mc.listToolsLeaderSeq++
	call2 := &listToolsCall{done: make(chan struct{}), seq: mc.listToolsLeaderSeq}
	mc.listToolsCur = call2
	close(call1.done)
	mc.listToolsMu.Unlock()

	select {
	case got := <-freshDone:
		t.Fatalf("fresh list returned the pre-call leader's result: %v", got)
	case <-time.After(50 * time.Millisecond):
	}

	mc.publishListToolsResult(fresh, nil)
	// A third leader is elected before the fresh waiter reads (the Sol r6 P1
	// window): the waiter must still get call 2's result.
	mc.listToolsMu.Lock()
	mc.listToolsLeaderSeq++
	mc.listToolsCur = &listToolsCall{done: make(chan struct{}), seq: mc.listToolsLeaderSeq}
	close(call2.done)
	mc.listToolsMu.Unlock()
	_ = release1 // leader 1 was handed off by hand above

	select {
	case got := <-freshDone:
		assert.Equal(t, fresh, got, "the fresh list takes the result of the request started after the call it waited on")
	case <-ctx.Done():
		t.Fatal("fresh ListTools did not return")
	}
}
