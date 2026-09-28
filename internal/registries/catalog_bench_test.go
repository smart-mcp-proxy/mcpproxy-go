package registries

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSearchAll_PerformanceBudget is the catalog half of SC-011: p95 of
// SearchAll must stay within the per-source timeout plus the same 0.5s
// overhead budget SC-011 allows over the 5s production default. Two sources
// answer in 50ms; one never answers at all. The test sets SourceTimeout to
// 500ms (rather than the 5s production default) so 20 runs stay fast, which
// is exactly what SearchOptions.SourceTimeout exists for.
func TestSearchAll_PerformanceBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-sensitive benchmark in -short mode")
	}

	fastBody := `[{"id":"a","name":"A"}]`
	fast1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fastBody))
	}))
	defer fast1.Close()
	fast2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fastBody))
	}))
	defer fast2.Close()

	block := make(chan struct{})
	defer close(block)
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer hung.Close()

	withTestRegistries(t, []RegistryEntry{
		{ID: "fast1", Name: "Fast1", ServersURL: fast1.URL},
		{ID: "fast2", Name: "Fast2", ServersURL: fast2.URL},
		{ID: "hung", Name: "Hung", ServersURL: hung.URL},
	})

	const runs = 20
	const sourceTimeout = 500 * time.Millisecond
	const overheadBudget = 500 * time.Millisecond
	durations := make([]time.Duration, 0, runs)

	for i := 0; i < runs; i++ {
		start := time.Now()
		hits, _, unavailable := SearchAll(context.Background(), "", "", 10, SearchOptions{SourceTimeout: sourceTimeout})
		durations = append(durations, time.Since(start))

		foundFast1, foundFast2 := false, false
		for _, h := range hits {
			switch h.Source {
			case "fast1":
				foundFast1 = true
			case "fast2":
				foundFast2 = true
			}
		}
		if !foundFast1 || !foundFast2 {
			t.Fatalf("run %d: expected both fast sources' results, got %+v", i, hits)
		}
		if len(unavailable) != 1 || unavailable[0].Source != "hung" {
			t.Fatalf("run %d: expected 'hung' listed unavailable, got %+v", i, unavailable)
		}
	}

	p95 := percentile95(durations)
	budget := sourceTimeout + overheadBudget
	if p95 > budget {
		t.Fatalf("p95 SearchAll duration %s exceeds budget %s (timeout %s + overhead %s)", p95, budget, sourceTimeout, overheadBudget)
	}
}

func percentile95(d []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), d...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	idx := (len(sorted) * 95) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
