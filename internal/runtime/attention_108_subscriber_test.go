package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// attn108Source is a mutable fake of the clients-service warning source.
type attn108Source struct {
	mu       sync.Mutex
	warnings []AttentionClientWarning
	expiries []time.Time
	calls    int
}

func (s *attn108Source) set(w []AttentionClientWarning, e []time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnings, s.expiries = w, e
}

func (s *attn108Source) read() ([]AttentionClientWarning, []time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return append([]AttentionClientWarning(nil), s.warnings...), append([]time.Time(nil), s.expiries...)
}

func newAttn108Runtime() *Runtime {
	return &Runtime{
		eventSubs:         make(map[chan Event]struct{}),
		internalEventSubs: make(map[chan Event]struct{}),
	}
}

func hasAttentionID(sub *attentionSubscriber, id string) bool {
	for _, it := range sub.Items() {
		if it.ID == id {
			return true
		}
	}
	return false
}

// waitForStartupRecompute blocks until the loop's startup recompute (armed by
// loop() on a thresholdTimer) has read the clients source at least once, so a
// later recompute can only have been caused by what the test does next.
func (s *attn108Source) waitForStartupRecompute(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.calls > 0
	}, 2*time.Second, time.Millisecond, "startup recompute never read the clients source")
}

// TestAttention108EventsTriggerRecompute fails if an event type is dropped
// from attentionTriggers: the startup recompute is awaited first (it would
// otherwise emit attention.changed on its own), timerCap is an hour so no
// periodic tick can fire, and the warning only appears after the startup read,
// so the event is the sole thing that can make the item show up.
func TestAttention108EventsTriggerRecompute(t *testing.T) {
	for _, evt := range []EventType{EventTypeClientBindingChanged, EventTypeProfilesChanged, EventTypeConfigReloaded, EventTypeConfigSaved} {
		t.Run(string(evt), func(t *testing.T) {
			rt := newAttn108Runtime()
			src := &attn108Source{}
			sub := newAttentionSubscriber(rt, 5*time.Millisecond)
			sub.clientSource = src.read
			sub.timerCap = time.Hour // only the event may drive this recompute

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			watcher := rt.SubscribeEvents()
			defer rt.UnsubscribeEvents(watcher)
			sub.start(ctx)

			src.waitForStartupRecompute(t)
			// Let the startup recompute finish publishing before the warning
			// exists, then drop anything it emitted.
			time.Sleep(50 * time.Millisecond)
			for drained := false; !drained; {
				select {
				case <-watcher:
				default:
					drained = true
				}
			}

			src.set([]AttentionClientWarning{{Code: string(profile.WarningClientHoldsAdminKey), ClientID: "cursor", DisplayName: "Cursor"}}, nil)
			rt.publishEvent(newEvent(evt, nil))
			waitForEvent(t, watcher, EventTypeAttentionChanged)
			assert.True(t, hasAttentionID(sub, "client_holds_admin_key:client:cursor"))
		})
	}
}

func TestAttention108PeriodicTickPicksUpChangeWithoutEvent(t *testing.T) {
	rt := newAttn108Runtime()
	src := &attn108Source{}
	sub := newAttentionSubscriber(rt, 5*time.Millisecond)
	sub.clientSource = src.read
	sub.timerCap = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(watcher)
	sub.start(ctx)
	// The loop arms its own startup recompute (thresholdTimer), which re-arms
	// the periodic tick; no event is needed. Wait for it, then drain.
	src.waitForStartupRecompute(t)
	time.Sleep(60 * time.Millisecond)
	for drained := false; !drained; {
		select {
		case <-watcher:
		default:
			drained = true
		}
	}

	// A rotation started directly in storage: no event, only the periodic tick.
	src.set([]AttentionClientWarning{{Code: string(profile.WarningClientRotationPending), ClientID: "codex", DisplayName: "Codex"}}, nil)
	waitForEvent(t, watcher, EventTypeAttentionChanged)
	assert.True(t, hasAttentionID(sub, "client_rotation_pending:client:codex"))
}

