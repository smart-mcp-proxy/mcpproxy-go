package bench

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestRetryRateForArm pins the documented per-arm retry-rate defaults
// (research D8): 0.0 for format-native JSON arms, 0.05 for TOON listings
// (multi-turn parsing cascades, arXiv:2605.29676 §5). Unknown arms fall back
// to 0.0.
func TestRetryRateForArm(t *testing.T) {
	cases := []struct {
		arm  string
		want float64
	}{
		{"baseline_json", 0.0},
		{"compact_sig", 0.0},
		{"tscg", 0.0},
		{"tron_dedup", 0.0},
		{"toon_listing", 0.05},
		{"toon_results", 0.0},    // results-class arm, not a listing format
		{"some_future_arm", 0.0}, // unknown arms default to 0.0
	}
	for _, c := range cases {
		if got := RetryRateForArm(c.arm); got != c.want {
			t.Errorf("RetryRateForArm(%q) = %v, want %v", c.arm, got, c.want)
		}
	}
}

// TestEstimateSessionCost verifies the D8 formula
//
//	session_cost = proxy_menu + calls × mean_response(arm) × (1 + retry_rate(arm))
//
// and that every input assumption (arm, calls, retry rate) is echoed in the
// resulting row. Provenance labeling is the report layer's job; the estimator
// only exposes the constant it must use.
func TestEstimateSessionCost(t *testing.T) {
	cases := []struct {
		name       string
		arm        string
		proxyMenu  int
		meanResp   float64
		calls      int
		wantRetry  float64
		wantTokens int
	}{
		// baseline_json: retry 0 — cost is proxy_menu + calls × mean.
		{"baseline 1 call", "baseline_json", 1200, 8640, 1, 0.0, 9840},
		{"baseline 3 calls", "baseline_json", 1200, 8640, 3, 0.0, 27120},
		{"baseline 5 calls", "baseline_json", 1200, 8640, 5, 0.0, 44400},
		{"baseline 10 calls", "baseline_json", 1200, 8640, 10, 0.0, 87600},
		// toon_listing: retry 0.05 inflates the per-call term.
		{"toon 1 call", "toon_listing", 1200, 7000, 1, 0.05, 8550},
		{"toon 3 calls", "toon_listing", 1200, 7000, 3, 0.05, 23250},
		{"toon 5 calls", "toon_listing", 1200, 7000, 5, 0.05, 37950},
		{"toon 10 calls", "toon_listing", 1200, 7000, 10, 0.05, 74700},
		// Zero calls degenerates to the menu cost alone.
		{"zero calls", "compact_sig", 1200, 500, 0, 0.0, 1200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EstimateSessionCost(c.arm, c.proxyMenu, c.meanResp, c.calls)
			want := SessionCostEstimate{
				Arm:             c.arm,
				CallsPerSession: c.calls,
				RetryRate:       c.wantRetry,
				EstimatedTokens: c.wantTokens,
			}
			if got != want {
				t.Errorf("EstimateSessionCost(%q, %d, %v, %d) = %+v, want %+v",
					c.arm, c.proxyMenu, c.meanResp, c.calls, got, want)
			}
		})
	}
}

// TestEstimateSessionCostRounding pins the rounding policy: the raw estimate
// is rounded half up to an integer token count (documented in session.go).
func TestEstimateSessionCostRounding(t *testing.T) {
	cases := []struct {
		name      string
		arm       string
		proxyMenu int
		meanResp  float64
		calls     int
		want      int
	}{
		// exact half rounds UP: 0 + 1×10.5×1.0 = 10.5 → 11
		{"half rounds up", "baseline_json", 0, 10.5, 1, 11},
		// below half rounds down: 10.4 → 10
		{"below half rounds down", "baseline_json", 0, 10.4, 1, 10},
		// above half rounds up: 10.6 → 11
		{"above half rounds up", "baseline_json", 0, 10.6, 1, 11},
		// retry-inflated exact half: 100 + 10×3×1.05 = 131.5 → 132
		{"retry half rounds up", "toon_listing", 100, 3, 10, 132},
		// retry-inflated fraction below half: 100 + 1×3×1.05 = 103.15 → 103
		{"retry fraction rounds down", "toon_listing", 100, 3, 1, 103},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EstimateSessionCost(c.arm, c.proxyMenu, c.meanResp, c.calls)
			if got.EstimatedTokens != c.want {
				t.Errorf("EstimatedTokens = %d, want %d", got.EstimatedTokens, c.want)
			}
		})
	}
}

