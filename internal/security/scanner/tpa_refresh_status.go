package scanner

import (
	"net/url"
	"sync"
	"time"
)

// Refresh and re-protection status (Spec 087 FR-017, Spec 101 US3 tasks B15,
// B17). The vocabularies below are CLOSED: a surface may branch on them.

// Refresh trigger vocabulary (BundleRefreshStatus.LastTrigger).
const (
	// RefreshTriggerStartup is the construction-time ConfigureBundleFromConfig.
	RefreshTriggerStartup = "startup"
	// RefreshTriggerConfig is a config.reloaded re-apply.
	RefreshTriggerConfig = "config"
	// RefreshTriggerTimer is the refresher's periodic tick.
	RefreshTriggerTimer = "timer"
	// RefreshTriggerManual is `security bundle refresh` / POST .../bundle/refresh.
	RefreshTriggerManual = "manual"
)

// Refresh outcome vocabulary (BundleRefreshStatus.LastOutcome).
const (
	// RefreshOutcomeActivated: a new bundle was verified and activated.
	RefreshOutcomeActivated = "activated"
	// RefreshOutcomeUnchanged: the candidate equals the serving bundle, or was
	// already refused (negative cache); nothing was compiled.
	RefreshOutcomeUnchanged = "unchanged"
	// RefreshOutcomeNotModified: the channel answered 304.
	RefreshOutcomeNotModified = "not_modified"
	// RefreshOutcomeRejected: the pipeline refused the candidate; the detail
	// is in BundleInfo.LastRejection.
	RefreshOutcomeRejected = "rejected"
	// RefreshOutcomeFetchUnavailable: a non-verification failure (network,
	// timeout, HTTP status, rate limit). Never a last_rejection.
	RefreshOutcomeFetchUnavailable = "fetch_unavailable"
	// RefreshOutcomeChannelInvalid: the channel answered but is malformed
	// (zero or several candidate assets, missing sidecar asset, oversize JSON).
	RefreshOutcomeChannelInvalid = "channel_invalid"
	// RefreshOutcomeSuperseded: the fetch or trust settings changed while a
	// download was in flight, so its candidate was discarded (Δ30).
	RefreshOutcomeSuperseded = "superseded"
	// RefreshOutcomeDisabled: the fetch leg may not issue requests (B10); the
	// reason says why.
	RefreshOutcomeDisabled = "disabled"
)

// Refresh reason vocabulary (BundleRefreshStatus.LastReason). Fetch-unavailable
// reasons first, then the disabled reasons (tasks B6, B10).
const (
	RefreshReasonNetworkError = "network_error"
	RefreshReasonTimeout      = "timeout"
	RefreshReasonHTTPStatus   = "http_status"
	RefreshReasonRateLimited  = "rate_limited"

	RefreshReasonFetchOff         = "fetch_off"
	RefreshReasonNoURL            = "no_url"
	RefreshReasonInvalidURL       = "invalid_url"
	RefreshReasonNoTrustedSigner  = "no_trusted_signer"
	RefreshReasonStateUnavailable = "state_unavailable"
)

// Re-protection sweep state vocabulary (BundleReprotectStatus.State).
const (
	ReprotectStateIdle      = "idle"
	ReprotectStateRunning   = "running"
	ReprotectStateSuspended = "suspended"
	ReprotectStateFailed    = "failed"
)

// ReprotectReasonAutoBaselineScanOff is the suspended reason shown while
// security.auto_baseline_scan is off (tasks B17).
const ReprotectReasonAutoBaselineScanOff = "auto_baseline_scan_off"

// BundleRefreshStatus is the refresh-cycle status merged into BundleInfo at
// read time (B15). It carries the host only, never the URL (B12).
type BundleRefreshStatus struct {
	FetchEnabled bool `json:"fetch_enabled"`
	// FetchHost is the host of the configured fetch URL ("" when none).
	FetchHost       string `json:"fetch_host,omitempty"`
	IntervalSeconds int    `json:"interval_seconds"`
	// LastTrigger, LastOutcome and LastReason use the closed vocabularies above.
	LastTrigger         string     `json:"last_trigger,omitempty"`
	LastOutcome         string     `json:"last_outcome,omitempty"`
	LastReason          string     `json:"last_reason,omitempty"`
	LastAttemptAt       *time.Time `json:"last_attempt_at,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	NextCheckAt         *time.Time `json:"next_check_at,omitempty"`
	// FetchState and FetchReason are the fetch leg's own last answer (one of the
	// RefreshOutcome* values and its reason), kept apart from LastOutcome: a file
	// activation owns the merged cycle outcome but must not hide a fetch leg that
	// cannot run (B16 warning 3).
	FetchState  string `json:"fetch_state,omitempty"`
	FetchReason string `json:"fetch_reason,omitempty"`
}

// BundleReprotectStatus is the post-activation re-protection sweep status
// (Spec 101 FR-019, B17), set from the server through SetReprotectStatus.
type BundleReprotectStatus struct {
	// State is one of the ReprotectState* constants.
	State string `json:"state"`
	// Reason qualifies suspended and failed (for example
	// ReprotectReasonAutoBaselineScanOff).
	Reason          string     `json:"reason,omitempty"`
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"`
	ServersScanned  int        `json:"servers_scanned"`
	ServersWithHits int        `json:"servers_with_hits"`
}

