package bench

import (
	"reflect"
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
