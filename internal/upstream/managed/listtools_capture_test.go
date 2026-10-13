package managed

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 117 PR-3 (#1565, FR-030): every upstream tools/list carries a capture
// identity taken by its leader when it owns the list, before the upstream
// request. Captures order inventories: a list that started later carries a
// larger sequence.

// leadOnce runs one leader flight that publishes tools and returns the
// capture a coalesced waiter of that flight received.
func leadOnce(t *testing.T, mc *Client, tools []*config.ToolMetadata) InventoryCapture {
	t.Helper()
	events := observeListToolsEvents(t, mc)
	_, release, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)
	_, seq := currentListToolsSeq(t, mc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		tools   []*config.ToolMetadata
		capture InventoryCapture
		err     error
	}
	done := make(chan result, 1)
	go func() {
		got, c, err := mc.ListToolsCaptured(ctx)
		done <- result{got, c, err}
	}()
	awaitListToolsEvent(t, events, ListToolsEvent{Seq: seq})
	mc.publishListToolsResult(tools, nil)
	release()
	select {
	case got := <-done:
		require.NoError(t, got.err)
		assert.Equal(t, tools, got.tools)
		return got.capture
	case <-ctx.Done():
		t.Fatal("the waiter did not return")
	}
	return InventoryCapture{}
}

// The leader's capture is stamped with a process-global sequence that grows
// with every flight, the connection epoch at list start and the client.
func TestListToolsCaptured_LeaderSequenceIsMonotonic(t *testing.T) {
	mc := newTestReadyClient(t)
	other := newTestReadyClient(t)
	tools := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a"}}

	c1 := leadOnce(t, mc, tools)
	c2 := leadOnce(t, other, tools)
	c3 := leadOnce(t, mc, tools)

	require.NotZero(t, c1.Seq, "a real tools/list is never unordered")
	assert.Greater(t, c2.Seq, c1.Seq, "a list started later on another client carries a larger sequence")
	assert.Greater(t, c3.Seq, c2.Seq, "a list started later carries a larger sequence")
	assert.Same(t, mc, c1.Client)
	assert.Same(t, other, c2.Client)
	assert.Equal(t, mc.ConnectionEpoch(), c1.Epoch)
	assert.True(t, mc.CaptureIsCurrent(c3))
}

// A coalesced waiter reads the capture of the flight it joined, even when a
// new leader is elected between its wake-up and its read (design review r2
// #4): it never sees the next flight's sequence.
func TestListToolsCaptured_WaiterGetsItsOwnLeadersCaptureAfterReplacement(t *testing.T) {
	mc := newTestReadyClient(t)
	want := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a"}}
	events := observeListToolsEvents(t, mc)

	_, release1, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)
	call1, seq1 := currentListToolsSeq(t, mc)
	mc.listToolsMu.Lock()
	leaderCapture := call1.capture
	mc.listToolsMu.Unlock()
	require.NotZero(t, leaderCapture.Seq)

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
		capture InventoryCapture
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, c, err := mc.ListToolsCaptured(ctx)
		done <- result{c, err}
	}()
	awaitListToolsEvent(t, events, ListToolsEvent{Seq: seq1})

	mc.publishListToolsResult(want, nil)
	release1()
	<-woke
	_, release2, ok := mc.acquireListToolsContext(context.Background(), 5*time.Second)
	require.True(t, ok)
	defer release2()
	call2, _ := currentListToolsSeq(t, mc)
	close(proceed)

	got := <-done
	require.NoError(t, got.err)
	assert.Equal(t, leaderCapture, got.capture, "the waiter returns the capture of the flight it joined")
	mc.listToolsMu.Lock()
	assert.Greater(t, call2.capture.Seq, got.capture.Seq)
	mc.listToolsMu.Unlock()
}

// ListTools keeps its signature and result.
func TestListTools_UnchangedByCapture(t *testing.T) {
	mc := newTestReadyClient(t)
	shared := []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a"}}
	mc.listToolsInProgress = true
	mc.listToolsCur = &listToolsCall{done: make(chan struct{}), tools: shared, published: true}
	close(mc.listToolsCur.done)
	tools, err := mc.ListTools(context.Background())
	require.NoError(t, err)
	assert.Equal(t, shared, tools)
}

// FR-036: a retired client's captures are no longer current, and neither is
// a capture taken before the connection epoch moved.
func TestCaptureIsCurrent_RetireAndEpoch(t *testing.T) {
	mc := newTestReadyClient(t)
	other := newTestReadyClient(t)
	c := leadOnce(t, mc, []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a"}})

	assert.True(t, mc.CaptureIsCurrent(c))
	assert.False(t, other.CaptureIsCurrent(c), "a capture of another client is never current")
	assert.False(t, mc.CaptureIsCurrent(InventoryCapture{}), "an unordered capture names no client")

	mc.connectionEpoch.Add(1)
	assert.False(t, mc.CaptureIsCurrent(c), "a reconnect makes the earlier capture stale")
	c2 := leadOnce(t, mc, []*config.ToolMetadata{{ServerName: "test-server", Name: "tool_a"}})
	assert.True(t, mc.CaptureIsCurrent(c2))

	mc.Retire()
	assert.True(t, mc.Retired())
	assert.False(t, mc.CaptureIsCurrent(c2), "a retired client's capture is discarded")
	current, err := mc.WithConnectionEpoch(mc.ConnectionEpoch(), func() error { return nil })
	require.NoError(t, err)
	assert.False(t, current, "a retired client is not current on any epoch")
}
