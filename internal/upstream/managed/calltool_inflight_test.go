package managed

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockingToolCaller blocks CallTool until release is closed, so tests can
// observe the managed client's state while a call is genuinely in flight.
type blockingToolCaller struct {
	release  chan struct{}
	started  chan struct{}
	startOne chan struct{} // sends a token each time CallTool is entered
}

func newBlockingToolCaller() *blockingToolCaller {
	return &blockingToolCaller{
		release:  make(chan struct{}),
		started:  make(chan struct{}, 8),
		startOne: make(chan struct{}, 8),
	}
}

func (b *blockingToolCaller) CallTool(ctx context.Context, _ string, _ map[string]interface{}) (*mcp.CallToolResult, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
		return &mcp.CallToolResult{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestCallTool_InFlightCounterTracksRealCall is the end-to-end wiring proof
// for the #1317 fix: mc.hasInFlightToolCall() must reflect a REAL call
// dispatched through the public CallTool() entry point (not just a
// hand-set counter), for the whole duration it is blocked on the transport,
// and drop back to false the moment it returns.
func TestCallTool_InFlightCounterTracksRealCall(t *testing.T) {
	mc := newTestClientForHealth(t)
	fake := newBlockingToolCaller()
	mc.toolInvoker = fake

	assert.False(t, mc.hasInFlightToolCall(), "no call has started yet")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = mc.CallTool(context.Background(), "navigate_page", map[string]interface{}{"url": "https://example.com"})
	}()

	select {
	case <-fake.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the fake tool call to start")
	}

	require.Eventually(t, mc.hasInFlightToolCall, time.Second, 5*time.Millisecond,
		"the in-flight counter must be set while the call is blocked on the transport")

	close(fake.release) // let the call return, as if consent were finally granted

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CallTool to return")
	}

	require.Eventually(t, func() bool { return !mc.hasInFlightToolCall() }, time.Second, 5*time.Millisecond,
		"the in-flight counter must clear once the call returns")
}