// TestEstimateSessionCosts verifies the full rows: one row per arm per default
// call count {1,3,5,10}, in deterministic order (arms sorted lexicographically,
// then calls ascending) regardless of map iteration order.
func TestEstimateSessionCosts(t *testing.T) {
	means := map[string]float64{
		"toon_listing":  7000, // deliberately out of lexicographic order
		"baseline_json": 8640,
	}
	got := EstimateSessionCosts(1200, means)

	want := []SessionCostEstimate{
		{Arm: "baseline_json", CallsPerSession: 1, RetryRate: 0.0, EstimatedTokens: 9840},
		{Arm: "baseline_json", CallsPerSession: 3, RetryRate: 0.0, EstimatedTokens: 27120},
		{Arm: "baseline_json", CallsPerSession: 5, RetryRate: 0.0, EstimatedTokens: 44400},
		{Arm: "baseline_json", CallsPerSession: 10, RetryRate: 0.0, EstimatedTokens: 87600},
		{Arm: "toon_listing", CallsPerSession: 1, RetryRate: 0.05, EstimatedTokens: 8550},
		{Arm: "toon_listing", CallsPerSession: 3, RetryRate: 0.05, EstimatedTokens: 23250},
		{Arm: "toon_listing", CallsPerSession: 5, RetryRate: 0.05, EstimatedTokens: 37950},
		{Arm: "toon_listing", CallsPerSession: 10, RetryRate: 0.05, EstimatedTokens: 74700},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EstimateSessionCosts rows mismatch:\n got  %+v\n want %+v", got, want)
	}

	// Determinism (FR-010): repeated runs over the same map are identical.
	for i := 0; i < 5; i++ {
		again := EstimateSessionCosts(1200, means)
		if !reflect.DeepEqual(again, got) {
			t.Fatalf("run %d produced different rows: %+v", i, again)
		}
	}

	// Empty input yields no rows (not nil-panic, not a partial table).
	if rows := EstimateSessionCosts(1200, nil); len(rows) != 0 {
		t.Errorf("EstimateSessionCosts with no arms returned %d rows, want 0", len(rows))
	}
}

// TestDefaultCallsPerSession pins the D8 default grid and guards against
// callers mutating the returned slice affecting later calls.
func TestDefaultCallsPerSession(t *testing.T) {
	want := []int{1, 3, 5, 10}
	got := DefaultCallsPerSession()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultCallsPerSession() = %v, want %v", got, want)
	}
	got[0] = 99
	if again := DefaultCallsPerSession(); !reflect.DeepEqual(again, want) {
		t.Errorf("DefaultCallsPerSession() after caller mutation = %v, want %v", again, want)
	}
}

// TestSessionEstimateProvenance: session estimates are always ESTIMATE
// provenance (FR-019, SC-005) — the report layer attaches this label.
func TestSessionEstimateProvenance(t *testing.T) {
	if SessionEstimateProvenance != ProvenanceEstimated {
		t.Errorf("SessionEstimateProvenance = %q, want %q", SessionEstimateProvenance, ProvenanceEstimated)
	}
	if SessionEstimateProvenance != "estimated" {
		t.Errorf("SessionEstimateProvenance = %q, want \"estimated\"", SessionEstimateProvenance)
	}
}

