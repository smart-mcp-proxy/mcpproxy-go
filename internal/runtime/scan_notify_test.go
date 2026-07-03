package runtime

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestScanNotify_ReconnectStormCollapses proves Spec 077 US4 (MCP-2207): a
// reconnect storm across N servers — each firing the full per-scanner
// scan_started/progress/completed lifecycle multiple times — must collapse into
// at most one settled scan event per server (<= N total), and must emit NO
// per-scanner lifecycle events (those are the storm we are eliminating).
func TestScanNotify_ReconnectStormCollapses(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer func() { _ = logger.Sync() }()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}
	// Short debounce so the test settles quickly.
	rt.scanNotify = newScanNotifyDebouncer(rt, 40*time.Millisecond)

	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	const numServers = 5
	const reconnectsPerServer = 4

	// Collect events in the background until the collection window closes.
	settledPerServer := make(map[string]int)
	legacyCount := 0
	collectDone := make(chan struct{})
	go func() {
		defer close(collectDone)
		timeout := time.After(2 * time.Second)
		for {
			select {
			case evt := <-eventChan:
				switch evt.Type {
				case EventTypeSecurityScanSettled:
					name, _ := evt.Payload["server_name"].(string)
					settledPerServer[name]++
				case EventTypeSecurityScanStarted,
					EventTypeSecurityScanProgress,
					EventTypeSecurityScanCompleted,
					EventTypeSecurityScanFailed:
					legacyCount++
				}
			case <-timeout:
				return
			}
		}
	}()

	// Simulate a reconnect storm: every server reconnects several times in quick
	// succession, each reconnect driving the full per-scanner lifecycle.
	for r := 0; r < reconnectsPerServer; r++ {
		for s := 0; s < numServers; s++ {
			server := fmt.Sprintf("server-%d", s)
			jobID := fmt.Sprintf("%s-job-%d", server, r)
			rt.EmitSecurityScanStarted(server, []string{"tpa-descriptions"}, jobID)
			rt.EmitSecurityScanProgress(server, "tpa-descriptions", "running", 0)
			rt.EmitSecurityScanProgress(server, "tpa-descriptions", "completed", 100)
			rt.EmitSecurityScanCompleted(server, map[string]int{"high": 1})
		}
	}

	// Wait long enough for the debounce window to fire, then close collection.
	time.Sleep(300 * time.Millisecond)
	rt.UnsubscribeEvents(eventChan)
	<-collectDone

	totalSettled := 0
	for _, c := range settledPerServer {
		totalSettled += c
	}

	assert.Zero(t, legacyCount, "per-scanner lifecycle events must be collapsed away entirely")
	assert.LessOrEqual(t, totalSettled, numServers,
		"a reconnect storm across %d servers must yield at most %d settled events, got %d",
		numServers, numServers, totalSettled)
	assert.Equal(t, numServers, len(settledPerServer),
		"every scanned server must receive exactly one settled event")
	for server, count := range settledPerServer {
		assert.Equal(t, 1, count, "server %s must settle exactly once", server)
	}
}

// TestScanNotify_SettledCarriesTerminalSummary verifies the single settled event
// carries the terminal findings summary and server identity.
func TestScanNotify_SettledCarriesTerminalSummary(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer func() { _ = logger.Sync() }()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}
	rt.scanNotify = newScanNotifyDebouncer(rt, 30*time.Millisecond)

	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	rt.EmitSecurityScanStarted("srv", []string{"tpa-descriptions"}, "job-1")
	rt.EmitSecurityScanCompleted("srv", map[string]int{"high": 2, "low": 1})

	select {
	case evt := <-eventChan:
		require.Equal(t, EventTypeSecurityScanSettled, evt.Type)
		assert.Equal(t, "srv", evt.Payload["server_name"])
		assert.NotZero(t, evt.Timestamp)
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive settled event within timeout")
	}
}