// RefreshOutcomeClass is the coarse class of one refresh cycle handed to the
// refresh observer (SetBundleRefreshObserver). It is scanner's own enum;
// internal/telemetry must not import scanner, so internal/server maps it to
// telemetry.TPABundleRefreshClass (tasks B21).
type RefreshOutcomeClass string

const (
	// RefreshClassActivated: the cycle activated a bundle.
	RefreshClassActivated RefreshOutcomeClass = "activated"
	// RefreshClassRejected: the pipeline refused a candidate.
	RefreshClassRejected RefreshOutcomeClass = "rejected"
	// RefreshClassUnavailable: fetch_unavailable or channel_invalid.
	RefreshClassUnavailable RefreshOutcomeClass = "unavailable"
	// RefreshClassUnchanged: unchanged, not_modified, superseded or disabled.
	RefreshClassUnchanged RefreshOutcomeClass = "unchanged"
)

// SetReprotectStatus publishes the re-protection sweep status that BundleStatus
// merges into BundleInfo.Reprotect (tasks B15, B17). A zero status (empty
// State) clears it.
func SetReprotectStatus(status BundleReprotectStatus) {
	bundleRT.disp.setReprotect(status)
}

// bundleDisplay is the read-time half of BundleInfo (tasks B15): the refresh
// settings and status, last_checked_at, the stale threshold and the
// re-protection status. It lives beside histMu under its own mutex, never
// under bundleRT.mu, and is merged into the COPIES BundleStatus and
// statusFromSnapshot return, so a refresh cycle or a threshold edit never
// republishes the (check, info) pair a scan reads.
type bundleDisplay struct {
	mu sync.Mutex
	displayData
}

// displayData is the guarded state of bundleDisplay, a separate type so reset
// can zero it without touching the mutex.
type displayData struct {
	// known is true once a policy has been applied; the refresh block is
	// reported from then on.
	known bool
	// configured is true after the first configure of this process: the first
	// apply is trigger "startup", every later one "config" unless it is an
	// identical re-apply of the startup settings (see noteTrigger).
	configured bool
	// startupSig is the settings signature of that first apply.
	startupSig settingsSig

	staleAfterDays  int
	fetchEnabled    bool
	fetchHost       string
	intervalSeconds int
	// intervalOverride, when positive, is what intervalSeconds reports.
	intervalOverride int

	lastTrigger         string
	lastOutcome         string
	lastReason          string
	fetchState          string
	fetchReason         string
	lastAttemptAt       time.Time
	consecutiveFailures int
	nextCheckAt         time.Time

	lastCheckedAt time.Time
	reprotect     *BundleReprotectStatus
}

// reset returns the display to its zero state (a fresh process).
func (d *bundleDisplay) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.displayData = displayData{}
}

// applySettings records the display settings of policy. It runs on every apply,
// before the idempotence short-circuit, so a threshold edit is visible even
// when the pipeline itself has nothing to redo.
func (d *bundleDisplay) applySettings(p *bundlePolicy) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.known = true
	d.staleAfterDays = p.StaleAfterDays
	d.fetchEnabled = p.FetchEnabled
	d.fetchHost = fetchHostOf(p.FetchURL)
	d.intervalSeconds = int(p.RefreshInterval / time.Second)
	if d.intervalOverride > 0 {
		d.intervalSeconds = d.intervalOverride
	}
}

// setIntervalOverride makes the reported interval v seconds regardless of the
// configuration (the refresher's IntervalOverride test seam); 0 removes it.
func (d *bundleDisplay) setIntervalOverride(v int) {
	d.mu.Lock()
	d.intervalOverride = v
	if v > 0 {
		d.intervalSeconds = v
	}
	d.mu.Unlock()
}

