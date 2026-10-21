package scanner

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// refresherHarness wires a BundleRefresher to a fake clock, a controllable
// rand, a live-config provider and the one-class-per-cycle observer.
type refresherHarness struct {
	t       *testing.T
	f       *rtFixture
	clock   *fakeClock
	ref     *BundleRefresher
	classes chan RefreshOutcomeClass

	mu  sync.Mutex
	cfg *config.SecurityConfig
	rnd float64
}

func cloneSecurity(sec *config.SecurityConfig) *config.SecurityConfig {
	c := *sec
	return &c
}

func newRefresherHarness(t *testing.T, f *rtFixture, mutate func(*BundleRefresherOptions)) *refresherHarness {
	t.Helper()
	h := &refresherHarness{t: t, f: f, clock: newFakeClock(), classes: make(chan RefreshOutcomeClass, 64), cfg: cloneSecurity(f.sec), rnd: 0.5}
	opts := BundleRefresherOptions{
		Clock:          h.clock,
		Rand:           func() float64 { h.mu.Lock(); defer h.mu.Unlock(); return h.rnd },
		ConfigProvider: func() *config.SecurityConfig { h.mu.Lock(); defer h.mu.Unlock(); return cloneSecurity(h.cfg) },
		Logger:         f.log,
		Version:        "1.2.3",
	}
	if mutate != nil {
		mutate(&opts)
	}
	h.ref = NewBundleRefresher(opts)
	SetBundleRefresher(h.ref)
	SetBundleRefreshObserver(func(c RefreshOutcomeClass) {
		select {
		case h.classes <- c:
		default:
		}
	})
	t.Cleanup(func() {
		h.ref.Stop()
		SetBundleRefresher(nil)
		SetBundleRefreshObserver(nil)
	})
	// The construction-time apply of the process.
	ConfigureBundleFromConfig(h.cfg, f.log)
	return h
}

func (h *refresherHarness) start() { h.ref.Start(context.Background()) }

func (h *refresherHarness) setRand(v float64) {
	h.mu.Lock()
	h.rnd = v
	h.mu.Unlock()
}

// reload mutates the live config the way a config.reloaded event does: apply it
// to the runtime, then nudge the refresher.
func (h *refresherHarness) reload(mutate func(*config.SecurityConfig)) {
	h.t.Helper()
	h.mu.Lock()
	next := cloneSecurity(h.cfg)
	mutate(next)
	h.cfg = next
	h.mu.Unlock()
	ConfigureBundleFromConfig(next, h.f.log)
	h.ref.Nudge(cloneSecurity(next))
}

func (h *refresherHarness) waitClass() RefreshOutcomeClass {
	h.t.Helper()
	select {
	case c := <-h.classes:
		return c
	case <-time.After(10 * time.Second):
		h.t.Fatal("no refresh cycle completed")
		return ""
	}
}

// tick fires the scheduler's timer and waits for the cycle it starts.
func (h *refresherHarness) tick() RefreshOutcomeClass {
	h.t.Helper()
	timer := h.clock.loop(h.t)
	arms := timer.arms()
	timer.Fire()
	class := h.waitClass()
	// The observer fires inside the cycle; the loop re-arms right after it.
	require.Eventually(h.t, func() bool { return timer.arms() > arms }, 10*time.Second, time.Millisecond, "the timer was never re-armed")
	return class
}

func (h *refresherHarness) requireNoMoreCycles() {
	h.t.Helper()
	select {
	case c := <-h.classes:
		h.t.Fatalf("unexpected extra refresh cycle (%s)", c)
	case <-time.After(150 * time.Millisecond):
	}
}

// fileFixture is an operator-root install with a configured bundle path and a
// fetch URL (fetch off unless the test turns it on).
func newFileAndFetchFixture(t *testing.T, ch *testChannel, fetch bool) *rtFixture {
	t.Helper()
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
		s.TPAFetchEnabled = fetch
		if ch != nil {
			s.TPAFetchURL = ch.srv.URL + releasesPath
		}
	}})
	// Production applies the config before the refresher ever runs.
	f.run()
	return f
}

