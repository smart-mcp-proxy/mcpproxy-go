package runtime

import "testing"

// Spec 109-k / audit finding F-Token: ServerTokenMetrics.Estimated tells the
// Web UI, macOS and CLI whether AverageQueryResultSize is a synthetic
// simulation (no real retrieve_tools call observed yet) or a real observed
// average. resolveAverageQueryResultSize is the pure decision the wiring in
// CalculateTokenSavings delegates to, so it can be unit-tested without
// constructing a full Runtime.
func TestResolveAverageQueryResultSize(t *testing.T) {
	t.Run("no real data yet: estimated, simulated value kept", func(t *testing.T) {
		size, estimated := resolveAverageQueryResultSize(1234, 0, false)
		if !estimated {
			t.Fatal("expected estimated=true before any real retrieve_tools call")
		}
		if size != 1234 {
			t.Fatalf("expected simulated size to pass through, got %d", size)
		}
	})

	t.Run("real data available: not estimated, derived from real bytes", func(t *testing.T) {
		size, estimated := resolveAverageQueryResultSize(1234, 4000, true)
		if estimated {
			t.Fatal("expected estimated=false once a real retrieve_tools call has happened")
		}
		if size <= 0 {
			t.Fatalf("expected a non-zero real size, got %d", size)
		}
		if size == 1234 {
			t.Fatal("expected the real size to differ from the synthetic simulation input")
		}
	})

	t.Run("real data available but tiny: still non-zero and not estimated", func(t *testing.T) {
		size, estimated := resolveAverageQueryResultSize(1234, 1, true)
		if estimated {
			t.Fatal("expected estimated=false")
		}
		if size <= 0 {
			t.Fatalf("expected a non-zero floor, got %d", size)
		}
	})
}
