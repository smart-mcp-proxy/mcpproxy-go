package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// TestAttentionThresholdTimerFiresWithoutFollowUpEvent pins FR-002: a
// connecting server becomes a server_error item at StateSince + threshold
// even when no second event ever fires — the subscriber's own armed timer
// must drive the recompute. Thresholds/cap are shrunk (not the package
// consts) so the test proves the arm-and-fire mechanism deterministically in
// milliseconds instead of waiting real minutes; the mechanism itself is the
// same code path production uses at 60s/5m/30s.
func TestAttentionThresholdTimerFiresWithoutFollowUpEvent(t *testing.T) {
	rt := &Runtime{
		eventSubs:         make(map[chan Event]struct{}),
		internalEventSubs: make(map[chan Event]struct{}),
	}
	sub := newAttentionSubscriber(rt, 5*time.Millisecond)
	sub.serverErrorThreshold = 80 * time.Millisecond
	sub.clientNeverSeenThreshold = 120 * time.Millisecond
	sub.timerCap = 20 * time.Millisecond // well under the threshold: must re-arm, never skip past it

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(watcher)

	sub.start(ctx)

	// A single servers.changed event puts "flaky" into "connecting". No
	// event follows it — everything from here must come from the timer.
	rt.publishEvent(newEvent(EventTypeServersChanged, map[string]any{
		"servers": []contracts.Server{{
			Name:    "flaky",
			Enabled: true,
			Health:  &contracts.HealthStatus{Status: health.StatusConnecting},
		}},
	}))

	// Immediately after, the server has not been connecting long enough.
	time.Sleep(15 * time.Millisecond)
	for _, it := range sub.Items() {
		assert.NotEqual(t, "server_error:server:flaky", it.ID, "must not be an item before the threshold")
	}

	// Wait past the threshold with no further events. The armed timer
	// (capped at 20ms, well under the 80ms threshold) must fire, recompute,
	// and cross the threshold on its own.
	evt := waitForEvent(t, watcher, EventTypeAttentionChanged)
	items, ok := evt.Payload["items"].([]AttentionEventItem)
	require.True(t, ok)
	found := false
	for _, it := range items {
		if it.ID == "server_error:server:flaky" {
			found = true
		}
	}
	assert.True(t, found, "attention.changed must carry the newly-crossed server_error item")

	found = false
	for _, it := range sub.Items() {
		if it.ID == "server_error:server:flaky" {
			found = true
		}
	}
	assert.True(t, found, "GET /attention (Items()) must reflect the threshold crossing")
}
