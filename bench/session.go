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

// ---------------------------------------------------------------------------
// FR-013 — a measurement supersedes the assumption, and the reader can tell
// ---------------------------------------------------------------------------
//
// The estimator above prices a session with a retry rate taken from the
// literature (research D8). Once a live agent loop actually runs a mode, that
// assumption is obsolete for that mode — but only for that mode. FR-013 asks
// for exactly that: measured figures REPLACE the defaults wherever a
// measurement exists, and anything still resting on an assumption stays badged
// estimated and is never presented without the distinction.
//
// Why this needs a per-ROW field rather than a section badge. RetryRateForArm
// returns 0.0 for an arm it has never heard of, which is bit-identical to a
// retry rate of 0.0 that a live loop measured. Under one section-level
// "estimated" badge, a table can therefore hold a measured 0.0 beside a
// defaulted 0.0 with nothing on the page separating them, and a reader has no
// way to know which number is evidence. Every row carries its own provenance
// so the two are distinguishable inside one table (contracts/report-v2-
// additions.md, "Add per-row provenance").
//
// Why a measured row is "computed" and not "measured". Substituting a measured
// retry rate does NOT make the row an observation. The row's token magnitudes
// still come from the deterministic tokenizer; only the behavioural multiplier
// came from the loop. The dashboard's own vocabulary already has the right
// label for that — computed: "arithmetic over measured inputs". Calling the
// row measured would claim the token figure itself was observed under a
// provider, and would invite exactly the cross-accounting-source confusion the
// never-sum rule exists to prevent (data-model.md invariant 2). Note also that
// the retry rate is a dimensionless rate, not a token quantity: applying it to
// tokenizer-counted tokens parameterises the estimate, it does not sum a
// provider figure with a tokenizer figure.

// MeasuredArmOutcome is what a live agent loop observed for ONE arm (or mode
// cell): its retry rate, its first-attempt success rate, and how many runs
// stand behind them. Runs is not decoration — see HasMeasurement.
type MeasuredArmOutcome struct {
	// RetryRate is the measured mean retries per call, on the same scale as
	// the armRetryRates defaults it supersedes.
	RetryRate float64
	// FirstAttemptSuccessPct is the measured first-attempt success rate
	// (FR-010), carried because FR-013 covers success figures as well as
	// retries.
	FirstAttemptSuccessPct float64
	// Runs is the number of completed runs behind the two figures above. A
	// zero here means no run produced them, which HasMeasurement treats as an
	// absence rather than a measurement of zero — the same
	// indistinguishable-zero hazard one level up. FR-021's four-run bar is a
	// PUBLICATION gate applied by the report layer, not a gate on
	// superseding: a two-run measurement is still better evidence than a
	// literature constant, and the row publishes Runs so the reader can apply
	// FR-021 themselves.
	Runs int
}

// HasMeasurement reports whether this outcome is backed by at least one run.
// An outcome that reached the map without a run behind it (an arm the loop was
// configured for but never executed) is an absence, and must not supersede a
// documented default.
func (m MeasuredArmOutcome) HasMeasurement() bool { return m.Runs > 0 }

// MeasuredOutcomeSource supplies measured outcomes per arm. It is a structural
// interface on purpose: the live agent loop that produces these figures is a
// separate concern with its own accounting source, and the estimator must stay
// unit-testable with a fake and no suite running (the bench/flipgate.go and
// bench/armrun.go precedent). A nil source means "nothing was measured", which
// is the ordinary offline case.
type MeasuredOutcomeSource interface {
	MeasuredOutcomeForArm(arm string) (MeasuredArmOutcome, bool)
}

// MeasuredOutcomes is the trivial map-backed MeasuredOutcomeSource, used by
// callers that already hold the figures and by tests.
type MeasuredOutcomes map[string]MeasuredArmOutcome

// MeasuredOutcomeForArm implements MeasuredOutcomeSource. It reports ok only
// for an entry that is actually backed by runs, so a zero-value entry cannot
// masquerade as a measured zero.
func (m MeasuredOutcomes) MeasuredOutcomeForArm(arm string) (MeasuredArmOutcome, bool) {
	out, ok := m[arm]
	if !ok || !out.HasMeasurement() {
		return MeasuredArmOutcome{}, false
	}
	return out, true
}

// ResolveRetryRate returns the retry rate to price an arm with, together with
// the provenance of that rate: ProvenanceMeasured when a live measurement
// supersedes the default, ProvenanceEstimated when the documented default
// stands. The provenance is the whole point of the second return value — the
// rate alone cannot tell a measured 0.0 from a defaulted 0.0.
func ResolveRetryRate(arm string, measured MeasuredOutcomeSource) (rate float64, provenance string) {
	if measured != nil {
		if out, ok := measured.MeasuredOutcomeForArm(arm); ok {
			return out.RetryRate, ProvenanceMeasured
		}
	}
	return RetryRateForArm(arm), ProvenanceEstimated
}