// newChannelFixture is a fetch-only install (no bundle path) pointed at ch.
func newChannelFixture(t *testing.T, ch *testChannel) *rtFixture {
	t.Helper()
	return newFetchFixture(t, func(s *config.SecurityConfig) { s.TPAFetchURL = ch.srv.URL + releasesPath })
}

// T327: cadence.
func TestRefreshScheduler_Cadence(t *testing.T) {
	t.Run("the first cycle lands in [30s,5m]", func(t *testing.T) {
		for _, tc := range []struct {
			rnd      float64
			min, max time.Duration
		}{{0, 30 * time.Second, 30 * time.Second}, {0.5, 30 * time.Second, 5 * time.Minute}, {0.999999, 4*time.Minute + 59*time.Second, 5 * time.Minute}} {
			f := newFileAndFetchFixture(t, nil, false)
			h := newRefresherHarness(t, f, nil)
			h.setRand(tc.rnd)
			h.start()
			first := h.clock.loop(t).firstDuration()
			assert.GreaterOrEqual(t, first, tc.min, "rand=%v", tc.rnd)
			assert.LessOrEqual(t, first, tc.max, "rand=%v", tc.rnd)
			h.ref.Stop()
		}
	})

	t.Run("later cycles land at interval +/- 10%", func(t *testing.T) {
		for _, tc := range []struct {
			rnd      float64
			min, max time.Duration
		}{{0, 21*time.Hour + 35*time.Minute + 59*time.Second, 21*time.Hour + 36*time.Minute + time.Second}, {0.5, 23*time.Hour + 59*time.Minute + 59*time.Second, 24*time.Hour + time.Second}, {0.999999, 26*time.Hour + 20*time.Minute, 26*time.Hour + 24*time.Minute + time.Second}} {
			f := newFileAndFetchFixture(t, nil, false)
			h := newRefresherHarness(t, f, nil)
			h.start()
			h.setRand(tc.rnd)
			h.tick()
			timer := h.clock.loop(t)
			require.Eventually(t, func() bool { return timer.arms() >= 2 }, 5*time.Second, time.Millisecond)
			assert.GreaterOrEqual(t, timer.last(), tc.min, "rand=%v", tc.rnd)
			assert.LessOrEqual(t, timer.last(), tc.max, "rand=%v", tc.rnd)
			h.ref.Stop()
		}
	})

	t.Run("a 10-day forward jump yields exactly one cycle", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		h := newRefresherHarness(t, f, nil)
		h.start()
		timer := h.clock.loop(t)
		h.clock.Advance(240 * time.Hour) // a suspended laptop waking up
		h.requireNoMoreCycles()          // wall-clock time alone fires nothing

		h.tick()

		h.requireNoMoreCycles()
		require.Eventually(t, func() bool { return timer.arms() == 2 }, 5*time.Second, time.Millisecond, "re-armed once, no catch-up burst")
	})

	t.Run("a backward wall-clock jump does not wedge the timer", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		h := newRefresherHarness(t, f, nil)
		h.start()
		h.tick()
		h.clock.Advance(-72 * time.Hour)
		h.tick()
		h.tick()
	})

	t.Run("an interval change via Nudge re-arms the timer", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		h := newRefresherHarness(t, f, nil)
		h.start()
		timer := h.clock.loop(t)
		armsBefore := timer.arms()

		h.reload(func(s *config.SecurityConfig) { s.TPARefreshInterval = config.Duration(6 * time.Hour) })

		require.Eventually(t, func() bool { return timer.arms() > armsBefore }, 5*time.Second, time.Millisecond)
		assert.InDelta(t, float64(6*time.Hour), float64(timer.last()), float64(time.Second), "rand=0.5 is the interval itself")
		info := BundleStatus()
		require.NotNil(t, info.Refresh)
		assert.Equal(t, int((6 * time.Hour).Seconds()), info.Refresh.IntervalSeconds)

		armsAfter := timer.arms()
		h.reload(func(s *config.SecurityConfig) { s.TPAStaleAfterDays = intp(10) }) // unrelated
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, armsAfter, timer.arms(), "a nudge that changes nothing relevant does not re-arm")
	})

	t.Run("an interval below the 1h floor is clamped", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		f.sec.TPARefreshInterval = config.Duration(time.Minute)
		h := newRefresherHarness(t, f, nil)
		h.start()
		h.tick()
		timer := h.clock.loop(t)
		require.Eventually(t, func() bool { return timer.arms() >= 2 }, 5*time.Second, time.Millisecond)
		assert.GreaterOrEqual(t, timer.last(), 54*time.Minute)
	})
}

