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

func newTestAttentionSubscriber(t *testing.T) (*Runtime, *attentionSubscriber) {
	t.Helper()
	rt := &Runtime{
		eventSubs:         make(map[chan Event]struct{}),
		internalEventSubs: make(map[chan Event]struct{}),
	}
	sub := newAttentionSubscriber(rt, 20*time.Millisecond)
	return rt, sub
}

// TestAttentionSubscriberRecomputesOnlyOnIDSetChange pins T054: an event
// triggers a debounced recompute, and attention.changed is only published
// when the set of ids actually changes.
func TestAttentionSubscriberRecomputesOnlyOnIDSetChange(t *testing.T) {
	rt, sub := newTestAttentionSubscriber(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(watcher)

	// Pre-seed StateSince as if this server had already been in "error" for
	// 2 minutes — the subscriber otherwise stamps StateSince at first
	// sighting (now), which is the documented restart-delay behaviour and
	// would make this test wait out the real 60s threshold.
	sub.state["flaky"] = attentionServerState{status: health.StatusError, since: time.Now().Add(-2 * time.Minute)}

	sub.start(ctx)

	server := errorServer("flaky", time.Now().Add(-2*time.Minute))
	rt.publishEvent(newEvent(EventTypeServersChanged, map[string]any{
		"servers": []contracts.Server{server},
	}))

	evt := waitForEvent(t, watcher, EventTypeAttentionChanged)
	items := sub.Items()
	require.Len(t, items, 1)
	assert.Equal(t, AttentionKindServerError, items[0].Kind)
	assert.NotNil(t, evt.Payload["items"])

	// Publishing the exact same server state again must not re-fire
	// attention.changed (the id set has not changed).
	rt.publishEvent(newEvent(EventTypeServersChanged, map[string]any{
		"servers": []contracts.Server{server},
	}))
	assertNoEvent(t, watcher, EventTypeAttentionChanged, 100*time.Millisecond)
}

// TestAttentionSubscriberAtomicSnapshot exercises Items() concurrently with
// recompute under -race.
func TestAttentionSubscriberAtomicSnapshot(t *testing.T) {
	rt, sub := newTestAttentionSubscriber(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub.start(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_ = sub.Items()
		}
	}()

	for i := 0; i < 5; i++ {
		rt.publishEvent(newEvent(EventTypeServersChanged, map[string]any{
			"servers": []contracts.Server{errorServer("s", time.Now().Add(-2*time.Minute))},
		}))
		time.Sleep(5 * time.Millisecond)
	}
	<-done
}

func errorServer(name string, since time.Time) contracts.Server {
	return contracts.Server{
		Name:      name,
		Enabled:   true,
		Connected: false,
		Updated:   since,
		Health: &contracts.HealthStatus{
			Status: health.StatusError,
		},
	}
}

func waitForEvent(t *testing.T, ch chan Event, want EventType) Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == want {
				return evt
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %s", want)
		}
	}
}

func assertNoEvent(t *testing.T, ch chan Event, unwanted EventType, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case evt := <-ch:
			if evt.Type == unwanted {
				t.Fatalf("unexpected event %s", unwanted)
			}
		case <-deadline:
			return
		}
	}
}
