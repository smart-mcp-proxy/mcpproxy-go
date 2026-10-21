package scanner

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Bundle source labels reported by BundleInfo.Source.
const (
	// BundleSourceEmbedded is the corpus compiled into this build
	// (bundled/scanner-bundle.json).
	BundleSourceEmbedded = "embedded"
	// BundleSourceFile is a corpus read from the configured filesystem path
	// (security.tpa_bundle_path / MCPPROXY_TPA_BUNDLE_PATH).
	BundleSourceFile = "file"
	// BundleSourceFetch is a corpus fetched from the configured channel
	// (security.tpa_fetch_url, Spec 087 / Spec 101 US3). It reaches the same
	// verifier as a file drop; only the Candidate's origin differs.
	BundleSourceFetch = "fetch"
)

// BundleInfo is the operator-facing status of the TPA signature corpus the
// scanner is actually running (spec 086 FR-019, GH #938 finding 2). Before this
// existed there was no supported way to answer "which signatures is my proxy
// running, and how old are they?" — the bundle was embed-only and its single
// load report went to an unconfigured global logger.
type BundleInfo struct {
	// Source is BundleSourceEmbedded, BundleSourceFile or BundleSourceFetch.
	Source string `json:"source"`
	// Path is the configured filesystem path, when Source is "file".
	Path string `json:"path,omitempty"`
	// BundleVersion / SchemaVersion are the corpus's own declared versions.
	BundleVersion string `json:"bundle_version,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty"`
	// SignatureCount is the bundle's self-declared signature count.
	SignatureCount int `json:"signature_count"`
	// RunnableRules is how many rules are LIVE in the offline tier; SkippedRules
	// and DeclaredSkipped are the not-runnable / bundle-declared-skipped
	// remainder (FR-006/FR-007) — un-evaluated coverage, never clean coverage.
	RunnableRules   int `json:"runnable_rules"`
	SkippedRules    int `json:"skipped_rules"`
	DeclaredSkipped int `json:"declared_skipped"`
	// GeneratedAt is the corpus freshness stamp. It is the bundle's own
	// `generated_at` key when present; for a file-sourced bundle without one it
	// falls back to the file's modification time, so a years-stale corpus is
	// distinguishable from a fresh export.
	GeneratedAt string `json:"generated_at,omitempty"`
	// Fingerprint is the first 12 hex chars of the SHA-256 of the corpus bytes:
	// a stable identity an operator (or automation) can compare across hosts
	// without shipping the whole file around.
	Fingerprint string `json:"fingerprint,omitempty"`
	// LoadedAt is when this corpus became active in this process.
	LoadedAt time.Time `json:"loaded_at"`
	// LoadError is the reason the LAST configured-path load failed, if any. The
	// previously active (or embedded) corpus stays live — a bundle problem must
	// never leave the scanner with no signatures — but the failure is surfaced
	// here rather than swallowed.
	LoadError string `json:"load_error,omitempty"`

	// --- Spec 101 FR-020 (contracts/bundle-status-surface.md) ---

	// FullDigest is the complete lowercase-hex SHA-256 of the bundle bytes —
	// the ONLY value rollback accepts (FR-010). Fingerprint stays the 12-hex
	// human label beside it.
	FullDigest string `json:"full_digest,omitempty"`
	// Sequence is the manifest sequence as published (decimal string), "" when
	// the bundle carries none.
	Sequence string `json:"sequence,omitempty"`
	// Authority is the signing root serving this bundle: "publisher" (the
	// embedded default, or verified against a publisher key), "operator", or ""
	// for an unsigned external bundle.
	Authority string `json:"authority,omitempty"`
	// TrustEpoch is the epoch the bundle was published under.
	TrustEpoch uint64 `json:"trust_epoch"`
	// SignatureVerified reports whether the active bundle's detached signature
	// verified. Always false for the embedded default (it is binary-trusted).
	SignatureVerified bool `json:"signature_verified"`
	// StalenessDays is the whole days since GeneratedAt (nil when unknown).
	StalenessDays *int `json:"staleness_days,omitempty"`
	// CoverageDegraded suspends scan-mode auto-approval; DegradedReason says
	// why (one of the Degraded* constants).
	CoverageDegraded bool   `json:"coverage_degraded"`
	DegradedReason   string `json:"degraded_reason,omitempty"`
	// Pinned is true while an operator rollback pin governs activation.
	Pinned bool `json:"pinned,omitempty"`
	// Rollback is true when the active bundle was activated by an explicit
	// rollback.
	Rollback bool `json:"rollback,omitempty"`
	// LastRejection is the most recent refused candidate. It is retained
	// across a later successful load so "what did it refuse, and when"
	// survives.
	LastRejection *BundleRejection `json:"last_rejection,omitempty"`

	// --- Spec 087 / Spec 101 US3 freshness fields (tasks B15) ---
	//
	// All pointer or omitempty so a status that never refreshed reads exactly
	// as before.

	// LastCheckedAt is the end of the most recent pipeline run, including
	// no-op runs the idempotence short-circuit skipped.
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	// LastActivatedAt is when the serving external bundle was activated; nil
	// for the embedded default.
	LastActivatedAt *time.Time `json:"last_activated_at,omitempty"`
	// Stale and StaleAfterDays are computed at read time next to
	// StalenessDays; a future generated_at is never stale. StaleAfterDays nil
	// means the flag is not evaluated.
	Stale          bool `json:"stale,omitempty"`
	StaleAfterDays *int `json:"stale_after_days,omitempty"`
	// Refresh is the refresh-cycle status (nil before any cycle or setting is
	// known). Reprotect is the post-activation re-protection sweep status,
	// set from the server through SetReprotectStatus.
	Refresh   *BundleRefreshStatus   `json:"refresh,omitempty"`
	Reprotect *BundleReprotectStatus `json:"reprotect,omitempty"`
}