// ---------------------------------------------------------------------------
// FR-013 — measured figures supersede the literature defaults (T044)
// ---------------------------------------------------------------------------
//
// The hazard these tests exist to prevent: RetryRateForArm returns 0.0 for an
// arm it has never heard of, which is bit-for-bit indistinguishable from a
// retry rate of 0.0 that a live agent loop actually measured. A section-level
// "estimated" badge over the whole session-cost table cannot express the
// difference either, so a reader looking at two 0.0 rows has no way to tell
// which one is evidence and which one is an absence of evidence.
//
// Every test below fails if that distinction collapses back into a bare
// float64.

// TestRetryRateProvenance_MeasuredZeroIsNotDefaultZero is the load-bearing
// test of FR-013: the SAME numeric rate (0.0) reached two different ways must
// come back carrying two different provenances.
func TestRetryRateProvenance_MeasuredZeroIsNotDefaultZero(t *testing.T) {
	const arm = "some_future_arm" // unknown to armRetryRates: default is 0.0

	defaultRate, defaultProv := ResolveRetryRate(arm, nil)
	measuredRate, measuredProv := ResolveRetryRate(arm, MeasuredOutcomes{
		arm: {RetryRate: 0.0, FirstAttemptSuccessPct: 88.0, Runs: 4},
	})

	if defaultRate != 0.0 || measuredRate != 0.0 {
		t.Fatalf("test premise broken: rates should both be 0.0, got default=%v measured=%v",
			defaultRate, measuredRate)
	}
	if defaultProv != ProvenanceEstimated {
		t.Errorf("unmeasured arm provenance = %q, want %q", defaultProv, ProvenanceEstimated)
	}
	if measuredProv != ProvenanceMeasured {
		t.Errorf("measured 0.0 provenance = %q, want %q", measuredProv, ProvenanceMeasured)
	}
	if defaultProv == measuredProv {
		t.Fatal("a measured 0.0 and a defaulted 0.0 are indistinguishable — FR-013 collapsed")
	}
}

// TestMeasuredRetryRateSupersedesDefault: where a measurement exists it wins,
// including where it contradicts the literature default, and the superseded
// default never leaks into the arithmetic.
func TestMeasuredRetryRateSupersedesDefault(t *testing.T) {
	const arm = "toon_listing" // documented default 0.05
	measured := MeasuredOutcomes{arm: {RetryRate: 0.20, FirstAttemptSuccessPct: 61.5, Runs: 6}}

	rate, prov := ResolveRetryRate(arm, measured)
	if rate != 0.20 {
		t.Errorf("ResolveRetryRate = %v, want the measured 0.20, not the default %v",
			rate, RetryRateForArm(arm))
	}
	if prov != ProvenanceMeasured {
		t.Errorf("provenance = %q, want %q", prov, ProvenanceMeasured)
	}

	// The estimator must price the row with the measured rate.
	row := EstimateSessionCostRow(arm, 1000, 100, 5, measured)
	if row.RetryRate != 0.20 {
		t.Errorf("row.RetryRate = %v, want 0.20 (the measurement)", row.RetryRate)
	}
	if want := 1000 + 5*100*1.20; float64(row.EstimatedTokens) != want {
		t.Errorf("row.EstimatedTokens = %d, want %v (measured rate applied)", row.EstimatedTokens, want)
	}
	if row.RetryRateProvenance != ProvenanceMeasured {
		t.Errorf("row.RetryRateProvenance = %q, want %q", row.RetryRateProvenance, ProvenanceMeasured)
	}
	// A row whose behavioural input was measured is arithmetic over measured
	// inputs — computed — but never "measured": its token magnitudes still
	// come from the deterministic tokenizer, not from provider usage.
	if row.Provenance != ProvenanceComputed {
		t.Errorf("row.Provenance = %q, want %q", row.Provenance, ProvenanceComputed)
	}
	if row.MeasuredRuns != 6 {
		t.Errorf("row.MeasuredRuns = %d, want 6", row.MeasuredRuns)
	}
	if row.FirstAttemptSuccessPct == nil {
		t.Fatal("row.FirstAttemptSuccessPct is nil despite a measurement (FR-013 covers success too)")
	}
	if *row.FirstAttemptSuccessPct != 61.5 {
		t.Errorf("row.FirstAttemptSuccessPct = %v, want 61.5", *row.FirstAttemptSuccessPct)
	}
}

