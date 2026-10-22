package scanner

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sol round-4 findings on the US3 fetch adapter and refresher (group "refresh").

// A timer tick already taken by the loop when a manual cycle completes is stale:
// the manual cycle satisfied the check and recorded a rate limit. Starting a
// cycle for it would hit the channel again inside the backoff (B6).
func TestSolR4_StaleTimerTickAfterManualCycleDoesNotStartACycle(t *testing.T) {
	ch := newTestChannel(t)
	ch.serve(releasesPath, 429, nil, map[string]string{"Retry-After": "86400"})
	f := newFileAndFetchFixture(t, ch, true)
	h := newRefresherHarness(t, f, nil)

	atTick, releaseTick := make(chan struct{}), make(chan struct{})
	h.ref.beforeTimerCycle = func() {
		atTick <- struct{}{}
		<-releaseTick
	}
	h.start()
	timer := h.clock.loop(t)
	arms := timer.arms()

	timer.Fire()
	<-atTick // the loop holds the old tick and has not started its cycle

	h.ref.Refresh(context.Background(), f.log)
	require.Equal(t, 1, ch.count(), "the manual cycle made the one request")

	close(releaseTick)
	require.Eventually(t, func() bool { return timer.arms() > arms }, 5*time.Second, time.Millisecond,
		"the stale tick must be consumed with a re-arm")
	assert.Equal(t, 1, ch.count(), "the stale tick must not start a cycle inside the recorded backoff")
	assert.GreaterOrEqual(t, timer.last(), 24*time.Hour, "re-armed past the rate limit the manual cycle recorded")
}

// A redirect response is a response: the backoff it announced survives the hop,
// whatever the destination (or the failed redirect) returns (B6).
func TestSolR4_RedirectResponsesKeepTheirBackoff(t *testing.T) {
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	withNow := func(s *fetchSettings) { s.Now = func() time.Time { return now } }
	redirect := func(ch *testChannel, from, to string, hdr map[string]string) {
		ch.handle(from, func(w http.ResponseWriter, _ *http.Request) {
			for k, v := range hdr {
				w.Header().Set(k, v)
			}
			w.Header().Set("Location", to)
			w.WriteHeader(http.StatusFound)
		})
	}

	t.Run("listing redirect with Retry-After, destination 500", func(t *testing.T) {
		ch := newTestChannel(t)
		redirect(ch, releasesPath, ch.srv.URL+"/moved", map[string]string{"Retry-After": "7200"})
		ch.serve("/moved", 500, nil, nil)
		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, withNow))
		require.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome)
		assert.Equal(t, now.Add(2*time.Hour), res.RetryAt)
	})
	t.Run("download redirect with an exhausted quota, destination 200", func(t *testing.T) {
		ch := newTestChannel(t)
		pair := ch.publishRelease(2, "r2")
		ch.serve(releasesPath, 200, ch.releasesJSON("scanner-bundle-2.json", "scanner-bundle-2.json.sig"), nil)
		reset := now.Add(3 * time.Hour)
		redirect(ch, "/dl/scanner-bundle-2.json", ch.srv.URL+"/cdn/scanner-bundle-2.json",
			map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": itoa(uint64(reset.Unix()))})
		ch.serve("/cdn/scanner-bundle-2.json", 200, pair.Bundle, nil)
		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, withNow))
		require.Equal(t, fetchOutcomeCandidate, res.Outcome, "reason=%s detail=%s", res.Reason, res.Detail)
		assert.Equal(t, reset, res.RetryAt)
	})
	t.Run("a redirect the client refuses still reports its backoff", func(t *testing.T) {
		ch := newTestChannel(t)
		redirect(ch, releasesPath, "http://[::1", map[string]string{"Retry-After": "7200"})
		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, withNow))
		assert.Equal(t, now.Add(2*time.Hour), res.RetryAt, "outcome=%s reason=%s", res.Outcome, res.Reason)
	})
}