// SessionCostRow is a session-cost estimate that knows where its inputs came
// from. It EMBEDS SessionCostEstimate rather than redefining it, so the
// serialized row is a strict superset of the existing shape (the embedded
// fields flatten into the same JSON object) and no existing consumer of a
// session-estimate row breaks.
type SessionCostRow struct {
	SessionCostEstimate

	// Provenance is this ROW's label, from the closed measured/computed/
	// estimated enum: computed when a measured retry rate was applied,
	// estimated when the row still rests on the literature default. Never
	// measured — the token magnitudes are tokenizer-counted (see the block
	// comment above).
	Provenance string `json:"provenance"`

	// RetryRateProvenance labels the retry rate specifically. It is the field
	// that makes a measured 0.0 distinguishable from a defaulted 0.0, and it
	// is separate from Provenance because a reader scanning the retry-rate
	// column needs the answer in that column.
	RetryRateProvenance string `json:"retry_rate_provenance"`

	// MeasuredRuns is how many runs back the measurement, 0 when none. It
	// travels with the row so a consumer can apply the FR-021 four-run
	// headline bar without re-joining against the agent-loop block.
	MeasuredRuns int `json:"measured_runs,omitempty"`

	// FirstAttemptSuccessPct is the measured first-attempt success rate, or
	// nil when nothing was measured. It is a POINTER for the same reason
	// PayloadDecompositionRow.ShareAnnotationsPct is: a 0 here would read as
	// "measured, and nothing ever succeeded on the first attempt", which is a
	// far stronger claim than "not measured". nil marshals to null.
	FirstAttemptSuccessPct *float64 `json:"first_attempt_success_pct"`
}

// EstimateSessionCostRow computes one session-cost row, applying a measured
// retry rate for the arm when one exists and the documented default otherwise.
// The formula and rounding policy are unchanged (package comment); only the
// source of retry_rate can differ, and the row says which source it was.
func EstimateSessionCostRow(arm string, proxyMenuTokens int, meanResponseTokens float64, calls int, measured MeasuredOutcomeSource) SessionCostRow {
	retry, retryProv := ResolveRetryRate(arm, measured)
	raw := float64(proxyMenuTokens) + float64(calls)*meanResponseTokens*(1+retry)

	row := SessionCostRow{
		SessionCostEstimate: SessionCostEstimate{
			Arm:             arm,
			CallsPerSession: calls,
			RetryRate:       retry,
			EstimatedTokens: roundHalfUp(raw),
		},
		Provenance:          SessionEstimateProvenance,
		RetryRateProvenance: retryProv,
	}
	if retryProv == ProvenanceMeasured {
		// Arithmetic over a measured input: computed, not estimated — and not
		// measured either.
		row.Provenance = ProvenanceComputed
		if out, ok := measured.MeasuredOutcomeForArm(arm); ok {
			row.MeasuredRuns = out.Runs
			success := out.FirstAttemptSuccessPct
			row.FirstAttemptSuccessPct = &success
		}
	}
	return row
}

// EstimateSessionCostRows produces the full self-describing session-estimate
// table: one row per arm per default calls-per-session point, in the same
// deterministic order as EstimateSessionCosts (arms lexicographic, then calls
// ascending — FR-010), with measured and estimated rows coexisting in the one
// slice. Pass a nil source offline; every row is then identical to what
// EstimateSessionCosts produces, plus its estimated badge.
func EstimateSessionCostRows(proxyMenuTokens int, meanResponseTokensByArm map[string]float64, measured MeasuredOutcomeSource) []SessionCostRow {
	armNames := make([]string, 0, len(meanResponseTokensByArm))
	for arm := range meanResponseTokensByArm {
		armNames = append(armNames, arm)
	}
	sort.Strings(armNames)

	rows := make([]SessionCostRow, 0, len(armNames)*len(defaultCallsPerSession))
	for _, arm := range armNames {
		for _, calls := range defaultCallsPerSession {
			rows = append(rows, EstimateSessionCostRow(arm, proxyMenuTokens, meanResponseTokensByArm[arm], calls, measured))
		}
	}
	return rows
}

// SessionCostRowsMixed reports whether a table holds rows of more than one
// provenance. A caller rendering a section badge must check this: a single
// section badge over a mixed table is precisely the misrepresentation FR-013
// forbids, so a mixed table must be badged per row instead.
func SessionCostRowsMixed(rows []SessionCostRow) bool {
	if len(rows) == 0 {
		return false
	}
	first := rows[0].Provenance
	for _, r := range rows[1:] {
		if r.Provenance != first {
			return true
		}
	}
	return false
}