// TestUnmeasuredRowStaysEstimated: with no measurement the row keeps the
// documented default AND stays badged estimated, with no fabricated success
// figure (nil, not 0 — a 0 would read as "measured, and nothing succeeded").
func TestUnmeasuredRowStaysEstimated(t *testing.T) {
	row := EstimateSessionCostRow("toon_listing", 1000, 100, 5, MeasuredOutcomes{})
	if row.RetryRate != 0.05 {
		t.Errorf("row.RetryRate = %v, want the documented default 0.05", row.RetryRate)
	}
	if row.RetryRateProvenance != ProvenanceEstimated {
		t.Errorf("row.RetryRateProvenance = %q, want %q", row.RetryRateProvenance, ProvenanceEstimated)
	}
	if row.Provenance != ProvenanceEstimated {
		t.Errorf("row.Provenance = %q, want %q", row.Provenance, ProvenanceEstimated)
	}
	if row.FirstAttemptSuccessPct != nil {
		t.Errorf("row.FirstAttemptSuccessPct = %v, want nil (no measurement exists)", *row.FirstAttemptSuccessPct)
	}
	if row.MeasuredRuns != 0 {
		t.Errorf("row.MeasuredRuns = %d, want 0", row.MeasuredRuns)
	}
}

// TestMeasuredOutcomeWithoutRunsIsNotAMeasurement: a zero-value outcome
// sitting in the map (an arm the loop was configured for but never ran) is an
// absence, not a measurement of zero — the same hazard one level up.
func TestMeasuredOutcomeWithoutRunsIsNotAMeasurement(t *testing.T) {
	measured := MeasuredOutcomes{"toon_listing": {}} // Runs == 0
	rate, prov := ResolveRetryRate("toon_listing", measured)
	if rate != 0.05 {
		t.Errorf("rate = %v, want the default 0.05 — a zero-run entry is not a measurement", rate)
	}
	if prov != ProvenanceEstimated {
		t.Errorf("provenance = %q, want %q for a zero-run entry", prov, ProvenanceEstimated)
	}
}

// TestSessionCostRows_MeasuredAndEstimatedCoexist: the whole point of per-ROW
// provenance — one table, two provenances, each row self-describing, in
// deterministic order.
func TestSessionCostRows_MeasuredAndEstimatedCoexist(t *testing.T) {
	means := map[string]float64{"baseline_json": 200, "toon_listing": 100}
	measured := MeasuredOutcomes{"toon_listing": {RetryRate: 0.20, FirstAttemptSuccessPct: 61.5, Runs: 6}}

	rows := EstimateSessionCostRows(1000, means, measured)
	if want := len(means) * len(DefaultCallsPerSession()); len(rows) != want {
		t.Fatalf("rows = %d, want %d (one per arm per calls point)", len(rows), want)
	}

	byArm := map[string]string{}
	for _, r := range rows {
		if prev, seen := byArm[r.Arm]; seen && prev != r.Provenance {
			t.Errorf("arm %q rows disagree on provenance: %q vs %q", r.Arm, prev, r.Provenance)
		}
		byArm[r.Arm] = r.Provenance
		if !IsValidProvenance(r.Provenance) {
			t.Errorf("row %+v carries provenance %q outside the closed enum", r, r.Provenance)
		}
		if !IsValidProvenance(r.RetryRateProvenance) {
			t.Errorf("row %+v carries retry-rate provenance %q outside the closed enum", r, r.RetryRateProvenance)
		}
	}
	if byArm["baseline_json"] != ProvenanceEstimated {
		t.Errorf("unmeasured arm badged %q, want %q", byArm["baseline_json"], ProvenanceEstimated)
	}
	if byArm["toon_listing"] != ProvenanceComputed {
		t.Errorf("measured arm badged %q, want %q", byArm["toon_listing"], ProvenanceComputed)
	}

	// Deterministic ordering (FR-010): arms lexicographic, calls ascending.
	if rows[0].Arm != "baseline_json" || rows[0].CallsPerSession != 1 {
		t.Errorf("first row = %+v, want baseline_json @ 1 call", rows[0])
	}

	// The per-row provenance must survive serialization: this is the field a
	// report consumer badges each row with.
	data, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal row: %v", err)
	}
	for _, key := range []string{"arm", "calls_per_session", "retry_rate", "estimated_tokens", "provenance", "retry_rate_provenance"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("serialized row missing key %q (got %v)", key, doc)
		}
	}
}