// T327: the file leg (087 US1, FR-004, SC-002).
func TestRefreshScheduler_FileLeg(t *testing.T) {
	t.Run("a dropped file activates on the next cycle without a restart", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		obs := recordActivations(t)
		h := newRefresherHarness(t, f, nil)
		h.start()
		require.Equal(t, BundleSourceEmbedded, BundleStatus().Source)
		c := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "dropped"})
		f.put(c)

		class := h.tick()

		assert.Equal(t, RefreshClassActivated, class)
		info := BundleStatus()
		assert.Equal(t, digestOf(c.Bundle), info.FullDigest)
		assert.Equal(t, BundleSourceFile, info.Source)
		require.NotNil(t, info.Refresh)
		assert.Equal(t, RefreshTriggerTimer, info.Refresh.LastTrigger)
		assert.Equal(t, RefreshOutcomeActivated, info.Refresh.LastOutcome)
		require.NotNil(t, info.Refresh.LastAttemptAt)
		assert.Equal(t, 1, obs.count())
	})

	t.Run("manual refresh with fetch disabled activates a dropped file with zero HTTP requests", func(t *testing.T) {
		ch := newTestChannel(t)
		f := newFileAndFetchFixture(t, ch, false)
		h := newRefresherHarness(t, f, nil)
		h.start()
		c := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "dropped"})
		f.put(c)

		info := RefreshBundle(context.Background(), f.log)

		assert.Equal(t, digestOf(c.Bundle), info.FullDigest, "load_error=%q", info.LoadError)
		require.NotNil(t, info.Refresh)
		assert.Equal(t, RefreshTriggerManual, info.Refresh.LastTrigger)
		assert.Equal(t, RefreshOutcomeActivated, info.Refresh.LastOutcome)
		assert.Zero(t, ch.count())
		assert.Equal(t, RefreshClassActivated, h.waitClass())
	})

	t.Run("a refused dropped file sets last_rejection and the previous bundle keeps serving", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		good := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "good"})
		f.activate(good)
		h := newRefresherHarness(t, f, nil)
		h.start()
		bad := signedCandidate(t, keyOperator, bundleOpts{Seq: 6, Tag: "bad"})
		bad.Bundle = flipByte(t, bad.Bundle, "bundle-marker")
		f.put(bad)

		class := h.tick()

		assert.Equal(t, RefreshClassRejected, class)
		info := BundleStatus()
		assert.Equal(t, digestOf(good.Bundle), info.FullDigest)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectTamper, info.LastRejection.Reason)
		assert.Equal(t, RefreshOutcomeRejected, info.Refresh.LastOutcome)
	})

	t.Run("ReloadBundle issues zero HTTP requests with fetch enabled", func(t *testing.T) {
		ch := newTestChannel(t)
		ch.publishRelease(2, "never")
		f := newFileAndFetchFixture(t, ch, true)
		h := newRefresherHarness(t, f, nil)
		h.start()

		info := ReloadBundle(f.log)

		assert.Equal(t, BundleSourceEmbedded, info.Source)
		assert.Zero(t, ch.count(), "reload is the local path only")
	})
}