// settingsSig is what a configure applied, as far as the trigger label cares.
type settingsSig struct {
	fingerprint string
	staleDays   int
	interval    time.Duration
}

func sigOf(p *bundlePolicy) settingsSig {
	return settingsSig{fingerprint: p.Fingerprint, staleDays: p.StaleAfterDays, interval: p.RefreshInterval}
}

// noteTrigger records what started the current apply. An empty trigger means a
// configure: "startup" the first time in this process, "config" afterwards. The
// scanner service re-applies the identical config right after the
// construction-time configure; that is still the startup, not a reload, so an
// identical re-apply keeps "startup" until a cycle or a real change replaces it.
func (d *bundleDisplay) noteTrigger(trigger string, p *bundlePolicy) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if trigger == "" {
		sig := sigOf(p)
		switch {
		case !d.configured:
			trigger = RefreshTriggerStartup
			d.configured = true
			d.startupSig = sig
		case d.lastTrigger == RefreshTriggerStartup && sig == d.startupSig:
			trigger = RefreshTriggerStartup
		default:
			trigger = RefreshTriggerConfig
		}
	}
	d.lastTrigger = trigger
}

func (d *bundleDisplay) markChecked(t time.Time) {
	d.mu.Lock()
	d.lastCheckedAt = t
	d.mu.Unlock()
}

func (d *bundleDisplay) setReprotect(s BundleReprotectStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s.State == "" {
		d.reprotect = nil
		return
	}
	cp := s
	d.reprotect = &cp
}

// cycleRecord is one finished refresh cycle.
type cycleRecord struct {
	Trigger, Outcome, Reason string
	At                       time.Time
	// FetchSet is true when the fetch leg ran (or was known unable to run) this
	// cycle; FetchState and FetchReason are then its own answer.
	FetchSet                bool
	FetchState, FetchReason string
	// Failures is how the consecutive-failure counter moves.
	Failures failureMove
}

type failureMove int

const (
	failuresKeep failureMove = iota
	failuresIncrement
	failuresReset
)

func (d *bundleDisplay) recordCycle(r cycleRecord) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastTrigger, d.lastOutcome, d.lastReason, d.lastAttemptAt = r.Trigger, r.Outcome, r.Reason, r.At
	if r.FetchSet {
		d.fetchState, d.fetchReason = r.FetchState, r.FetchReason
	}
	switch r.Failures {
	case failuresIncrement:
		d.consecutiveFailures++
	case failuresReset:
		d.consecutiveFailures = 0
	}
}

func (d *bundleDisplay) setNextCheck(t time.Time) {
	d.mu.Lock()
	d.nextCheckAt = t
	d.mu.Unlock()
}

// merge adds the read-time fields to info, a copy the caller owns.
func (d *bundleDisplay) merge(info *BundleInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.lastCheckedAt.IsZero() {
		t := d.lastCheckedAt
		info.LastCheckedAt = &t
	}
	if d.known {
		info.Stale, info.StaleAfterDays = false, nil
		if d.staleAfterDays > 0 {
			v := d.staleAfterDays
			info.StaleAfterDays = &v
			// A future generated_at reads as age 0 (stalenessDays) and so is
			// never stale.
			info.Stale = info.StalenessDays != nil && *info.StalenessDays >= d.staleAfterDays
		}
		r := &BundleRefreshStatus{
			FetchEnabled:        d.fetchEnabled,
			FetchHost:           d.fetchHost,
			IntervalSeconds:     d.intervalSeconds,
			LastTrigger:         d.lastTrigger,
			LastOutcome:         d.lastOutcome,
			LastReason:          d.lastReason,
			FetchState:          d.fetchState,
			FetchReason:         d.fetchReason,
			ConsecutiveFailures: d.consecutiveFailures,
		}
		if !d.lastAttemptAt.IsZero() {
			t := d.lastAttemptAt
			r.LastAttemptAt = &t
		}
		if !d.nextCheckAt.IsZero() {
			t := d.nextCheckAt
			r.NextCheckAt = &t
		}
		info.Refresh = r
	}
	if d.reprotect != nil {
		cp := *d.reprotect
		info.Reprotect = &cp
	}
}

// fetchHostOf is the host of a configured fetch URL, "" when it is empty or
// unparsable. It is the only part of the URL any status, log or metric carries
// (B12).
func fetchHostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
