package bench

// Session-cost estimator (FR-019, research D8): an honest substitute for
// driving a live agent loop. For each arm and each calls-per-session point on
// the default grid it estimates the total discovery-related token cost of one
// agent session:
//
//	session_cost(arm, calls) =
//	    proxy_menu_tokens + calls × mean_response_tokens(arm) × (1 + retry_rate(arm))
//
// All inputs are surfaced: retry_rate and calls_per_session are echoed in each
// SessionCostEstimate row, proxy_menu_tokens and mean response tokens appear in
// the report's break_even/arms sections. Provenance labeling is handled by the
// report layer, which must mark these rows SessionEstimateProvenance
// ("estimated") — the retry rates are literature-derived defaults, not
// measurements.
//
// Rounding policy: the raw estimate is rounded HALF UP to an integer token
// count (10.5 → 11, 10.4 → 10). Inputs are non-negative, so half-up and
// half-away-from-zero coincide.

import (
	"math"
	"sort"
)

// SessionEstimateProvenance is the provenance label the report layer attaches
// to session-estimate rows (SC-005): always "estimated", never "measured" —
// the estimator is a model with documented assumptions, not an observation.
const SessionEstimateProvenance = ProvenanceEstimated

// defaultCallsPerSession is the D8 calls-per-session grid.
var defaultCallsPerSession = [...]int{1, 3, 5, 10}

// DefaultCallsPerSession returns the default calls-per-session grid {1,3,5,10}
// (research D8) as a fresh slice, so callers cannot mutate the shared default.
func DefaultCallsPerSession() []int {
	calls := make([]int, len(defaultCallsPerSession))
	copy(calls, defaultCallsPerSession[:])
	return calls
}

// armRetryRates holds the documented per-arm retry-rate defaults (research
// D8): 0.0 for format-native JSON renderings (baseline_json, compact_sig,
// tscg, tron_dedup — models parse JSON/compact signatures natively), 0.05 for
// toon_listing per the parsing-cascade evidence in arXiv:2605.29676 §5
// (TOON's multi-turn benchmark showed format-induced parsing errors cascading
// into retry turns at roughly this rate).
var armRetryRates = map[string]float64{
	"baseline_json": 0.0,
	"compact_sig":   0.0,
	"tscg":          0.0,
	"tron_dedup":    0.0,
	"toon_listing":  0.05,
}

// RetryRateForArm returns the documented retry-rate default for an arm.
// Unknown arms default to 0.0 (no retry evidence → no penalty; the rate is
// echoed in every estimate row, so the assumption stays visible).
func RetryRateForArm(arm string) float64 {
	return armRetryRates[arm]
}

// EstimateSessionCost computes one estimator row for a single arm and
// calls-per-session point (formula and rounding policy in the package
// comment). meanResponseTokens is the arm's mean retrieve_tools response cost
// — measured for the live baseline, derived from arm token ratios otherwise.
func EstimateSessionCost(arm string, proxyMenuTokens int, meanResponseTokens float64, calls int) SessionCostEstimate {
	retry := RetryRateForArm(arm)
	raw := float64(proxyMenuTokens) + float64(calls)*meanResponseTokens*(1+retry)
	return SessionCostEstimate{
		Arm:             arm,
		CallsPerSession: calls,
		RetryRate:       retry,
		EstimatedTokens: roundHalfUp(raw),
	}
}

// EstimateSessionCosts produces the full session-estimate table: one row per
// arm per default calls-per-session point, in deterministic order (arms
// sorted lexicographically, then calls ascending) regardless of map iteration
// order (FR-010). meanResponseTokensByArm maps arm name → mean
// retrieve_tools response tokens for that arm.
func EstimateSessionCosts(proxyMenuTokens int, meanResponseTokensByArm map[string]float64) []SessionCostEstimate {
	armNames := make([]string, 0, len(meanResponseTokensByArm))
	for arm := range meanResponseTokensByArm {
		armNames = append(armNames, arm)
	}
	sort.Strings(armNames)

	rows := make([]SessionCostEstimate, 0, len(armNames)*len(defaultCallsPerSession))
	for _, arm := range armNames {
		for _, calls := range defaultCallsPerSession {
			rows = append(rows, EstimateSessionCost(arm, proxyMenuTokens, meanResponseTokensByArm[arm], calls))
		}
	}
	return rows
}

// roundHalfUp rounds to the nearest integer with exact halves rounding up
// (the documented estimator rounding policy). Inputs are non-negative token
// counts.
func roundHalfUp(x float64) int {
	return int(math.Floor(x + 0.5))
}