// gatedRelease serves a release whose JSON handler blocks until released, to
// hold a cycle in flight.
type gatedRelease struct {
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func holdReleaseJSON(ch *testChannel, pair Candidate, seq uint64) *gatedRelease {
	g := &gatedRelease{reached: make(chan struct{}), release: make(chan struct{})}
	name := "scanner-bundle-" + itoa(seq) + ".json"
	ch.serve("/dl/"+name, 200, pair.Bundle, nil)
	ch.serve("/dl/"+name+".sig", 200, pair.Sidecar, nil)
	body := ch.releasesJSON(name, name+".sig")
	ch.handle(releasesPath, func(w http.ResponseWriter, r *http.Request) {
		g.once.Do(func() { close(g.reached) })
		select {
		case <-g.release:
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write(body)
	})
	return g
}

func (g *gatedRelease) waitReached(t *testing.T) {
	t.Helper()
	select {
	case <-g.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the cycle never reached the channel")
	}
}

// T327: single flight.
func TestRefreshScheduler_SingleFlight(t *testing.T) {
	t.Run("a manual refresh during an in-flight cycle joins it", func(t *testing.T) {
		ch := newTestChannel(t)
		pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "join"})
		g := holdReleaseJSON(ch, pair, 2)
		f := newFileAndFetchFixture(t, ch, true)
		obs := recordActivations(t)
		h := newRefresherHarness(t, f, nil)
		h.start()

		h.clock.loop(t).Fire()
		g.waitReached(t)
		done := make(chan BundleInfo, 1)
		go func() { done <- RefreshBundle(context.Background(), f.log) }()
		select {
		case <-done:
			t.Fatal("the manual refresh returned before the in-flight cycle finished: it did not join")
		case <-time.After(200 * time.Millisecond):
		}
		close(g.release)

		var info BundleInfo
		select {
		case info = <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the joined refresh never returned")
		}
		assert.Equal(t, digestOf(pair.Bundle), info.FullDigest)
		assert.Equal(t, BundleSourceFetch, info.Source)
		assert.Equal(t, 3, ch.count(), "one release JSON, one bundle, one sidecar")
		assert.Equal(t, 1, obs.count())
		assert.Len(t, f.store.historyRows(), 1)
		assert.Equal(t, RefreshClassActivated, h.waitClass())
		h.requireNoMoreCycles()
	})

	t.Run("tick, reload and refresh racing give one activation per distinct candidate", func(t *testing.T) {
		ch := newTestChannel(t)
		ch.publishRelease(2, "race")
		f := newFileAndFetchFixture(t, ch, true)
		f.put(signedCandidate(t, keyOperator, bundleOpts{Seq: 3, Tag: "file"}))
		obs := recordActivations(t)
		h := newRefresherHarness(t, f, nil)
		h.start()

		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); h.clock.loop(t).Fire() }()
		go func() { defer wg.Done(); ReloadBundle(f.log) }()
		go func() { defer wg.Done(); RefreshBundle(context.Background(), f.log) }()
		wg.Wait()
		h.waitClass()
		drainBundleObserversForTest()

		// File seq 3 is above the fetched seq 2: it wins and the fetched release is
		// below the new watermark, so the run settles on one activation.
		rows := f.store.historyRows()
		assert.Len(t, rows, 1, "one activation per distinct valid candidate")
		assert.Equal(t, 1, obs.count())
		assert.Equal(t, BundleSourceFile, BundleStatus().Source)
	})
}

// T327: a config change while a download is in flight supersedes its candidate.
func TestRefreshScheduler_MidFlightNudgeSupersedes(t *testing.T) {
	changes := map[string]func(*config.SecurityConfig){
		"the URL changes": func(s *config.SecurityConfig) { s.TPAFetchURL = "https://elsewhere.test/repos/a/b/releases/latest" },
		"the fetch is disabled": func(s *config.SecurityConfig) {
			s.TPAFetchEnabled = false
		},
		"the active root changes":       func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootPublisher },
		"the operator signers change":   func(s *config.SecurityConfig) { s.TPAOperatorSigners = []string{keyUntrusted.b64} },
		"require_signed_bundle flips":   func(s *config.SecurityConfig) { s.RequireSignedBundle = true },
		"the bundle path flips":         func(s *config.SecurityConfig) { s.TPABundlePath = "" },
		"the operator epoch changes":    func(s *config.SecurityConfig) { s.TPAOperatorEpoch = u64p(3) },
		"the operator baseline changes": func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(0) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			ch := newTestChannel(t)
			pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "superseded"})
			g := holdReleaseJSON(ch, pair, 2)
			f := newFileAndFetchFixture(t, ch, true)
			obs := recordActivations(t)
			h := newRefresherHarness(t, f, nil)
			h.start()

			h.clock.loop(t).Fire()
			g.waitReached(t)
			h.reload(change)
			close(g.release)

			assert.Equal(t, RefreshClassUnchanged, h.waitClass(), "superseded is an unchanged-class cycle")
			info := BundleStatus()
			require.NotNil(t, info.Refresh)
			assert.Equal(t, RefreshOutcomeSuperseded, info.Refresh.LastOutcome)
			assert.NotEqual(t, digestOf(pair.Bundle), info.FullDigest, "the candidate was discarded")
			assert.Equal(t, 0, obs.count())
			assert.Empty(t, f.store.historyRows())
		})
	}

	t.Run("a nudge after Stop is a no-op", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		h := newRefresherHarness(t, f, nil)
		h.start()
		timer := h.clock.loop(t)
		h.ref.Stop()
		arms := timer.arms()

		sec := cloneSecurity(f.sec)
		sec.TPARefreshInterval = config.Duration(3 * time.Hour)
		h.ref.Nudge(sec)

		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, arms, timer.arms())
		assert.False(t, h.ref.Running())
	})

	t.Run("nudges racing Stop are safe", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		h := newRefresherHarness(t, f, nil)
		h.start()
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sec := cloneSecurity(f.sec)
				sec.TPARefreshInterval = config.Duration(time.Duration(2+i) * time.Hour)
				h.ref.Nudge(sec)
			}(i)
		}
		h.ref.Stop()
		wg.Wait()
	})
}