// BundleRejection is one refused candidate (FR-020).
type BundleRejection struct {
	// Reason is one of the Reject* constants; the only field to branch on.
	Reason string `json:"reason"`
	// Detail is a short human string (which ceiling, which key id, which
	// epoch, the bundlesig contract code). Never parsed.
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// activeBundle is the process-wide corpus in use. The serialized runtime
// (tpa_bundle_runtime.go) replaces it wholesale at the end of each run; readers
// take a snapshot under RLock so a hot-reload can never hand a scan a
// half-swapped corpus, and (check, info) are always published together.
var (
	activeBundleMu    sync.RWMutex
	activeBundleCheck *BundleCheck
	activeBundleInfo  BundleInfo
)

// ConfigureBundle installs the TPA signature corpus the scanner will run,
// reading it from path when non-empty and falling back to the embedded default
// otherwise (spec 086 FR-019: the path MUST NOT be hardcoded). It is a thin
// wrapper over the serialized runtime with an otherwise default policy and NO
// environment override: the path is explicit. Production code configures the
// full trust policy through ConfigureBundleFromConfig.
//
// Failure policy (fail-closed, never fail-empty): if the configured file cannot
// be read, verified, parsed, version-checked, or compiled, the currently active
// corpus (or the embedded default on first call) stays live and the reason is
// recorded in BundleInfo.LoadError and BundleInfo.LastRejection AND logged
// through the INJECTED logger.
func ConfigureBundle(path string, logger *zap.Logger) {
	configureWithPolicy(policyFor(&config.SecurityConfig{TPABundlePath: path}, path), logger)
}

// bundleClock is the clock of the bundle runtime and of the read-time status
// fields (staleness, last_checked_at). It is a variable only so tests can
// inject a fixed time.
var bundleClock = time.Now

// BundleStatus returns a snapshot of the active corpus for the operator-facing
// surfaces (REST /api/v1/security/overview, `mcpproxy security overview`).
// It lazily installs the embedded default so a caller that never configured a
// bundle still gets a truthful answer instead of a zero value.
func BundleStatus() BundleInfo {
	_, info := activeBundleSnapshot()
	info.StalenessDays = stalenessDays(info.GeneratedAt, time.Now())
	return info
}

// statusFromSnapshot is BundleStatus without the lazy install, for callers that
// already hold bundleRT.mu (the lazy install would deadlock on it).
func statusFromSnapshot() BundleInfo {
	_, info := snapshotBundle()
	info.StalenessDays = stalenessDays(info.GeneratedAt, time.Now())
	return info
}

// stalenessDays is the whole days between generatedAt and now, nil when the
// stamp is absent or unparseable. A stamp in the future reads as 0.
func stalenessDays(generatedAt string, now time.Time) *int {
	if generatedAt == "" {
		return nil
	}
	var t time.Time
	var err error
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err = time.Parse(layout, generatedAt); err == nil {
			break
		}
	}
	if err != nil {
		return nil
	}
	days := int(now.Sub(t).Hours() / 24)
	if days < 0 {
		days = 0
	}
	return &days
}

// logBundle emits the one status report an operator can grep for.
func logBundle(log *zap.Logger, info BundleInfo) {
	log.Info("loaded TPA scanner bundle",
		zap.String("source", info.Source),
		zap.String("path", info.Path),
		zap.String("bundle_version", info.BundleVersion),
		zap.String("generated_at", info.GeneratedAt),
		zap.String("fingerprint", info.Fingerprint),
		zap.String("full_digest", info.FullDigest),
		zap.String("sequence", info.Sequence),
		zap.String("authority", info.Authority),
		zap.Uint64("trust_epoch", info.TrustEpoch),
		zap.Bool("signature_verified", info.SignatureVerified),
		zap.Bool("coverage_degraded", info.CoverageDegraded),
		zap.String("degraded_reason", info.DegradedReason),
		zap.Bool("pinned", info.Pinned),
		zap.Bool("rollback", info.Rollback),
		zap.Int("signature_count", info.SignatureCount),
		zap.Int("runnable_rules", info.RunnableRules),
		zap.Int("skipped_rules", info.SkippedRules),
		zap.Int("declared_skipped", info.DeclaredSkipped))
}

func storeBundle(check *BundleCheck, info BundleInfo) {
	activeBundleMu.Lock()
	activeBundleCheck = check
	activeBundleInfo = info
	activeBundleMu.Unlock()
}

func snapshotBundle() (*BundleCheck, BundleInfo) {
	activeBundleMu.RLock()
	defer activeBundleMu.RUnlock()
	return activeBundleCheck, activeBundleInfo
}

// activeBundleSnapshot returns ONE consistent (check, info) pair, lazily
// installing the embedded default when nothing has been published yet. Callers
// that need both the check and its coverage (the scan gate) must take them from
// this single snapshot, never from two lock acquisitions.
func activeBundleSnapshot() (*BundleCheck, BundleInfo) {
	check, info := snapshotBundle()
	if check != nil || info.LoadError != "" {
		return check, info
	}
	installDefaultBundleIfUnpublished()
	return snapshotBundle()
}

// installDefaultBundleIfUnpublished installs the embedded default, but only if
// nothing has been published by the time the activation mutex is held: a real
// configuration that raced this lazy install must never be clobbered by the
// default policy.
func installDefaultBundleIfUnpublished() {
	bundleRT.mu.Lock()
	defer bundleRT.mu.Unlock()
	if check, info := snapshotBundle(); check != nil || info.LoadError != "" {
		return
	}
	bundleRT.runLocked(bundleRT.policyOrDefaultLocked(), bundleLogger(nil), runOptions{force: true})
}
