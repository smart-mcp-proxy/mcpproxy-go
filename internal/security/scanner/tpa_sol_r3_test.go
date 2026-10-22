package scanner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Sol round-3 findings on the US3 fetch adapter and refresher (group "refresh").

// One attempt makes several requests. A backoff announced by an earlier
// response survives a later failure and a later success (B6).
func TestSolR3_MultiRequestFetchKeepsTheLatestBackoff(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	reset := now.Add(3 * time.Hour)
	quota := map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": itoa(uint64(reset.Unix()))}
	withNow := func(s *fetchSettings) { s.Now = func() time.Time { return now } }

	t.Run("releases: listing backoff survives a failed bundle download", func(t *testing.T) {
		ch := newTestChannel(t)
		ch.publishRelease(2, "r2")
		ch.serve(releasesPath, 200, ch.releasesJSON("scanner-bundle-2.json", "scanner-bundle-2.json.sig", "notes.txt"), quota)
		ch.serve("/dl/scanner-bundle-2.json", 500, nil, nil)
		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, withNow))
		require.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome)
		assert.Equal(t, reset, res.RetryAt)
	})
	t.Run("releases: a successful sidecar download's backoff is kept on the candidate", func(t *testing.T) {
		ch := newTestChannel(t)
		pair := ch.publishRelease(2, "r2")
		ch.serve("/dl/scanner-bundle-2.json.sig", 200, pair.Sidecar, map[string]string{"Retry-After": "7200"})
		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, withNow))
		require.Equal(t, fetchOutcomeCandidate, res.Outcome, "reason=%s detail=%s", res.Reason, res.Detail)
		assert.Equal(t, now.Add(2*time.Hour), res.RetryAt)
	})
	t.Run("releases: the latest of several backoffs wins", func(t *testing.T) {
		ch := newTestChannel(t)
		pair := ch.publishRelease(2, "r2")
		ch.serve(releasesPath, 200, ch.releasesJSON("scanner-bundle-2.json", "scanner-bundle-2.json.sig", "notes.txt"), map[string]string{"Retry-After": "600"})
		ch.serve("/dl/scanner-bundle-2.json", 200, pair.Bundle, map[string]string{"Retry-After": "7200"})
		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, withNow))
		require.Equal(t, fetchOutcomeCandidate, res.Outcome)
		assert.Equal(t, now.Add(2*time.Hour), res.RetryAt)
	})
	t.Run("direct: bundle backoff survives a failed sidecar", func(t *testing.T) {
		ch := newTestChannel(t)
		const path = "/bundles/scanner-bundle.json"
		pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "r2"})
		ch.serve(path, 200, pair.Bundle, quota)
		ch.serve(path+".sig", 500, nil, nil)
		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+path, withNow))
		require.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome)
		assert.Equal(t, reset, res.RetryAt)
	})
	t.Run("direct: a successful sidecar's backoff is kept on the candidate", func(t *testing.T) {
		ch := newTestChannel(t)
		const path = "/bundles/scanner-bundle.json"
		pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "r2"})
		ch.serve(path, 200, pair.Bundle, nil)
		ch.serve(path+".sig", 200, pair.Sidecar, map[string]string{"Retry-After": "7200"})
		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+path, withNow))
		require.Equal(t, fetchOutcomeCandidate, res.Outcome)
		assert.Equal(t, now.Add(2*time.Hour), res.RetryAt)
	})
}

// Start captured config A (24h), a reload then published config B (6h) and
// nudged before Start took its lock. Start must not put A back over B.
func TestSolR3_StartKeepsWhatAConcurrentNudgeDelivered(t *testing.T) {
	f := newFileAndFetchFixture(t, nil, false)
	h := newRefresherHarness(t, f, nil)
	h.ref.startCaptured = func() {
		h.reload(func(s *config.SecurityConfig) { s.TPARefreshInterval = config.Duration(6 * time.Hour) })
	}
	h.start()

	h.ref.mu.Lock()
	interval, fp := h.ref.interval, h.ref.fp
	h.ref.mu.Unlock()
	h.mu.Lock()
	want := policyFromConfig(h.cfg).Fingerprint
	h.mu.Unlock()
	assert.Equal(t, 6*time.Hour, interval, "the nudged cadence stands")
	assert.Equal(t, want, fp, "the nudged fingerprint stands")
}

// A manual refresh whose caller gave up still ends in a re-arm of the timer:
// the cycle, not the waiting caller, owns it.
func TestSolR3_ManualCycleRearmsEvenWhenItsCallerCancelled(t *testing.T) {
	f := newFileAndFetchFixture(t, nil, false)
	h := newRefresherHarness(t, f, nil)
	inCycle, release := make(chan struct{}), make(chan struct{})
	h.ref.afterCapture = func() {
		inCycle <- struct{}{}
		<-release
	}
	h.start()
	timer := h.clock.loop(t)
	arms := timer.arms()

	ctx, cancel := context.WithCancel(t.Context())
	returned := make(chan struct{})
	go func() {
		h.ref.Refresh(ctx, f.log)
		close(returned)
	}()
	<-inCycle
	cancel()
	<-returned
	close(release)

	h.waitClass()
	require.Eventually(t, func() bool { return timer.arms() > arms }, 5*time.Second, time.Millisecond,
		"the manual cycle's completion must re-arm the timer although its caller left")
}
