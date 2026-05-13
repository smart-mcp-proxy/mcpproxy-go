package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 047 — Phase 5 (US3): coalesce servers.changed bursts to ≤ 1 publish per
// interval window, last-write-wins.

func newCoalescerTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	rt := &Runtime{
		logger:            logger,
		eventSubs:         make(map[chan Event]struct{}),
		managementService: &fakeServersLister{servers: []*contracts.Server{{Name: "s1"}}, stats: &contracts.ServerStats{}},
	}
	rt.coalescer = newServersChangedCoalescer(rt, 50*time.Millisecond)
	return rt
}

func collectEvents(ch <-chan Event, window time.Duration) []Event {
	deadline := time.After(window)
	var out []Event
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return out
			}
			if evt.Type == EventTypeServersChanged {
				out = append(out, evt)
			}
		case <-deadline:
			return out
		}
	}
}

func TestCoalescer_CollapsesBurstToSingleEvent(t *testing.T) {
	rt := newCoalescerTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.coalescer.start(ctx)

	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	// Fire 100 rapid updates within 10 ms.
	for i := 0; i < 100; i++ {
		rt.emitServersChanged("burst", map[string]any{"i": i})
	}

	// Wait > 1 interval window for the drainer to publish.
	events := collectEvents(ch, 200*time.Millisecond)
	assert.LessOrEqual(t, len(events), 1, "expected ≤ 1 published event after burst, got %d", len(events))
	if len(events) == 1 {
		// Last submitted i was 99.
		assert.Equal(t, 99, events[0].Payload["i"], "last-write-wins: expected i=99")
	}
}

func TestCoalescer_LastWriteWins(t *testing.T) {
	rt := newCoalescerTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.coalescer.start(ctx)

	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	rt.emitServersChanged("first", nil)
	rt.emitServersChanged("middle", nil)
	rt.emitServersChanged("last", nil)

	events := collectEvents(ch, 200*time.Millisecond)
	require.Len(t, events, 1, "expected exactly 1 coalesced event")
	assert.Equal(t, "last", events[0].Payload["reason"])
}

func TestCoalescer_FlushesOnShutdown(t *testing.T) {
	rt := newCoalescerTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	rt.coalescer.start(ctx)

	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	// Submit one event, immediately cancel — drainer must flush before exiting.
	rt.emitServersChanged("right-before-shutdown", nil)
	cancel()

	events := collectEvents(ch, 500*time.Millisecond)
	require.GreaterOrEqual(t, len(events), 1, "expected at least 1 event flushed on shutdown")
	assert.Equal(t, "right-before-shutdown", events[0].Payload["reason"])
}

func TestCoalescer_NoStarvationOnSingleEvent(t *testing.T) {
	rt := newCoalescerTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.coalescer.start(ctx)

	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	rt.emitServersChanged("solo", nil)

	events := collectEvents(ch, 250*time.Millisecond)
	require.Len(t, events, 1, "single event must publish within ~1 interval window")
	assert.Equal(t, "solo", events[0].Payload["reason"])
}

func TestCoalescer_FlushNowSynchronousHook(t *testing.T) {
	// flushNow lets tests force a publish without sleeping. Useful for the
	// deterministic harness below: we submit, flushNow, assert immediately.
	rt := newCoalescerTestRuntime(t)

	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	rt.emitServersChanged("instant", nil)
	rt.coalescer.flushNow()

	select {
	case evt := <-ch:
		assert.Equal(t, "instant", evt.Payload["reason"])
	case <-time.After(100 * time.Millisecond):
		t.Fatal("flushNow should have caused immediate publish")
	}
}

// TestCoalescer_AmortisesBuildAcrossBurst is the regression test for the
// lazy-build refactor: K rapid emitServersChanged calls must result in
// exactly one ListServers call (and therefore at most one Quarantine-stats
// BBolt scan per server), not K. Before the refactor, the build ran eagerly
// inside emitServersChanged so a 100-emit burst paid 100×(1+N) BBolt ops
// even though the coalescer dropped 99 publishes.
func TestCoalescer_AmortisesBuildAcrossBurst(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	lister := &fakeServersLister{
		servers: []*contracts.Server{{Name: "s1"}, {Name: "s2"}},
		stats:   &contracts.ServerStats{},
	}
	rt := &Runtime{
		logger:            logger,
		eventSubs:         make(map[chan Event]struct{}),
		managementService: lister,
	}
	rt.coalescer = newServersChangedCoalescer(rt, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.coalescer.start(ctx)

	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	for i := 0; i < 100; i++ {
		rt.emitServersChanged("burst", map[string]any{"i": i})
	}

	events := collectEvents(ch, 200*time.Millisecond)
	assert.LessOrEqual(t, len(events), 1, "coalescer must collapse the burst to ≤ 1 publish")
	assert.EqualValues(t, 1, lister.calls.Load(),
		"lazy build: 100 rapid emits must result in exactly 1 ListServers call (got %d)",
		lister.calls.Load())
}

func TestCoalescer_ConcurrentSubmittersAreSafe(t *testing.T) {
	rt := newCoalescerTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.coalescer.start(ctx)

	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				rt.emitServersChanged("concurrent", map[string]any{"goroutine": i, "j": j})
			}
		}(i)
	}
	wg.Wait()

	// 20 × 50 = 1000 submissions in << 1 interval. After the first window
	// elapses and a follow-up window, expect a small bounded number of events.
	events := collectEvents(ch, 200*time.Millisecond)
	assert.LessOrEqual(t, len(events), 5, "expected coalesced result; got %d events", len(events))
}
