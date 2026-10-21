package scanner

import (
	"context"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The refresh scheduler (Spec 087 FR-003, FR-004, FR-009 to FR-015; Spec 101
// US3 tasks B6 to B8, B19, B20). One BundleRefresher per process runs a cycle
// per interval: the network-free file leg, then the optional fetch leg. Every
// trigger (timer tick, manual refresh, config nudge) that arrives during a
// cycle joins it, so there is one download and one activation per distinct
// candidate.
//
// Locking rules this file keeps:
//   - network I/O never happens under bundleRT.mu (the fetch adapter takes no
//     lock; the pipeline is entered only through applyPolicy/offerCandidate);
//   - the refresher never reads rt.state or rt.active: it reads the snapshot
//     publishLocked leaves in bundleFacts;
//   - the refresher never takes Runtime.mu: live configuration arrives through
//     ConfigProvider and Nudge.

const (
	// refreshCycleDeadline is the total time budget of one cycle; the CLI's hard
	// limit is 30s (B8).
	refreshCycleDeadline = 20 * time.Second
	// refreshFirstMin and refreshFirstMax bound the first cycle after Start.
	refreshFirstMin = 30 * time.Second
	refreshFirstMax = 5 * time.Minute
	// refreshJitter is the +/- fraction applied to the interval.
	refreshJitter = 0.10
	// refreshRetryCap caps how far a rate limit may push the next attempt.
	refreshRetryCap = 24 * time.Hour
	// refreshLogEvery is how often a persisting failure is logged again.
	refreshLogEvery = 24 * time.Hour
)

// RefreshClock is the time seam of the scheduler. Tests inject a fake whose
// timers fire on demand; production uses the real clock (nil Clock).
type RefreshClock interface {
	Now() time.Time
	// NewTimer returns a timer that fires once after d.
	NewTimer(d time.Duration) RefreshTimer
}

// RefreshTimer is the timer half of RefreshClock, shaped like *time.Timer.
type RefreshTimer interface {
	// C delivers the fire time.
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTimer(d time.Duration) RefreshTimer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time        { return r.t.C }
func (r realTimer) Stop() bool                 { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

// BundleRefresherOptions are the construction-time seams of a BundleRefresher.
// Every field is optional; the zero value selects the production defaults.
type BundleRefresherOptions struct {
	// Clock replaces the real clock and timers.
	Clock RefreshClock
	// Rand returns a uniform value in [0,1) for jitter.
	Rand func() float64
	// HTTPClient, when set, is used as is; otherwise one is built from
	// Transport and the egress rules.
	HTTPClient *http.Client
	// Transport replaces the base transport of the built client.
	Transport http.RoundTripper
	// Resolver replaces DNS for the egress checks.
	Resolver FetchResolver
	// ConfigProvider returns the live security config each cycle. The
	// refresher never takes Runtime.mu to read it.
	ConfigProvider func() *config.SecurityConfig
	// Logger is the injected logger (nil => no-op).
	Logger *zap.Logger
	// Version is the mcpproxy version for the User-Agent header.
	Version string
	// IntervalOverride is a test seam that replaces the configured interval
	// and bypasses the 1h floor; zero means "use the config".
	IntervalOverride time.Duration
}

// wakeReason says why the loop was woken outside its timer.
type wakeReason int

const (
	wakeNone wakeReason = iota
	// wakeManual: a manual cycle finished; the daily check is satisfied, so the
	// timer is re-armed.
	wakeManual
	// wakeInterval: the interval changed; re-arm at the new cadence.
	wakeInterval
	// wakeSoon: fetch or trust settings changed with the fetch on; look again
	// within the first-cycle window instead of waiting out the interval.
	wakeSoon
)

// cycleCall is one in-flight cycle that triggers join.
type cycleCall struct{ done chan struct{} }

// BundleRefresher owns the refresh lifecycle. Start/Stop follow the
// telemetry.Service pattern: Stop cancels and joins, a Start after Stop is a
// no-op.
type BundleRefresher struct {
	opts  BundleRefresherOptions
	clock RefreshClock

	mu      sync.Mutex
	started bool
	stopped bool
	running bool
	base    context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// cycle is the in-flight cycle, nil between cycles.
	cycle *cycleCall

	// gen is bumped on any change to the fetch settings or to an input of the
	// policy fingerprint; fp is the fingerprint last seen. A download finished
	// under an older generation is discarded as superseded (B8).
	gen uint64
	fp  string
	// nudged is the config the last Nudge delivered, the fallback when there is
	// no ConfigProvider.
	nudged   *config.SecurityConfig
	interval time.Duration
	pending  wakeReason
	wake     chan struct{}
	etags    map[string]string
	retryAt  time.Time

	// lastLogKey and lastLogAt deduplicate failure logs per (outcome, reason).
	lastLogKey string
	lastLogAt  time.Time
}

// NewBundleRefresher builds a refresher; it does nothing until Start.
func NewBundleRefresher(opts BundleRefresherOptions) *BundleRefresher {
	r := &BundleRefresher{
		opts:  opts,
		clock: opts.Clock,
		wake:  make(chan struct{}, 1),
		etags: map[string]string{},
	}
	if r.clock == nil {
		r.clock = realClock{}
	}
	if r.opts.Version == "" {
		r.opts.Version = "dev"
	}
	return r
}

// config returns the live security config: the provider's, else the last
// nudged one, else nil. It never holds r.mu across the provider call.
func (r *BundleRefresher) config() *config.SecurityConfig {
	if r.opts.ConfigProvider != nil {
		if sec := r.opts.ConfigProvider(); sec != nil {
			return sec
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nudged
}

func (r *BundleRefresher) random() float64 {
	if r.opts.Rand != nil {
		return r.opts.Rand()
	}
	return rand.Float64()
}

func (r *BundleRefresher) logger(fallback *zap.Logger) *zap.Logger {
	if r.opts.Logger != nil {
		return r.opts.Logger
	}
	return fallback
}

// intervalFor is the effective cadence for sec.
func (r *BundleRefresher) intervalFor(sec *config.SecurityConfig) time.Duration {
	if r.opts.IntervalOverride > 0 {
		return r.opts.IntervalOverride
	}
	return clampRefreshInterval(sec.EffectiveTPARefreshInterval())
}

// fingerprintOf is the policy fingerprint of sec ("" for nil).
func fingerprintOf(sec *config.SecurityConfig) string {
	if sec == nil {
		return ""
	}
	return policyFromConfig(sec).Fingerprint
}

// jitter returns d spread uniformly over +/- refreshJitter.
func (r *BundleRefresher) jitter(d time.Duration) time.Duration {
	f := 1 - refreshJitter + 2*refreshJitter*r.random()
	return time.Duration(float64(d) * f)
}

// firstDelay is the delay before the first cycle after Start (a uniform point
// in [30s, 5m], so a fleet restarted together does not stampede the channel),
// and after a settings change that needs an early look. With an interval
// override (tests) the first cycle follows the override instead.
func (r *BundleRefresher) firstDelay() time.Duration {
	if r.opts.IntervalOverride > 0 {
		return r.jitter(r.opts.IntervalOverride)
	}
	return refreshFirstMin + time.Duration(r.random()*float64(refreshFirstMax-refreshFirstMin))
}

// nextDelay is the delay until the next cycle: the interval +/- 10%, pushed out
// to a pending rate-limit reset (capped at 24h) when that is later.
func (r *BundleRefresher) nextDelay() time.Duration {
	r.mu.Lock()
	interval, retryAt := r.interval, r.retryAt
	r.mu.Unlock()
	d := r.jitter(interval)
	if !retryAt.IsZero() {
		if until := retryAt.Sub(r.clock.Now()); until > 0 {
			if until > refreshRetryCap {
				until = refreshRetryCap
			}
			if until > d {
				d = until
			}
		}
	}
	return d
}

// Start begins the cycle loop, bound to ctx. A second Start, or a Start after
// Stop, is a no-op. The loop timer is created before Start returns.
func (r *BundleRefresher) Start(ctx context.Context) {
	sec := r.config()

	r.mu.Lock()
	if r.started || r.stopped {
		r.mu.Unlock()
		return
	}
	r.started, r.running = true, true
	r.base, r.cancel = context.WithCancel(ctx)
	r.interval = r.intervalFor(sec)
	r.fp = fingerprintOf(sec)
	base := r.base
	r.wg.Add(1)
	r.mu.Unlock()

	if r.opts.IntervalOverride > 0 {
		bundleRT.disp.setIntervalOverride(int(r.opts.IntervalOverride / time.Second))
	}
	delay := r.firstDelay()
	timer := r.clock.NewTimer(delay)
	bundleRT.disp.setNextCheck(r.clock.Now().Add(delay))
	go r.loop(base, timer)
}

// Stop cancels any in-flight cycle and returns only after the loop goroutine
// and every cycle goroutine exited. Idempotent.
func (r *BundleRefresher) Stop() {
	r.mu.Lock()
	r.stopped = true
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}

// Running reports whether the loop goroutine is alive.
func (r *BundleRefresher) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running && !r.stopped
}

// Nudge tells the refresher the security config changed: it re-reads the
// fetch and trust settings, bumps the generation (superseding an in-flight
// download whose inputs changed) and re-arms the timer. Non-blocking; a Nudge
// after Stop began is a no-op.
func (r *BundleRefresher) Nudge(sec *config.SecurityConfig) {
	if sec == nil {
		return
	}
	policy := policyFromConfig(sec)
	bundleRT.disp.applySettings(policy)

	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.nudged = sec
	reason := wakeNone
	if policy.Fingerprint != r.fp {
		r.fp = policy.Fingerprint
		r.gen++
		r.etags = map[string]string{}
		r.retryAt = time.Time{}
		if policy.FetchEnabled {
			reason = wakeSoon
		}
	}
	if iv := r.intervalFor(sec); iv != r.interval {
		r.interval = iv
		if reason < wakeInterval {
			reason = wakeInterval
		}
	}
	r.mu.Unlock()
	if reason != wakeNone {
		r.signal(reason)
	}
}

// signal records why the loop should re-arm and wakes it, without blocking.
func (r *BundleRefresher) signal(reason wakeReason) {
	r.mu.Lock()
	if reason > r.pending {
		r.pending = reason
	}
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *BundleRefresher) takePending() wakeReason {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pending
	r.pending = wakeNone
	return p
}

// arm re-arms the loop timer and publishes the next check time.
func (r *BundleRefresher) arm(t RefreshTimer, d time.Duration) {
	t.Stop()
	t.Reset(d)
	bundleRT.disp.setNextCheck(r.clock.Now().Add(d))
}

// loop is the scheduler goroutine: one timer, re-armed after each cycle
// completes. A suspended machine or a forward clock jump therefore yields ONE
// cycle on wake, never a catch-up burst, and the timer counts durations, not
// wall-clock instants, so a backward jump cannot wedge it.
func (r *BundleRefresher) loop(ctx context.Context, timer RefreshTimer) {
	defer r.wg.Done()
	defer func() {
		timer.Stop()
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C():
			c := r.joinOrStart(RefreshTriggerTimer)
			if c == nil {
				return
			}
			select {
			case <-c.cycle.done:
			case <-ctx.Done():
				return
			}
			if r.takePending() == wakeSoon {
				r.arm(timer, r.firstDelay())
			} else {
				r.arm(timer, r.nextDelay())
			}
		case <-r.wake:
			switch r.takePending() {
			case wakeSoon:
				r.arm(timer, r.firstDelay())
			case wakeInterval, wakeManual:
				r.arm(timer, r.nextDelay())
			}
		}
	}
}

// startedCycle is a joined or newly started cycle.
type startedCycle struct {
	cycle   *cycleCall
	started bool
}

// joinOrStart joins the in-flight cycle or starts one with the given trigger.
// It returns nil when the refresher is not running.
func (r *BundleRefresher) joinOrStart(trigger string) *startedCycle {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.stopped {
		return nil
	}
	if r.cycle != nil {
		return &startedCycle{cycle: r.cycle}
	}
	c := &cycleCall{done: make(chan struct{})}
	r.cycle = c
	r.wg.Add(1)
	go r.executeCycle(r.base, c, trigger)
	return &startedCycle{cycle: c, started: true}
}

func (r *BundleRefresher) executeCycle(ctx context.Context, c *cycleCall, trigger string) {
	defer r.wg.Done()
	defer func() {
		r.mu.Lock()
		r.cycle = nil
		r.mu.Unlock()
		close(c.done)
	}()
	r.doCycle(ctx, trigger)
}

// Refresh runs one cycle now (trigger "manual"), joining an in-flight cycle if
// there is one, and returns the resulting status. Without a running loop it is
// ReloadBundle.
func (r *BundleRefresher) Refresh(ctx context.Context, logger *zap.Logger) BundleInfo {
	c := r.joinOrStart(RefreshTriggerManual)
	if c == nil {
		return ReloadBundle(r.logger(logger))
	}
	select {
	case <-c.cycle.done:
	case <-ctx.Done():
		return BundleStatus()
	}
	if c.started {
		// A manual cycle satisfies the daily check: push the timer out.
		r.signal(wakeManual)
	}
	return BundleStatus()
}

// --- one cycle ----------------------------------------------------------------

// legResult is what one leg of a cycle reports.
type legResult struct {
	Outcome string
	Reason  string
	Detail  string
}

// doCycle runs the file leg, then the fetch leg, and records the result. A
// cycle cancelled by Stop reports nothing.
func (r *BundleRefresher) doCycle(ctx context.Context, trigger string) {
	raw := r.logger(zap.NewNop())
	log := raw.Named(scannerLogName)
	sec := r.config()
	startGen := r.currentGeneration()

	// The 20s total budget runs on the injected clock, so a trickled body is
	// cut deterministically in tests. Its cancellation cause is how
	// fetchCandidate tells a timeout from a Stop.
	cctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	deadline := r.clock.NewTimer(refreshCycleDeadline)
	defer deadline.Stop()
	go func() {
		select {
		case <-deadline.C():
			cancel(errCycleDeadline)
		case <-cctx.Done():
		}
	}()

	// File leg: re-apply the live policy (idempotent) and re-read the
	// configured file. Network-free and always on.
	var (
		policy *bundlePolicy
		file   runOutcome
	)
	if sec != nil {
		policy = policyFromConfig(sec)
		file = applyPolicy(policy, raw, trigger)
	} else {
		_, file = reloadLastPolicy(raw, trigger)
	}

	// Fetch leg: only when B10 allows it; fetchCandidate decides.
	fetch := r.fetchLeg(cctx, sec, policy, startGen, log)
	if ctx.Err() != nil {
		return // Stop: nothing is recorded
	}

	outcome, reason := mergeLegs(file, fetch)
	rec := cycleRecord{Trigger: trigger, Outcome: outcome, Reason: reason, At: r.clock.Now()}
	switch fetch.Outcome {
	case RefreshOutcomeFetchUnavailable, RefreshOutcomeChannelInvalid:
		rec.Failures = failuresIncrement
	case RefreshOutcomeSuperseded:
		rec.Failures = failuresKeep
	default:
		rec.Failures = failuresReset
	}
	bundleRT.disp.recordCycle(rec)
	r.logOutcome(log, outcome, reason, fetch, policy)
	notifyRefreshObserver(classOfOutcome(outcome))
}

// mergeLegs picks the one outcome a cycle reports: an activation beats a
// refusal, which beats whatever the fetch leg said, which beats "disabled".
func mergeLegs(file runOutcome, fetch legResult) (outcome, reason string) {
	switch {
	case file.Activated || fetch.Outcome == RefreshOutcomeActivated:
		return RefreshOutcomeActivated, ""
	case file.Rejection != nil || fetch.Outcome == RefreshOutcomeRejected:
		return RefreshOutcomeRejected, ""
	case fetch.Outcome == "":
		return RefreshOutcomeUnchanged, ""
	}
	return fetch.Outcome, fetch.Reason
}

// classOfOutcome maps a cycle outcome to the observer's coarse class.
func classOfOutcome(outcome string) RefreshOutcomeClass {
	switch outcome {
	case RefreshOutcomeActivated:
		return RefreshClassActivated
	case RefreshOutcomeRejected:
		return RefreshClassRejected
	case RefreshOutcomeFetchUnavailable, RefreshOutcomeChannelInvalid:
		return RefreshClassUnavailable
	}
	return RefreshClassUnchanged
}

func (r *BundleRefresher) currentGeneration() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}

// stillCurrent reports whether the settings a download started under are still
// the live ones: same generation and the same policy fingerprint (which covers
// the fetch settings, the active root, the operator signers, baseline and
// epoch, require_signed_bundle and the bundle path). Otherwise the candidate
// is discarded as superseded (B8).
func (r *BundleRefresher) stillCurrent(startGen uint64, policy *bundlePolicy) bool {
	return r.currentGeneration() == startGen && fingerprintOf(r.config()) == policy.Fingerprint
}

func (r *BundleRefresher) etagFor(url string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.etags[url]
}

func (r *BundleRefresher) rememberETag(url, etag string) {
	if etag == "" {
		return
	}
	r.mu.Lock()
	r.etags[url] = etag
	r.mu.Unlock()
}

func (r *BundleRefresher) setRetryAt(t time.Time) {
	r.mu.Lock()
	r.retryAt = t
	r.mu.Unlock()
}

// fetchLeg runs the optional network leg. The HTTP exchange happens with no
// lock held; the candidate reaches the pipeline only through offerCandidate.
func (r *BundleRefresher) fetchLeg(ctx context.Context, sec *config.SecurityConfig, policy *bundlePolicy, startGen uint64, log *zap.Logger) legResult {
	if sec == nil || policy == nil {
		return legResult{Outcome: RefreshOutcomeDisabled, Reason: RefreshReasonFetchOff}
	}
	client := r.opts.HTTPClient
	if client == nil && r.opts.Transport != nil {
		client = &http.Client{Transport: r.opts.Transport}
	}
	settings := fetchSettings{
		Enabled:          policy.FetchEnabled,
		URL:              policy.FetchURL,
		Version:          r.opts.Version,
		Policy:           policy,
		StateUnavailable: publishedStateUnavailable(),
		Watermark:        publishedWatermark,
		ETag:             r.etagFor(policy.FetchURL),
		Client:           client,
		Resolver:         r.opts.Resolver,
		Now:              r.clock.Now,
		Log:              log,
	}
	res := fetchCandidate(ctx, settings)
	if !res.RetryAt.IsZero() {
		r.setRetryAt(res.RetryAt)
	} else if res.Outcome != RefreshOutcomeDisabled {
		r.setRetryAt(time.Time{})
	}

	leg := legResult{Outcome: res.Outcome, Reason: res.Reason, Detail: res.Detail}
	if res.Outcome != fetchOutcomeCandidate {
		if res.Outcome == RefreshOutcomeUnchanged || res.Outcome == RefreshOutcomeNotModified {
			r.rememberETag(policy.FetchURL, res.ETag)
		}
		return leg
	}

	if ctx.Err() != nil && context.Cause(ctx) != errCycleDeadline {
		return legResult{} // Stop: discarded without a trace
	}
	if !r.stillCurrent(startGen, policy) {
		log.Info("a fetched TPA bundle was discarded: the fetch or trust settings changed while it downloaded",
			zap.String("host", fetchHostOf(policy.FetchURL)))
		return legResult{Outcome: RefreshOutcomeSuperseded}
	}
	_, off := offerCandidate(*res.Candidate, r.logger(zap.NewNop()))
	if off.Outcome != RefreshOutcomeRejected || off.Rejection.Reason != RejectStateCommitFailed {
		// A transient commit failure must not freeze the channel behind a 304.
		r.rememberETag(policy.FetchURL, res.ETag)
	}
	return legResult{Outcome: off.Outcome}
}

// logOutcome logs a failing or misconfigured fetch leg, deduplicated on
// (outcome, reason): when it changes, or once per 24h while it persists. The
// line carries the host only (B12).
func (r *BundleRefresher) logOutcome(log *zap.Logger, outcome, reason string, fetch legResult, policy *bundlePolicy) {
	key := ""
	switch outcome {
	case RefreshOutcomeFetchUnavailable, RefreshOutcomeChannelInvalid:
		key = outcome + "|" + reason
	case RefreshOutcomeDisabled:
		switch reason {
		case RefreshReasonNoURL, RefreshReasonInvalidURL, RefreshReasonNoTrustedSigner, RefreshReasonStateUnavailable:
			if policy != nil && policy.FetchEnabled {
				key = outcome + "|" + reason
			}
		}
	}

	now := r.clock.Now()
	r.mu.Lock()
	repeat := key != "" && key == r.lastLogKey && now.Sub(r.lastLogAt) < refreshLogEvery
	if !repeat {
		r.lastLogKey = key
		if key != "" {
			r.lastLogAt = now
		}
	}
	r.mu.Unlock()
	if key == "" || repeat {
		return
	}

	host := ""
	if policy != nil {
		host = fetchHostOf(policy.FetchURL)
	}
	fields := []zap.Field{zap.String("outcome", outcome), zap.String("reason", reason), zap.String("host", host)}
	if fetch.Detail != "" {
		fields = append(fields, zap.String("detail", fetch.Detail))
	}
	if outcome == RefreshOutcomeDisabled {
		log.Warn("TPA bundle fetch is enabled but cannot run", fields...)
		return
	}
	log.Warn("TPA bundle fetch failed; keeping the last-known-good corpus", fields...)
}

var (
	refresherSlotMu sync.RWMutex
	refresherSlot   *BundleRefresher
)

// SetBundleRefresher installs r as the process's refresher, the one
// RefreshBundle consults. nil clears the slot.
func SetBundleRefresher(r *BundleRefresher) {
	refresherSlotMu.Lock()
	defer refresherSlotMu.Unlock()
	refresherSlot = r
}

// CurrentBundleRefresher returns the installed refresher, or nil. The runtime
// uses it on Close to clear the slot only if it still points at its own
// instance.
func CurrentBundleRefresher() *BundleRefresher {
	refresherSlotMu.RLock()
	defer refresherSlotMu.RUnlock()
	return refresherSlot
}

// RefreshBundle is the manual refresh (`security bundle refresh`, B20): one
// joined cycle (file leg plus fetch leg), trigger "manual". When no running
// refresher is installed it falls back to ReloadBundle, so a manual refresh
// still picks up a dropped file.
func RefreshBundle(ctx context.Context, logger *zap.Logger) BundleInfo {
	if r := CurrentBundleRefresher(); r != nil && r.Running() {
		return r.Refresh(ctx, logger)
	}
	return ReloadBundle(logger)
}
