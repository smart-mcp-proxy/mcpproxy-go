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

// observeListToolsEvents installs a buffered ListTools observer and returns
// its event channel.
func observeListToolsEvents(t *testing.T, mc *Client) <-chan ListToolsEvent {
	t.Helper()
	events := make(chan ListToolsEvent, 64)
	mc.SetListToolsObserver(func(ev ListToolsEvent) { events <- ev })
	t.Cleanup(func() { mc.SetListToolsObserver(nil) })
	return events
}

// awaitListToolsEvent waits for the observer to report want.
func awaitListToolsEvent(t *testing.T, events <-chan ListToolsEvent, want ListToolsEvent) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev == want {
				return
			}
		case <-timeout:
			t.Fatalf("ListTools event %+v never observed", want)
		}
	}
}

// currentListToolsSeq returns the seq of the in-flight call.
func currentListToolsSeq(t *testing.T, mc *Client) (*listToolsCall, uint64) {
	t.Helper()
	mc.listToolsMu.Lock()
	defer mc.listToolsMu.Unlock()
	require.NotNil(t, mc.listToolsCur)
	return mc.listToolsCur, mc.listToolsCur.seq
}

// TestListTools_WaiterReadsItsOwnLeadersResultAfterReplacement (Sol r6 P1,
// Sol r7 P2): a real ListTools waiter woken by leader 1 must read leader 1's
// result even when leader 2 is elected — with no result yet — between the
// waiter waking and reading. A waiter reading shared or current-call state
// would see leader 2's unpublished slot and never return (nil, nil) or block.
// The waiter is held at the read boundary, so the interleaving is forced.
func TestListTools_WaiterReadsItsOwnLeadersResultAfterReplacement(t *testing.T) {
	mc := newTestReadyClient(t)
	want := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a"}}
	events := observeListToolsEvents(t, mc)

	_, release1, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)
	_, seq1 := currentListToolsSeq(t, mc)

	woke := make(chan struct{})
	proceed := make(chan struct{})
	hook := func(seq uint64) {
		if seq == seq1 {
			close(woke)
			<-proceed
		}
	}
	mc.listToolsBeforeWaiterRead.Store(&hook)
	t.Cleanup(func() { mc.listToolsBeforeWaiterRead.Store(nil) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		tools []*config.ToolMetadata
		err   error
	}
	done := make(chan result, 1)
	go func() {
		tools, err := mc.ListTools(ctx)
		done <- result{tools, err}
	}()
	awaitListToolsEvent(t, events, ListToolsEvent{Seq: seq1})

	// Leader 1 publishes and releases; the waiter wakes and is held before
	// it reads. Leader 2 is elected in that window, with no result yet.
	mc.publishListToolsResult(want, nil)
	release1()
	select {
	case <-woke:
	case <-ctx.Done():
		t.Fatal("the waiter never woke on leader 1")
	}
	_, release2, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok, "leader 2 is elected while the waiter is held")
	defer release2()
	close(proceed)

	select {
	case got := <-done:
		require.NoError(t, got.err)
		assert.Equal(t, want, got.tools, "a waiter on call 1 reads call 1's result, not leader 2's empty slot")
	case <-ctx.Done():
		t.Fatal("the waiter did not return leader 1's result")
	}
}