// TestEstimateSessionCostsUnchangedWithoutMeasurements guards the existing
// pipeline: with no measurements supplied the legacy rows are untouched, so
// adding FR-013 does not silently move any published figure.
func TestEstimateSessionCostsUnchangedWithoutMeasurements(t *testing.T) {
	means := map[string]float64{"baseline_json": 200, "toon_listing": 100}
	legacy := EstimateSessionCosts(1000, means)
	rows := EstimateSessionCostRows(1000, means, nil)
	if len(legacy) != len(rows) {
		t.Fatalf("row counts differ: legacy %d, rows %d", len(legacy), len(rows))
	}
	for i := range legacy {
		if !reflect.DeepEqual(legacy[i], rows[i].SessionCostEstimate) {
			t.Errorf("row %d diverges from the legacy estimate:\n got  %+v\n want %+v",
				i, rows[i].SessionCostEstimate, legacy[i])
		}
	}
}

// ---------------------------------------------------------------------------
// FR-023 — the cost-versus-outcome view (T046)
// ---------------------------------------------------------------------------
//
// Placement note: these exercise bench/report.go, whose companion
// report_test.go is owned by another workstream on this branch. Go does not
// care which _test.go file in a package holds a test, so they live here beside
// the other FR-013/FR-023 work rather than being dropped for want of a file.
//
// What they pin: a reader must be able to see which modes are WORTH their
// savings, not merely which are cheapest. That requires completion outcome
// beside cost at equal prominence (FR-018) and a cell that is cheap-but-worse
// visibly marked a regression rather than quietly winning the cost column.

// TestCostOutcomeView_PlotsEveryHonestCell: one point per cell that has an
// honest figure, in cell order, on a zero-based cost axis.
func TestCostOutcomeView_PlotsEveryHonestCell(t *testing.T) {
	block := sampleAgentLoopBlock()
	view := NewCostOutcomeView(block)

	if len(view.Points) != len(block.Cells) {
		t.Fatalf("plotted %d points, want %d (one per cell)", len(view.Points), len(block.Cells))
	}
	for i, p := range view.Points {
		cell := block.Cells[i]
		if p.CellID != cell.CellID {
			t.Errorf("point %d is %q, want %q — cell order must be preserved", i, p.CellID, cell.CellID)
		}
		if p.TokensPerCompletedTask != cell.TokensPerCompletedTask {
			t.Errorf("point %q cost = %v, want %v", p.CellID, p.TokensPerCompletedTask, cell.TokensPerCompletedTask)
		}
		if p.CompletionRatePct != cell.CompletionRatePct {
			t.Errorf("point %q completion = %v, want %v", p.CellID, p.CompletionRatePct, cell.CompletionRatePct)
		}
		if p.Regression != cell.Regression {
			t.Errorf("point %q regression = %v, want %v", p.CellID, p.Regression, cell.Regression)
		}
		if p.Provenance != cell.Provenance {
			t.Errorf("point %q provenance = %q, want %q", p.CellID, p.Provenance, cell.Provenance)
		}
	}

	// The cost axis starts at zero: a truncated axis makes a small saving look
	// like a large one, which is exactly the misreading this view exists to
	// prevent.
	if view.CostAxisMaxTokens < 41000 {
		t.Errorf("cost axis max = %v, want at least the most expensive cell (41000)", view.CostAxisMaxTokens)
	}
	// The completion axis is fixed 0..100 so cells stay comparable between runs.
	if view.CompletionAxisMaxPct != 100 {
		t.Errorf("completion axis max = %v, want a fixed 100", view.CompletionAxisMaxPct)
	}
	for _, p := range view.Points {
		if p.PlotY != view.PlotY(p.CompletionRatePct) {
			t.Errorf("point %q PlotY inconsistent with the axis mapping", p.CellID)
		}
	}
}

