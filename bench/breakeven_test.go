package bench

import (
	"math"
	"strings"
	"testing"
)

func TestComputeBreakEven(t *testing.T) {
	cases := []struct {
		name        string
		naive       int
		proxyMenu   int
		mean        float64
		wantCalls   float64
		wantNoBE    bool
		wantErr     bool
		errContains string
	}{
		{
			name:      "headline case",
			naive:     10000,
			proxyMenu: 1000,
			mean:      300,
			wantCalls: 30,
		},
		{
			name:      "fractional result is not rounded",
			naive:     1100,
			proxyMenu: 1000,
			mean:      3,
			wantCalls: 100.0 / 3.0,
		},
		{
			name:      "live-profiling shape (research D1: ~38 calls)",
			naive:     340320,
			proxyMenu: 12000,
			mean:      8640,
			wantCalls: float64(340320-12000) / 8640.0,
		},
		{
			name:      "numerator zero: proxy menu equals naive",
			naive:     1000,
			proxyMenu: 1000,
			mean:      300,
			wantNoBE:  true,
		},
		{
			name:      "numerator negative: proxy menu heavier than naive",
			naive:     500,
			proxyMenu: 1000,
			mean:      300,
			wantNoBE:  true,
		},
		{
			name:      "numerator negative with zero mean still a clean no-break-even",
			naive:     500,
			proxyMenu: 1000,
			mean:      0,
			wantNoBE:  true,
		},
		{
			name:        "zero mean guard",
			naive:       10000,
			proxyMenu:   1000,
			mean:        0,
			wantErr:     true,
			errContains: "mean response tokens",
		},
		{
			name:        "negative mean guard",
			naive:       10000,
			proxyMenu:   1000,
			mean:        -5,
			wantErr:     true,
			errContains: "mean response tokens",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ComputeBreakEven(tc.naive, tc.proxyMenu, tc.mean)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("error %q does not mention %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("ComputeBreakEven: %v", err)
			}

			// Inputs are echoed in the struct (FR-004) — a reader must be able
			// to recompute break_even_calls from the row alone.
			if got.NaiveFullMenuTokens != tc.naive {
				t.Errorf("NaiveFullMenuTokens = %d, want %d", got.NaiveFullMenuTokens, tc.naive)
			}
			if got.ProxyMenuTokens != tc.proxyMenu {
				t.Errorf("ProxyMenuTokens = %d, want %d", got.ProxyMenuTokens, tc.proxyMenu)
			}
			if got.MeanResponseTokens != tc.mean {
				t.Errorf("MeanResponseTokens = %v, want %v", got.MeanResponseTokens, tc.mean)
			}

			if got.NoBreakEven != tc.wantNoBE {
				t.Errorf("NoBreakEven = %v, want %v", got.NoBreakEven, tc.wantNoBE)
			}
			if tc.wantNoBE {
				if got.BreakEvenCalls != 0 {
					t.Errorf("BreakEvenCalls = %v, want 0 on a no-break-even row", got.BreakEvenCalls)
				}
				return
			}
			if math.Abs(got.BreakEvenCalls-tc.wantCalls) > 1e-9 {
				t.Errorf("BreakEvenCalls = %v, want %v", got.BreakEvenCalls, tc.wantCalls)
			}
		})
	}
}
