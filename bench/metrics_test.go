package bench

import (
	"math"
	"testing"
)

// almostEqual compares floats within a small tolerance (metric math involves
// log2 divisions, so exact equality is brittle).
func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

// worked example reused across the metric tests:
//
//	relevant labels: A(rel 2), B(rel 1), C(rel 1)  -> 3 relevant tools
//	ranking returned: [A, X, B, Y]                 -> X, Y are irrelevant
var (
	exLabels = []Label{
		{ToolID: "A", Relevance: 2},
		{ToolID: "B", Relevance: 1},
		{ToolID: "C", Relevance: 1},
	}
	exRanked = []string{"A", "X", "B", "Y"}
)

func TestRecallAtK(t *testing.T) {
	cases := []struct {
		k    int
		want float64
	}{
		{1, 1.0 / 3.0}, // top-1 {A}: 1 of 3 relevant
		{3, 2.0 / 3.0}, // top-3 {A,X,B}: 2 of 3 relevant
		{5, 2.0 / 3.0}, // only 4 results; {A,B} retrieved: 2 of 3
	}
	for _, c := range cases {
		got := RecallAtK(exRanked, exLabels, c.k)
		if !almostEqual(got, c.want) {
			t.Errorf("RecallAtK(k=%d) = %v, want %v", c.k, got, c.want)
		}
	}
}

func TestReciprocalRank(t *testing.T) {
	// First relevant (A) is at rank 1 -> RR = 1.0
	if got := ReciprocalRank(exRanked, exLabels); !almostEqual(got, 1.0) {
		t.Errorf("ReciprocalRank = %v, want 1.0", got)
	}
	// First relevant (B) at rank 2 -> RR = 0.5
	if got := ReciprocalRank([]string{"Z", "B", "A"}, exLabels); !almostEqual(got, 0.5) {
		t.Errorf("ReciprocalRank(B@2) = %v, want 0.5", got)
	}
	// No relevant retrieved -> RR = 0
	if got := ReciprocalRank([]string{"Z", "Y"}, exLabels); !almostEqual(got, 0.0) {
		t.Errorf("ReciprocalRank(none) = %v, want 0", got)
	}
}

func TestNDCGAtK(t *testing.T) {
	// DCG  = 2/log2(2) + 0 + 1/log2(4)          = 2 + 0.5      = 2.5
	// IDCG = 2/log2(2) + 1/log2(3) + 1/log2(4)  = 2 + 0.63093 + 0.5 = 3.13093
	// nDCG = 2.5 / 3.13093 = 0.798486
	want := 2.5 / (2.0 + 1.0/math.Log2(3) + 0.5)
	if got := NDCGAtK(exRanked, exLabels, 10); !almostEqual(got, want) {
		t.Errorf("NDCGAtK(10) = %v, want %v", got, want)
	}
	// Perfect ranking -> nDCG = 1.0
	if got := NDCGAtK([]string{"A", "B", "C"}, exLabels, 10); !almostEqual(got, 1.0) {
		t.Errorf("NDCGAtK(perfect) = %v, want 1.0", got)
	}
}

func TestAveragePrecision(t *testing.T) {
	// A@1 -> precision 1/1 = 1.0 ; B@3 -> precision 2/3 ; C not retrieved -> 0
	// AP = (1.0 + 0.6667 + 0) / 3 = 0.555556
	want := (1.0 + 2.0/3.0) / 3.0
	if got := AveragePrecision(exRanked, exLabels); !almostEqual(got, want) {
		t.Errorf("AveragePrecision = %v, want %v", got, want)
	}
}

func TestScoreRetrieval(t *testing.T) {
	golden := &GoldenSet{
		CorpusVersion: "corpus_v1",
		Queries: []GoldenQuery{
			{ID: "q1", Query: "find A", Labels: exLabels},
			{ID: "q2", Query: "find D", Labels: []Label{{ToolID: "D", Relevance: 2}}},
		},
	}
	// Deterministic fake search: q1 -> exRanked, q2 -> perfect [D]
	search := func(query string, _ int) ([]string, error) {
		if query == "find A" {
			return exRanked, nil
		}
		return []string{"D"}, nil
	}
	m, err := ScoreRetrieval(golden, search, []int{1, 3, 5, 10})
	if err != nil {
		t.Fatalf("ScoreRetrieval error: %v", err)
	}
	if m.RunsAveraged != 1 {
		t.Errorf("RunsAveraged = %d, want 1", m.RunsAveraged)
	}
	// Recall@1: q1=1/3, q2=1 -> mean = (0.3333+1)/2 = 0.66667
	wantR1 := (1.0/3.0 + 1.0) / 2.0
	if !almostEqual(m.RecallAt[1], wantR1) {
		t.Errorf("mean Recall@1 = %v, want %v", m.RecallAt[1], wantR1)
	}
	// MRR: q1=1.0, q2=1.0 -> 1.0
	if !almostEqual(m.MRR, 1.0) {
		t.Errorf("MRR = %v, want 1.0", m.MRR)
	}
	if m.QueryCount != 2 {
		t.Errorf("QueryCount = %d, want 2", m.QueryCount)
	}
}