// TestCostOutcomeView_ExcludesWithheldCells: a cell whose figure was withheld
// has no honest position on either axis. It must be named as excluded rather
// than plotted at the origin, where it would read as "free and never
// completes".
func TestCostOutcomeView_ExcludesWithheldCells(t *testing.T) {
	block := sampleAgentLoopBlock()
	block.Cells = append(block.Cells, AgentLoopCell{
		CellID: "direct_deferred", Provenance: ProvenanceMeasured, Runs: 4,
		Withheld: true, WithheldReason: "cross-accounting-source aggregate",
	})

	view := NewCostOutcomeView(block)
	for _, p := range view.Points {
		if p.CellID == "direct_deferred" {
			t.Error("a withheld cell was plotted — it has no honest coordinates")
		}
	}
	if len(view.Excluded) != 1 || view.Excluded[0].CellID != "direct_deferred" {
		t.Fatalf("withheld cell not reported as excluded: %+v", view.Excluded)
	}
	if !strings.Contains(view.Excluded[0].Reason, "cross-accounting-source") {
		t.Errorf("exclusion reason = %q, want the withheld reason carried through", view.Excluded[0].Reason)
	}
}

// TestCostOutcomeView_SingleCellDoesNotDivideByZero: one cell (or several with
// identical cost) must not produce NaN positions.
func TestCostOutcomeView_SingleCellDoesNotDivideByZero(t *testing.T) {
	view := NewCostOutcomeView(&AgentLoopBlock{Cells: []AgentLoopCell{{
		CellID: "solo", Provenance: ProvenanceMeasured, Runs: 4,
		TokensPerCompletedTask: 0, CompletionRatePct: 0,
	}}})
	if len(view.Points) != 1 {
		t.Fatalf("points = %d, want 1", len(view.Points))
	}
	p := view.Points[0]
	if math.IsNaN(p.PlotX) || math.IsNaN(p.PlotY) || math.IsInf(p.PlotX, 0) || math.IsInf(p.PlotY, 0) {
		t.Errorf("degenerate axis produced non-finite coordinates: %+v", p)
	}
}

// TestCostOutcomeView_NilBlock: an absent agent loop renders nothing rather
// than panicking inside the template.
func TestCostOutcomeView_NilBlock(t *testing.T) {
	view := NewCostOutcomeView(nil)
	if len(view.Points) != 0 || len(view.Excluded) != 0 {
		t.Errorf("nil block produced a view: %+v", view)
	}
}

