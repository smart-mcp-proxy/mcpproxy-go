package scanner

import "time"

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
// merges into BundleInfo.Reprotect (tasks B15, B17).
//
// STUB (Spec 101 US3 wave 0): no-op until T330.
func SetReprotectStatus(status BundleReprotectStatus) {
	_ = status
}