// TestListTools_UnpublishedLeaderIsNotAnEmptySuccess: a leader that releases
// without publishing must not hand its waiters (nil, nil) — they list again
// and take the next leader's real result.
func TestListTools_UnpublishedLeaderIsNotAnEmptySuccess(t *testing.T) {
	mc := newTestReadyClient(t)
	want := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a"}}
	events := observeListToolsEvents(t, mc)

	_, release1, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)
	call1, seq1 := currentListToolsSeq(t, mc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan []*config.ToolMetadata, 1)
	errCh := make(chan error, 1)
	go func() {
		tools, err := mc.ListTools(ctx)
		errCh <- err
		done <- tools
	}()
	awaitListToolsEvent(t, events, ListToolsEvent{Seq: seq1})

	// Hand off atomically from unpublished leader 1 to leader 2, so the
	// waiter cannot become a leader itself.
	mc.listToolsMu.Lock()
	mc.listToolsLeaderSeq++
	call2 := &listToolsCall{done: make(chan struct{}), seq: mc.listToolsLeaderSeq}
	mc.listToolsCur = call2
	close(call1.done)
	mc.listToolsMu.Unlock()
	_ = release1 // leader 1 was handed off by hand above

	// The waiter re-attaches to leader 2 instead of returning: proven by
	// the observer, not by a sleep.
	awaitListToolsEvent(t, events, ListToolsEvent{Seq: call2.seq})
	select {
	case got := <-done:
		t.Fatalf("waiter returned for an unpublished leader: %v (err %v)", got, <-errCh)
	default:
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
// started after the call. Every step is acknowledged by the ListTools
// observer (Sol r7 P3): no sleeps.
func TestListToolsFresh_DoesNotJoinLeaderThatPredatesTheCall(t *testing.T) {
	mc := newTestReadyClient(t)
	stale := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a", Description: "pre-change"}}
	fresh := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a", Description: "post-change"}}
	events := observeListToolsEvents(t, mc)

	// Leader 1 is in flight before the verification is called.
	_, release1, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)
	mc.publishListToolsResult(stale, nil)
	call1, seq1 := currentListToolsSeq(t, mc)
	mc.listToolsMu.Lock()
	entrySeq := mc.listToolsLeaderSeq
	mc.listToolsMu.Unlock()

	// Hold the fresh waiter between waking on call 2 and reading it.
	var seq2 uint64
	woke := make(chan struct{})
	proceed := make(chan struct{})
	var seq2Set sync.WaitGroup
	seq2Set.Add(1)
	hook := func(seq uint64) {
		seq2Set.Wait()
		if seq == seq2 {
			close(woke)
			<-proceed
		}
	}
	mc.listToolsBeforeWaiterRead.Store(&hook)
	t.Cleanup(func() { mc.listToolsBeforeWaiterRead.Store(nil) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	freshDone := make(chan []*config.ToolMetadata, 1)
	go func() {
		tools, err := mc.listTools(ctx, true, entrySeq)
		assert.NoError(t, err)
		freshDone <- tools
	}()
	awaitListToolsEvent(t, events, ListToolsEvent{Seq: seq1, Fresh: true})

	// Hand off atomically from leader 1 to leader 2 (started after the call),
	// so the fresh caller cannot become a leader itself.
	mc.listToolsMu.Lock()
	mc.listToolsLeaderSeq++
	call2 := &listToolsCall{done: make(chan struct{}), seq: mc.listToolsLeaderSeq}
	seq2 = call2.seq
	mc.listToolsCur = call2
	close(call1.done)
	mc.listToolsMu.Unlock()
	seq2Set.Done()
	_ = release1 // leader 1 was handed off by hand above

	// The fresh caller rejected leader 1 and attached to leader 2.
	awaitListToolsEvent(t, events, ListToolsEvent{Seq: seq2, Fresh: true})
	select {
	case got := <-freshDone:
		t.Fatalf("fresh list returned the pre-call leader's result: %v", got)
	default:
	}

	mc.publishListToolsResult(fresh, nil)
	mc.listToolsMu.Lock()
	mc.listToolsCur = nil
	mc.listToolsInProgress = false
	close(call2.done)
	mc.listToolsMu.Unlock()

	// A third leader is elected after the waiter woke on call 2 and before
	// it reads (the Sol r6 P1 window): it must still get call 2's result.
	select {
	case <-woke:
	case <-ctx.Done():
		t.Fatal("the fresh waiter never woke on leader 2")
	}
	_, release3, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok, "leader 3 is elected while the fresh waiter is held")
	defer release3()
	close(proceed)

	select {
	case got := <-freshDone:
		assert.Equal(t, fresh, got, "the fresh list takes the result of the request started after the call it waited on")
	case <-ctx.Done():
		t.Fatal("fresh ListTools did not return")
	}
}
