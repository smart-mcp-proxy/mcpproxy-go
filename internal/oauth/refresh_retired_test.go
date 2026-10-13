package oauth

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// Review round 1 (Spec 116 PR4): a healthy caller that joined a flight run by
// a retired instance gets a flight of its own, even when the retired instance
// starts a new (also refused) flight before the healthy caller retries. It
// never returns the retired instance's ErrRetired.
func TestRefreshCoordinator_RetiredFlightWaiterRunsOwnFlight(t *testing.T) {
	f := newCoordFixture(t, "dcr-client")
	c := NewRefreshCoordinator()

	var retiredCalls atomic.Int32
	first := make(chan struct{})
	releaseFirst := make(chan struct{})
	second := make(chan struct{})
	releaseSecond := make(chan struct{})
	retiredRefresh := func(_ context.Context, _ *storage.OAuthTokenRecord) (*client.Token, error) {
		switch retiredCalls.Add(1) {
		case 1:
			close(first)
			<-releaseFirst
		case 2:
			close(second)
			<-releaseSecond
		}
		return nil, fmt.Errorf("token request: %w", dialgate.ErrRetired)
	}
	retiredReq := f.req(RefreshTriggerReactive, "rt-0")
	retiredReq.Refresh = retiredRefresh
	healthyReq := f.req(RefreshTriggerReactive, "rt-0")
	healthyReq.ServerName = "srv-healthy"

	// The healthy caller retries only after the retired instance's second
	// flight is running, so its retry joins that flight.
	prev := refreshRetiredRetryHook
	var hookOnce atomic.Bool
	refreshRetiredRetryHook = func(req RefreshRequest) {
		if req.ServerName != "srv-healthy" || !hookOnce.CompareAndSwap(false, true) {
			return
		}
		<-second
	}
	t.Cleanup(func() { refreshRetiredRetryHook = prev })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	retiredDone := make(chan error, 1)
	go func() {
		_, _, err := c.Do(ctx, retiredReq)
		retiredDone <- err
	}()
	<-first
	type result struct {
		tok *client.Token
		err error
	}
	healthyDone := make(chan result, 1)
	go func() {
		tok, _, err := c.Do(ctx, healthyReq)
		healthyDone <- result{tok, err}
	}()
	require.Eventually(t, func() bool { return c.waiters(f.key) == 2 }, 5*time.Second, time.Millisecond,
		"the healthy caller joined the retired flight")
	close(releaseFirst)
	// The initiator led its flight: it is refused and does not retry.
	require.ErrorIs(t, <-retiredDone, dialgate.ErrRetired, "the retired caller is refused")

	// Another call on the retired instance starts a new (also refused)
	// flight before the healthy caller retries, so the retry joins it.
	retired2Done := make(chan error, 1)
	go func() {
		_, _, err := c.Do(ctx, retiredReq)
		retired2Done <- err
	}()
	<-second
	require.Eventually(t, func() bool { return c.waiters(f.key) == 2 }, 5*time.Second, time.Millisecond,
		"the healthy caller joined the second retired flight")
	close(releaseSecond)
	require.ErrorIs(t, <-retired2Done, dialgate.ErrRetired)

	res := <-healthyDone
	require.NoError(t, res.err, "the healthy caller ran its own flight")
	require.NotNil(t, res.tok)
	assert.Equal(t, int32(1), f.calls.Load(), "exactly one real token request, on the healthy transport")
}