func TestAttention108ExpiryThresholdAppearsWithoutEvent(t *testing.T) {
	rt := newAttn108Runtime()
	src := &attn108Source{}
	sub := newAttentionSubscriber(rt, 5*time.Millisecond)
	sub.clientSource = src.read
	sub.timerCap = time.Hour

	clock := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	sub.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }

	// The credential enters its 14-day window 40 ms from now (fake clock offset
	// is applied to the real timer through the computed threshold only).
	exp := clock.Add(clientCredentialExpiringWindow + 40*time.Millisecond)
	src.set(nil, []time.Time{exp})

	next := sub.recompute()
	require.Greater(t, next, time.Duration(0))
	assert.LessOrEqual(t, next, 40*time.Millisecond)

	// Past the threshold the service reports the warning.
	mu.Lock()
	clock = clock.Add(time.Second)
	mu.Unlock()
	src.set([]AttentionClientWarning{{Code: string(profile.WarningClientCredentialExpiring), ClientID: "codex", DisplayName: "Codex", ExpiresAt: &exp}}, nil)
	sub.recompute()
	require.True(t, hasAttentionID(sub, "client_credential_expiring:client:codex"))
	assert.Equal(t, exp.Add(-clientCredentialExpiringWindow), sub.Items()[0].Since)
}

func TestAttention108FirstSeenStampedAndPruned(t *testing.T) {
	rt := newAttn108Runtime()
	src := &attn108Source{}
	sub := newAttentionSubscriber(rt, 5*time.Millisecond)
	sub.clientSource = src.read

	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	clock := t0
	sub.now = func() time.Time { return clock }

	w := AttentionClientWarning{Code: string(profile.WarningClientHoldsAdminKey), ClientID: "cursor", DisplayName: "Cursor"}
	src.set([]AttentionClientWarning{w}, nil)
	sub.recompute()
	require.Len(t, sub.Items(), 1)
	assert.Equal(t, t0, sub.Items()[0].Since)

	clock = t0.Add(10 * time.Minute)
	sub.recompute()
	assert.Equal(t, t0, sub.Items()[0].Since, "since is the first time the id was seen, not the latest recompute")

	src.set(nil, nil)
	sub.recompute()
	assert.Empty(t, sub.Items())
	sub.mu.Lock()
	assert.Empty(t, sub.firstSeen, "firstSeen pruned when the warning clears")
	sub.mu.Unlock()

	clock = t0.Add(20 * time.Minute)
	src.set([]AttentionClientWarning{w}, nil)
	sub.recompute()
	assert.Equal(t, clock, sub.Items()[0].Since, "a re-appearing warning is dated afresh")
}

func TestAttention108NoClientSourceMeansNoItemsAndNoPeriodicTimer(t *testing.T) {
	rt := newAttn108Runtime()
	sub := newAttentionSubscriber(rt, 5*time.Millisecond)
	require.Nil(t, sub.clientSource, "no clients service (server edition shape)")
	assert.Equal(t, time.Duration(0), sub.recompute())
	assert.Empty(t, sub.Items())
}

// TestAttention108QuietBootSurfacesWarningWithoutAnyEvent pins F1.1: a core
// that boots with a client warning already present and then sees no event at
// all (no servers, no presence change) must still surface it. The subscriber
// starts before the management service is wired, so primeFromManagement
// returns early; start() itself has to schedule the first recompute.
func TestAttention108QuietBootSurfacesWarningWithoutAnyEvent(t *testing.T) {
	rt := newAttn108Runtime()
	src := &attn108Source{}
	src.set([]AttentionClientWarning{{Code: string(profile.WarningClientRotationPending), ClientID: "codex", DisplayName: "Codex"}}, nil)
	sub := newAttentionSubscriber(rt, 5*time.Millisecond)
	sub.clientSource = src.read
	sub.timerCap = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(watcher)
	sub.start(ctx) // no event is ever published

	waitForEvent(t, watcher, EventTypeAttentionChanged)
	assert.True(t, hasAttentionID(sub, "client_rotation_pending:client:codex"))
}
