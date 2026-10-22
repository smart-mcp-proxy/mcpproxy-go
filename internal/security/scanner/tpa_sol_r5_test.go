package scanner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Sol round-5 findings on the US3 refresher (group "refresh"). The scheduler
// keeps one authoritative next-due time; these pin the two sequences the
// counter-based stale-tick guard of rounds 3 and 4 got wrong.

// A settings change during a manual cycle asks for an early look. The manual
// cycle it superseded finishing afterwards must not turn that early timer into
// a stale tick: when it fires, a cycle runs under the new settings.
func TestSolR5_EarlyLookArmedDuringASupersededManualCycleRuns(t *testing.T) {
	ch := newTestChannel(t)
	pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "held"})
	g := holdReleaseJSON(ch, pair, 2)
	f := newFileAndFetchFixture(t, ch, true)
	h := newRefresherHarness(t, f, nil)
	h.start()
	timer := h.clock.loop(t)

	manual := make(chan struct{})
	go func() {
		defer close(manual)
		h.ref.Refresh(t.Context(), f.log)
	}()
	g.waitReached(t)

	// The fake clock does not move: a different rand keeps the early look off
	// the instant the first-cycle timer is already armed for.
	h.setRand(0.2)
	arms := timer.arms()
	h.reload(func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(2) })
	require.Eventually(t, func() bool { return timer.arms() > arms }, 5*time.Second, time.Millisecond,
		"the settings change re-arms the timer for an early look")
	require.Less(t, timer.last(), 6*time.Minute)

	close(g.release)
	<-manual
	h.waitClass() // the superseded manual cycle
	assert.Never(t, func() bool { return timer.last() >= 6*time.Minute }, 150*time.Millisecond, time.Millisecond,
		"the superseded manual cycle keeps the early look armed")
	requests := ch.count()

	timer.Fire()

	h.waitClass()
	info := BundleStatus()
	require.NotNil(t, info.Refresh)
	assert.Equal(t, RefreshTriggerTimer, info.Refresh.LastTrigger, "the early look ran as a timer cycle")
	assert.Greater(t, ch.count(), requests, "the early look reached the channel under the new settings")
}

// A manual completion that wakes the loop is consumed (the loop re-arms) before
// the cycle goroutine has finished. The next legitimate tick must still run its
// cycle, not be discarded as stale.
func TestSolR5_ManualWakeConsumedBeforeCleanupKeepsTheNextTick(t *testing.T) {
	f := newFileAndFetchFixture(t, nil, false)
	h := newRefresherHarness(t, f, nil)
	var timer *fakeTimer
	var arms int
	h.ref.afterCompletionWake = func(trigger string) {
		if trigger != RefreshTriggerManual {
			return
		}
		assert.Eventually(t, func() bool { return timer.arms() > arms }, 5*time.Second, time.Millisecond,
			"the loop consumes the completion's wake and re-arms")
	}
	h.start()
	timer = h.clock.loop(t)
	arms = timer.arms()

	h.ref.Refresh(t.Context(), f.log)
	h.waitClass()
	require.Greater(t, timer.arms(), arms)

	timer.Fire()

	h.waitClass()
	info := BundleStatus()
	require.NotNil(t, info.Refresh)
	assert.Equal(t, RefreshTriggerTimer, info.Refresh.LastTrigger, "the re-armed daily tick ran its cycle")
}
