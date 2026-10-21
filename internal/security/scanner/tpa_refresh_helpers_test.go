package scanner

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Shared helpers of the Spec 087 / Spec 101 US3 runtime-core tests (tasks
// T320-T329): the fetch fixture, an activation-observer recorder, a log
// counter and a fake clock whose timers fire on demand.

func intp(v int) *int { return &v }

// fetchChannelURL is the configured fetch URL of the runtime fixtures. No
// request is ever made to it: the runtime tests offer candidates directly.
const fetchChannelURL = "https://mirror.test/repos/acme/tpa-db/releases/latest"

// fetchRT is newRTFixture configured the way a fetch-only install is: no
// bundle path, the operator root with a baseline of 1, fetch enabled.
func newFetchFixture(t *testing.T, mutate func(*config.SecurityConfig)) *rtFixture {
	t.Helper()
	return newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPABundlePath = ""
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
		s.TPAFetchEnabled = true
		s.TPAFetchURL = fetchChannelURL
		if mutate != nil {
			mutate(s)
		}
	}})
}

// fetchedCandidate is a signed candidate in the shape the fetch adapter hands
// to the pipeline: Source fetch, host-only Path.
func fetchedCandidate(t *testing.T, key testKey, o bundleOpts) Candidate {
	t.Helper()
	c := signedCandidate(t, key, o)
	c.Source = BundleSourceFetch
	c.Path = "https://mirror.test"
	return c
}

// activationRecorder collects BundleActivation notifications.
type activationRecorder struct {
	mu   sync.Mutex
	rows []BundleActivation
}

func recordActivations(t *testing.T) *activationRecorder {
	t.Helper()
	r := &activationRecorder{}
	unsub := AddBundleActivationObserver(func(a BundleActivation) {
		r.mu.Lock()
		r.rows = append(r.rows, a)
		r.mu.Unlock()
	})
	t.Cleanup(unsub)
	return r
}

// all drains in-flight dispatches first so the answer is deterministic.
func (r *activationRecorder) all() []BundleActivation {
	drainBundleObserversForTest()
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]BundleActivation(nil), r.rows...)
}

func (r *activationRecorder) count() int { return len(r.all()) }

// countLogs counts the entries at level whose message contains substr.
func (f *rtFixture) countLogs(level zapcore.Level, substr string) int {
	n := 0
	for _, e := range f.logs.All() {
		if e.Level == level && strings.Contains(e.Message, substr) {
			n++
		}
	}
	return n
}

// setBundleClock pins bundleClock for one test.
func setBundleClock(t *testing.T, now func() time.Time) {
	t.Helper()
	prev := bundleClock
	bundleClock = now
	t.Cleanup(func() { bundleClock = prev })
}

// --- fake clock ---------------------------------------------------------------

// fakeClock is a RefreshClock whose timers fire only when the test says so.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the wall clock without firing any timer (a suspended laptop,
// a forward or backward clock jump).
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *fakeClock) NewTimer(d time.Duration) RefreshTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, ch: make(chan time.Time, 1), active: true, durations: []time.Duration{d}}
	c.timers = append(c.timers, t)
	return t
}

// loop returns the scheduler's own timer: the first one created.
func (c *fakeClock) loop(t *testing.T) *fakeTimer {
	t.Helper()
	var ft *fakeTimer
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if len(c.timers) == 0 {
			return false
		}
		ft = c.timers[0]
		return true
	}, 5*time.Second, time.Millisecond, "the scheduler never armed its timer")
	return ft
}

// deadlines returns the timers armed with exactly d (the 20s cycle deadline).
func (c *fakeClock) deadlines(d time.Duration) []*fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*fakeTimer
	for _, t := range c.timers[min(1, len(c.timers)):] {
		if t.firstDuration() == d {
			out = append(out, t)
		}
	}
	return out
}

type fakeTimer struct {
	clock     *fakeClock
	ch        chan time.Time
	mu        sync.Mutex
	active    bool
	durations []time.Duration // every arm: the creation duration, then each Reset
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	was := t.active
	t.active = false
	return was
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	was := t.active
	t.active = true
	t.durations = append(t.durations, d)
	// A real timer discards a stale, undelivered fire on Reset.
	select {
	case <-t.ch:
	default:
	}
	return was
}

// Fire delivers one tick, as the real timer would when its duration elapsed.
func (t *fakeTimer) Fire() {
	t.mu.Lock()
	t.active = false
	t.mu.Unlock()
	select {
	case t.ch <- t.clock.Now():
	default:
	}
}

func (t *fakeTimer) firstDuration() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.durations[0]
}

// last is the most recent arm duration.
func (t *fakeTimer) last() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.durations[len(t.durations)-1]
}

func (t *fakeTimer) arms() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.durations)
}

func (t *fakeTimer) isActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active
}