// T327: lifecycle.
func TestRefreshScheduler_Lifecycle(t *testing.T) {
	t.Run("Stop cancels in-flight HTTP and returns only after the goroutine exited", func(t *testing.T) {
		ch := newTestChannel(t)
		pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "stop"})
		g := holdReleaseJSON(ch, pair, 2)
		cancelled := make(chan struct{})
		ch.handle(releasesPath, func(w http.ResponseWriter, r *http.Request) {
			g.once.Do(func() { close(g.reached) })
			<-r.Context().Done()
			close(cancelled)
		})
		f := newFileAndFetchFixture(t, ch, true)
		h := newRefresherHarness(t, f, nil)
		h.start()
		require.True(t, h.ref.Running())

		h.clock.loop(t).Fire()
		g.waitReached(t)
		stopped := make(chan struct{})
		go func() { h.ref.Stop(); close(stopped) }()

		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Fatal("Stop did not return: the in-flight request was not cancelled")
		}
		select {
		case <-cancelled:
		case <-time.After(10 * time.Second):
			t.Fatal("the server never saw the request cancelled")
		}
		assert.False(t, h.ref.Running())
		select {
		case c := <-h.classes:
			t.Fatalf("a cycle cancelled by Stop must not report (%s)", c)
		default:
		}
	})

	t.Run("a Start after Stop is a no-op", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		h := newRefresherHarness(t, f, nil)
		h.start()
		timer := h.clock.loop(t)
		h.ref.Stop()
		arms := timer.arms()

		h.start()

		assert.False(t, h.ref.Running())
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, arms, timer.arms())
		h.ref.Stop() // idempotent
	})

	t.Run("Start is bound to its context", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		h := newRefresherHarness(t, f, nil)
		ctx, cancel := context.WithCancel(context.Background())
		h.ref.Start(ctx)
		require.True(t, h.ref.Running())
		h.ref.Start(ctx) // a second Start is a no-op
		cancel()
		require.Eventually(t, func() bool { return !h.ref.Running() }, 5*time.Second, time.Millisecond)
	})

	t.Run("RefreshBundle on a stopped refresher falls back to ReloadBundle", func(t *testing.T) {
		ch := newTestChannel(t)
		ch.publishRelease(2, "stopped")
		f := newFileAndFetchFixture(t, ch, true)
		h := newRefresherHarness(t, f, nil)
		h.start()
		h.ref.Stop()
		c := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "dropped"})
		f.put(c)

		info := RefreshBundle(context.Background(), f.log)

		assert.Equal(t, digestOf(c.Bundle), info.FullDigest, "the dropped file is still picked up")
		assert.Zero(t, ch.count(), "and nothing is fetched")
	})

	t.Run("RefreshBundle without any refresher falls back to ReloadBundle", func(t *testing.T) {
		f := newFileAndFetchFixture(t, nil, false)
		SetBundleRefresher(nil)
		c := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "dropped"})
		f.put(c)
		info := RefreshBundle(context.Background(), f.log)
		assert.Equal(t, digestOf(c.Bundle), info.FullDigest)
	})

	t.Run("SetBundleRefresher(nil) clears the slot", func(t *testing.T) {
		r := NewBundleRefresher(BundleRefresherOptions{})
		SetBundleRefresher(r)
		assert.Same(t, r, CurrentBundleRefresher())
		SetBundleRefresher(nil)
		assert.Nil(t, CurrentBundleRefresher())
	})
}