// TestDashboardV2_CostVersusOutcomeSection renders the real dashboard and
// checks the section carries what FR-018/FR-023 require.
func TestDashboardV2_CostVersusOutcomeSection(t *testing.T) {
	rep := sampleReportV2WithBlocks()
	html := renderDashboardV2(t, rep)

	// The two headline figures, named, at equal prominence.
	for _, want := range []string{"Tokens per completed task", "Completion rate"} {
		if !strings.Contains(html, want) {
			t.Errorf("cost-versus-outcome section missing %q", want)
		}
	}

	// Both figures for every cell, plus the per-ROW provenance badge — a cell
	// still carrying an assumption must be visible as such beside its
	// measured neighbours (FR-013).
	for _, cell := range rep.AgentLoop.Cells {
		for _, want := range []string{
			cell.CellID,
			strconv.FormatFloat(cell.TokensPerCompletedTask, 'f', -1, 64),
			strconv.FormatFloat(cell.CompletionRatePct, 'f', 1, 64),
		} {
			if !strings.Contains(html, want) {
				t.Errorf("cell %q: dashboard missing %q", cell.CellID, want)
			}
		}
		// Assert the badge sits in THIS ROW, not merely somewhere on the page.
		// A bare Contains passes off the page-wide provenance legend and the
		// static "estimated" footnote, so deleting the whole provenance column
		// left it green — the assertion could not see its own subject.
		row := rowFor(t, html, cell.CellID)
		if !strings.Contains(row, `badge badge-`+cell.Provenance) {
			t.Errorf("cell %q: its own row carries no provenance badge %q; row was:\n%s",
				cell.CellID, cell.Provenance, row)
		}
	}

	// The accounting source, with the pinned model, and the never-sum rule.
	for _, want := range []string{
		rep.AgentLoop.AccountingSource.Identity,
		rep.AgentLoop.AccountingSource.Model,
		rep.AgentLoop.Suite,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("cost-versus-outcome section missing accounting context %q", want)
		}
	}
	if !strings.Contains(strings.ToLower(html), "never summed") {
		t.Error("section does not state that provider-usage figures are never summed with tokenizer figures")
	}

	// The cheap-but-worse cell must be marked a regression, not presented as a
	// saving (FR-019), and the under-powered cell must not be a headline
	// (FR-021).
	// Both of these previously matched STATIC TEMPLATE PROSE — the legend
	// sentence "Red: a completion regression..." contains "regression", and
	// "at least four runs" contains "runs" — so they passed with no regressed
	// cell present and with the run-count column deleted entirely. Scope them
	// to the row that is supposed to carry the mark.
	var regressed, provisional *AgentLoopCell
	for i := range rep.AgentLoop.Cells {
		c := &rep.AgentLoop.Cells[i]
		if c.Regression {
			regressed = c
		}
		if !c.Headline && !c.Withheld && c.Runs > 0 {
			provisional = c
		}
	}
	if regressed == nil {
		t.Fatal("fixture must contain a regressed cell for this assertion to mean anything")
	}
	if row := rowFor(t, html, regressed.CellID); !strings.Contains(strings.ToUpper(row), "REGRESSION") {
		t.Errorf("the regressed cell %q must be marked in its own row; row was:\n%s", regressed.CellID, row)
	}
	if provisional != nil {
		row := rowFor(t, html, provisional.CellID)
		if !strings.Contains(row, strconv.Itoa(provisional.Runs)+" runs") {
			t.Errorf("cell %q must show its run count in its own row; row was:\n%s", provisional.CellID, row)
		}
	}

	// The plot itself: an inline SVG with both axes labelled.
	if !strings.Contains(html, "<svg") {
		t.Error("no cost-versus-outcome plot rendered")
	}
}

// rowFor extracts the single <tr> containing the given cell id, so a row-level
// assertion cannot be satisfied by static prose elsewhere on the page. Three
// assertions in this file were vacuous for exactly that reason.
func rowFor(t *testing.T, html, cellID string) string {
	t.Helper()
	needle := "<code>" + cellID + "</code>"
	i := strings.Index(html, needle)
	if i < 0 {
		t.Fatalf("cell %q does not appear in the rendered dashboard at all", cellID)
	}
	start := strings.LastIndex(html[:i], "<tr>")
	end := strings.Index(html[i:], "</tr>")
	if start < 0 || end < 0 {
		t.Fatalf("cell %q is not inside a table row", cellID)
	}
	return html[start : i+end]
}
