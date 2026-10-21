package scanner

import (
	"fmt"
	"time"
)

// consecutiveFailureWarnAt is how many fetch_unavailable / channel_invalid
// cycles in a row make the doctor call the refresh stuck (tasks B16). One or
// two are ordinary network weather; three days of them is not.
const consecutiveFailureWarnAt = 3

// BundleDoctorWarnings turns the bundle status into at most three short doctor
// warnings: stale, stuck on last-known-good, and fetch configured but inactive
// (tasks B16). The strings carry no digest, path, URL or host: a doctor report
// is made to be shared, and the reason codes are a closed vocabulary. It is a
// pure function of its arguments so the truth table is testable without a
// runtime; the caller supplies the status (the server registers
// BundleStatus() with time.Now()).
//
// These warnings intentionally feed the doctor verdict, the MCP doctor tool and
// the telemetry runtime_warnings pass/fail bit: each one is an actionable state
// on an install that configured a refresh. A stale embedded corpus on an install
// that never configured a refresh stays in the status surfaces only; binary
// upgrades are Spec 079's nudge.
//
// now is accepted for the signature's symmetry with the other status readers;
// every age here was already computed at read time into the info (Stale,
// StalenessDays), so the function does not recompute against a second clock.
func BundleDoctorWarnings(info BundleInfo, now time.Time) []string {
	_ = now
	var warnings []string

	if w := doctorStaleWarning(info); w != "" {
		warnings = append(warnings, w)
	}
	if w := doctorStuckWarning(info); w != "" {
		warnings = append(warnings, w)
	}
	if w := doctorFetchInactiveWarning(info); w != "" {
		warnings = append(warnings, w)
	}
	return warnings
}

// doctorStaleWarning is warning (1): a stale corpus on an install that
// configured a way to refresh it (an external file or fetched bundle, or fetch
// enabled).
func doctorStaleWarning(info BundleInfo) string {
	if !info.Stale {
		return ""
	}
	fetchOn := info.Refresh != nil && info.Refresh.FetchEnabled
	if info.Source != BundleSourceFile && info.Source != BundleSourceFetch && !fetchOn {
		return ""
	}
	age := "past its freshness threshold"
	if info.StalenessDays != nil {
		age = fmt.Sprintf("%d days old", *info.StalenessDays)
	}
	threshold := ""
	if info.StaleAfterDays != nil {
		threshold = fmt.Sprintf(" (stale after %d days)", *info.StaleAfterDays)
	}
	return fmt.Sprintf("TPA signature bundle is stale: %s%s; run `mcpproxy security bundle refresh` or check the refresh source", age, threshold)
}

// doctorStuckWarning is warning (2): the refresh keeps failing, or a candidate
// was refused after the serving bundle was activated, so the install is stuck
// on last-known-good. downgrade and deny_listed are a quiet set: they are the
// pipeline correctly refusing a replay or a rolled-away release.
func doctorStuckWarning(info BundleInfo) string {
	if rej := info.LastRejection; rej != nil && rej.Reason != RejectDowngrade && rej.Reason != RejectDenyListed {
		// "Activated" is the serving bundle's activation time; the embedded
		// default has none, so process start (when it became active here) is
		// the reference.
		since := info.LoadedAt
		if info.LastActivatedAt != nil {
			since = *info.LastActivatedAt
		}
		if rej.At.After(since) {
			return fmt.Sprintf("TPA signature bundle: a newer candidate was refused (%s); still serving last-known-good (see `mcpproxy security bundle`)", rej.Reason)
		}
	}
	if info.Refresh != nil && info.Refresh.ConsecutiveFailures >= consecutiveFailureWarnAt {
		return fmt.Sprintf("TPA signature bundle refresh has failed %d times in a row; still serving last-known-good (see `mcpproxy security bundle`)", info.Refresh.ConsecutiveFailures)
	}
	return ""
}

// doctorFetchInactiveWarning is warning (3): fetch is enabled but the leg may
// not issue requests because of a configuration problem (Spec 087 edge case: no
// key configured is a clear configuration error, not silence).
func doctorFetchInactiveWarning(info BundleInfo) string {
	r := info.Refresh
	if r == nil || !r.FetchEnabled || r.LastOutcome != RefreshOutcomeDisabled {
		return ""
	}
	switch r.LastReason {
	case RefreshReasonNoURL, RefreshReasonInvalidURL:
		return "TPA bundle fetch is enabled but inactive: security.tpa_fetch_url is empty or invalid"
	case RefreshReasonNoTrustedSigner:
		return "TPA bundle fetch is enabled but inactive: no trusted signer is configured for the active root (set security.tpa_operator_signers with tpa_active_root=operator)"
	}
	return ""
}