// T327: failure accounting and log dedup.
func TestRefreshScheduler_FailureAccounting(t *testing.T) {
	ch := newTestChannel(t)
	f := newChannelFixture(t, ch)
	h := newRefresherHarness(t, f, nil)
	h.start()
	failing := func() { ch.serve(releasesPath, 500, []byte("boom"), nil) }
	invalid := func() { ch.serve(releasesPath, 200, ch.releasesJSON("notes.txt"), nil) }
	failures := func() int { return BundleStatus().Refresh.ConsecutiveFailures }

	failing()
	for i := 1; i <= 3; i++ {
		assert.Equal(t, RefreshClassUnavailable, h.tick())
		assert.Equal(t, i, failures())
	}
	info := BundleStatus()
	assert.Equal(t, RefreshOutcomeFetchUnavailable, info.Refresh.LastOutcome)
	assert.Equal(t, RefreshReasonHTTPStatus, info.Refresh.LastReason)
	assert.Nil(t, info.LastRejection, "a network failure is not a verification failure")
	require.NotNil(t, info.Refresh.NextCheckAt)
	assert.WithinDuration(t, h.clock.Now().Add(24*time.Hour), *info.Refresh.NextCheckAt, 2*time.Second, "a plain failure retries at the next jittered tick, with no exponential stretch")
	assert.Equal(t, 1, f.countLogs(zapcore.WarnLevel, "TPA bundle fetch failed"), "three identical failures log once")

	invalid()
	assert.Equal(t, RefreshClassUnavailable, h.tick())
	assert.Equal(t, 4, failures(), "channel_invalid counts as a failed cycle")
	assert.Equal(t, 2, f.countLogs(zapcore.WarnLevel, "TPA bundle fetch failed"), "a different (outcome, reason) logs again")

	h.clock.Advance(25 * time.Hour)
	h.tick()
	assert.Equal(t, 3, f.countLogs(zapcore.WarnLevel, "TPA bundle fetch failed"), "and an outcome that persists logs again after 24h")

	t.Run("not_modified resets the counter", func(t *testing.T) {
		ch.serve(releasesPath, http.StatusNotModified, nil, nil)
		assert.Equal(t, RefreshClassUnchanged, h.tick())
		assert.Equal(t, 0, failures())
		assert.Equal(t, RefreshOutcomeNotModified, BundleStatus().Refresh.LastOutcome)
	})

	t.Run("unchanged resets the counter", func(t *testing.T) {
		failing()
		h.tick()
		require.Equal(t, 1, failures())
		ch.publishRelease(1, "below-baseline") // at or below the operator baseline: hint skip
		assert.Equal(t, RefreshClassUnchanged, h.tick())
		assert.Equal(t, 0, failures())
		assert.Equal(t, RefreshOutcomeUnchanged, BundleStatus().Refresh.LastOutcome)
	})

	t.Run("rejected resets the counter and is one class", func(t *testing.T) {
		failing()
		h.tick()
		require.Equal(t, 1, failures())
		pair := ch.publishRelease(2, "tampered")
		ch.serve("/dl/scanner-bundle-2.json", 200, flipByte(t, pair.Bundle, "bundle-marker"), nil)
		assert.Equal(t, RefreshClassRejected, h.tick())
		assert.Equal(t, 0, failures())
		info := BundleStatus()
		assert.Equal(t, RefreshOutcomeRejected, info.Refresh.LastOutcome)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectTamper, info.LastRejection.Reason)
	})

	t.Run("activated resets the counter", func(t *testing.T) {
		failing()
		h.tick()
		require.Equal(t, 1, failures())
		ch.publishRelease(3, "good")
		assert.Equal(t, RefreshClassActivated, h.tick())
		assert.Equal(t, 0, failures())
		assert.Equal(t, BundleSourceFetch, BundleStatus().Source)
	})
	h.requireNoMoreCycles()
}
