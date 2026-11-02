package runtime

import "testing"

// Nearest-rank p95 of 10 calls is the 10th fastest (ceil(9.5)); truncating to
// rank 9 reported nine fast calls plus one 3 s call as a ~30 ms p95.
func TestToolUsagePercentile_SmallTotalsUseNearestRank(t *testing.T) {
	tu := &ToolUsage{LatencyBuckets: make([]int64, len(latencyBucketBoundsMs)+1)}
	for i := 0; i < 9; i++ {
		tu.LatencyBuckets[latencyBucketIndex(30)]++
	}
	tu.LatencyBuckets[latencyBucketIndex(3000)]++

	if got, _ := tu.Percentile(0.95); got != 5000 {
		t.Errorf("p95 = %d ms, want 5000 (bucket holding the 3000 ms call)", got)
	}
	if got, _ := tu.Percentile(0.50); got != 50 {
		t.Errorf("p50 = %d ms, want 50", got)
	}
}
