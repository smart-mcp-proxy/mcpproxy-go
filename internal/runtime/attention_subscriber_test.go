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

// TestAttentionSubscriberPrunesStateOnServerRemoval pins the F1 review
// finding: attentionServerState (keyed by server name) must be pruned when a
// server disappears from a servers.changed payload. Without pruning, a
// re-added server with the same name inherits the deleted server's stale
// StateSince, so a fast-failing respawn appears to have been in "error"
// since the old server's time — bypassing FR-002's 60s threshold and
// reporting a wrong Since.
func TestAttentionSubscriberPrunesStateOnServerRemoval(t *testing.T) {
	rt, sub := newTestAttentionSubscriber(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// "flaky" has already been in status:error since long ago (as if it had
	// been sitting in the attention list since Monday) — pre-seeded the same
	// way TestAttentionSubscriberRecomputesOnlyOnIDSetChange does, since
	// buildAttentionServer only stamps StateSince at first sighting.
	longAgo := time.Now().Add(-2 * time.Hour)
	sub.state["flaky"] = attentionServerState{status: health.StatusError, since: longAgo}

	sub.start(ctx)

	watcher := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(watcher)

	rt.publishEvent(newEvent(EventTypeServersChanged, map[string]any{
		"servers": []contracts.Server{errorServer("flaky", longAgo)},
	}))
	waitForEvent(t, watcher, EventTypeAttentionChanged)

	// "flaky" is removed (servers.changed with an empty list — deletion).
	rt.publishEvent(newEvent(EventTypeServersChanged, map[string]any{
		"servers": []contracts.Server{},
	}))
	waitForEvent(t, watcher, EventTypeAttentionChanged)

	sub.mu.Lock()
	_, stillTracked := sub.state["flaky"]
	sub.mu.Unlock()
	assert.False(t, stillTracked, "removed server's state must be pruned, not kept stale")

	// A new server named "flaky" is re-added, freshly failing (error status
	// observed for the first time just now).
	fresh := errorServer("flaky", time.Now())
	rt.publishEvent(newEvent(EventTypeServersChanged, map[string]any{
		"servers": []contracts.Server{fresh},
	}))

	// It must NOT immediately appear as a server_error item — StateSince
	// should be stamped at this re-sighting, not inherited from the deleted
	// server's 2-hour-old timestamp.
	assertNoEvent(t, watcher, EventTypeAttentionChanged, 100*time.Millisecond)
	items := sub.Items()
	assert.Empty(t, items, "freshly re-added server must wait out the threshold, not reuse stale StateSince")
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

// TestNewAttentionSubscriberHonorsNeverSeenAfterEnvHook pins T060/review
// finding F6: quickstart.md §3's live-verification recipe (referenced by
// T066) instructs setting MCPPROXY_ATTENTION_NEVER_SEEN_AFTER=5s to exercise
// the client_never_seen 5-minute threshold quickly, but nothing read the
// variable — the recipe had no effect. newAttentionSubscriber must apply it
// when set and parseable, and fall back to the package default otherwise.
func TestNewAttentionSubscriberHonorsNeverSeenAfterEnvHook(t *testing.T) {
	rt := &Runtime{eventSubs: make(map[chan Event]struct{}), internalEventSubs: make(map[chan Event]struct{})}

	t.Run("unset keeps the production default", func(t *testing.T) {
		sub := newAttentionSubscriber(rt, time.Millisecond)
		assert.Equal(t, AttentionClientNeverSeenThreshold, sub.clientNeverSeenThreshold)
	})

	t.Run("valid duration overrides the threshold", func(t *testing.T) {
		t.Setenv("MCPPROXY_ATTENTION_NEVER_SEEN_AFTER", "5s")
		sub := newAttentionSubscriber(rt, time.Millisecond)
		assert.Equal(t, 5*time.Second, sub.clientNeverSeenThreshold)
	})

	t.Run("unparseable value is ignored, keeping the default", func(t *testing.T) {
		t.Setenv("MCPPROXY_ATTENTION_NEVER_SEEN_AFTER", "not-a-duration")
		sub := newAttentionSubscriber(rt, time.Millisecond)
		assert.Equal(t, AttentionClientNeverSeenThreshold, sub.clientNeverSeenThreshold)
	})
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
