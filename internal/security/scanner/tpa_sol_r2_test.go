package scanner

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Sol round-2 findings on the US3 fetch adapter, scheduler and runtime (group
// "refresh").

// A 304 still carries rate-limit headers: both channel shapes must keep the
// validator AND the backoff they announce (B6).
func TestSolR2_NotModifiedKeepsTheRateLimitBackoff(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	reset := now.Add(3 * time.Hour)
	for name, path := range map[string]string{"releases": releasesPath, "direct": "/bundles/scanner-bundle.json"} {
		settings := func(t *testing.T, ch *testChannel) fetchSettings {
			return fetchTestSettings(t, ch.srv.URL+path, func(s *fetchSettings) {
				s.ETag = `"cached"`
				s.Now = func() time.Time { return now }
			})
		}
		t.Run(name+"/Retry-After", func(t *testing.T) {
			ch := newTestChannel(t)
			ch.serve(path, 304, nil, map[string]string{"Retry-After": "7200"})
			res := fetchCandidate(fetchCtx(t), settings(t, ch))
			assert.Equal(t, RefreshOutcomeNotModified, res.Outcome)
			assert.Equal(t, `"cached"`, res.ETag, "the validator is kept")
			assert.Equal(t, now.Add(2*time.Hour), res.RetryAt)
		})
		t.Run(name+"/exhausted quota", func(t *testing.T) {
			ch := newTestChannel(t)
			ch.serve(path, 304, nil, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": itoa(uint64(reset.Unix()))})
			res := fetchCandidate(fetchCtx(t), settings(t, ch))
			assert.Equal(t, RefreshOutcomeNotModified, res.Outcome)
			assert.Equal(t, reset, res.RetryAt)
		})
		t.Run(name+"/plain 304", func(t *testing.T) {
			ch := newTestChannel(t)
			ch.serve(path, 304, nil, nil)
			res := fetchCandidate(fetchCtx(t), settings(t, ch))
			assert.True(t, res.RetryAt.IsZero())
		})
	}
}

// A manual completion must not replace an early re-arm a Nudge asked for after
// the manual call began: the check and the pending-wake update are one step.
func TestSolR2_ManualCompletionDoesNotOverwriteAnEarlyRearm(t *testing.T) {
	r := NewBundleRefresher(BundleRefresherOptions{Clock: newFakeClock()})
	seq := r.soonSeq

	// The early re-arm: Nudge bumped soonSeq and queued wakeSoon.
	r.mu.Lock()
	r.soonSeq++
	r.mu.Unlock()
	r.signal(wakeSoon)
	// The loop consumed the early re-arm and armed its timer.
	<-r.wake
	require.Equal(t, wakeSoon, r.takePending())

	r.signalManualUnlessSoon(seq)

	assert.Equal(t, wakeNone, r.takePending(), "the manual completion must not queue a full-interval re-arm over the early one")
	select {
	case <-r.wake:
		t.Fatal("a suppressed manual completion must not wake the loop")
	default:
	}

	r.signalManualUnlessSoon(r.soonSeq)
	assert.Equal(t, wakeManual, r.takePending(), "with no early look pending the manual completion re-arms")
	<-r.wake
}

// A response fetched under a superseded generation must not restore a
// rate-limit delay the settings change cleared.
func TestSolR2_SupersededResponseDoesNotRestoreTheRetryDelay(t *testing.T) {
	ch := newTestChannel(t)
	gate := make(chan struct{})
	reached := make(chan struct{})
	ch.handle(releasesPath, func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Retry-After", "86400")
		w.WriteHeader(429)
	})
	f := newChannelFixture(t, ch)
	h := newRefresherHarness(t, f, nil)
	h.start()

	h.clock.loop(t).Fire()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the cycle never reached the channel")
	}
	// The operator disables fetching while the request is in flight.
	h.reload(func(s *config.SecurityConfig) { s.TPAFetchEnabled = false })
	close(gate)
	h.waitClass()

	h.ref.mu.Lock()
	retryAt := h.ref.retryAt
	gen := h.ref.gen
	h.ref.mu.Unlock()
	assert.True(t, retryAt.IsZero(), "the obsolete 24h delay must not come back: %v", retryAt)

	// The live generation still records its delay.
	want := time.Now().Add(time.Hour)
	h.ref.setRetryAt(want, gen)
	h.ref.mu.Lock()
	assert.Equal(t, want, h.ref.retryAt)
	h.ref.mu.Unlock()
}

// A Nudge that begins after Stop publishes nothing.
func TestSolR2_NudgeAfterStopDoesNotChangeTheDisplay(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.sec.TPAStaleAfterDays = intp(60)
	f.run()
	h := newRefresherHarness(t, f, nil)
	h.start()
	h.ref.Stop()

	next := cloneSecurity(f.sec)
	next.TPAStaleAfterDays = intp(77)
	next.TPARefreshInterval = config.Duration(6 * time.Hour)
	h.ref.Nudge(next)

	info := BundleStatus()
	require.NotNil(t, info.StaleAfterDays)
	assert.Equal(t, 60, *info.StaleAfterDays, "a stopped refresher must not publish settings")
	require.NotNil(t, info.Refresh)
	assert.NotEqual(t, int((6 * time.Hour).Seconds()), info.Refresh.IntervalSeconds)
}

// A configure waiting behind a reload must have its display settings win: the
// reload applied the policy it captured under the lock, the configure is later.
func TestSolR2_ConfigureDisplaySettingsAreOrderedWithAReload(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.sec.TPAStaleAfterDays = intp(60)
	f.run()

	bundleRT.mu.Lock()
	old := bundleRT.policyOrDefaultLocked() // a reload holds the lock with the old policy
	f.sec.TPAStaleAfterDays = intp(77)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.run()
	}()
	time.Sleep(100 * time.Millisecond) // let the configure reach the lock (or, unordered, publish early)
	bundleRT.disp.applySettings(old)   // the reload publishes the old policy's settings
	bundleRT.mu.Unlock()
	<-done

	info := BundleStatus()
	require.NotNil(t, info.StaleAfterDays)
	assert.Equal(t, 77, *info.StaleAfterDays, "the later configure's threshold must be the one displayed")
}

// The leg-specific warning does not outlive the repair of its cause, even when
// every cycle since is a file activation that skips the (now eligible) fetch
// leg.
func TestSolR2_RepairedFetchConfigClearsTheLegSpecificWarning(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorSigners = nil
		s.TPAFetchEnabled = true
		s.TPAFetchURL = fetchChannelURL
	}})
	f.run()
	h := newRefresherHarness(t, f, nil)
	h.start()
	f.put(unsignedCandidate(t, bundleOpts{Seq: 5, Tag: "day-1"}))

	require.Equal(t, RefreshClassActivated, h.tick())
	require.Equal(t, RefreshReasonNoTrustedSigner, BundleStatus().Refresh.FetchReason)
	require.Len(t, BundleDoctorWarnings(BundleStatus(), time.Now()), 1)

	// The operator repairs the configuration; a new local bundle activates.
	h.reload(func(s *config.SecurityConfig) {
		s.TPAOperatorSigners = []string{keyOperator.b64}
		s.TPAOperatorBaseline = u64p(1)
	})
	f.put(unsignedCandidate(t, bundleOpts{Seq: 6, Tag: "day-2"}))
	require.Equal(t, RefreshClassActivated, h.tick())

	info := BundleStatus()
	require.NotNil(t, info.Refresh)
	assert.Empty(t, info.Refresh.FetchReason, "the stale leg state is cleared once the leg is eligible")
	assert.Empty(t, BundleDoctorWarnings(info, time.Now()))
}
