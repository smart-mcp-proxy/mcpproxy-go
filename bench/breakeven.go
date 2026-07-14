// breakeven.go — break-even analysis for proxy-mediated tool discovery
// (Spec 083 US1, FR-003/FR-004, research D8).
package bench

import "fmt"

// ComputeBreakEven computes the number of retrieve_tools calls at which the
// proxy's cumulative context cost (proxy menu + N discovery responses) equals
// the naive full-menu cost:
//
//	break_even_calls = (naive_full_menu_tokens − proxy_menu_tokens) / mean_response_tokens
//
// Below break_even_calls discovery calls the proxy is strictly cheaper; each
// call beyond it spends part of the menu savings. All three inputs are echoed
// in the result (FR-004) so a reader can recompute the number from the row
// alone. Both menu inputs MUST come from the same canonical full-definition
// renderer (research D7b) or the numerator compares different token shapes.
//
// When the numerator is ≤ 0 the proxy menu is already at least as expensive
// as the naive menu — there are no savings to amortize — and the result is a
// NoBreakEven row (BreakEvenCalls stays 0), whatever the response cost.
// Otherwise meanResponseTokens must be positive: a zero mean means no
// responses were measured, and dividing by it is undefined, so that is an
// error rather than a fabricated verdict.
func ComputeBreakEven(naiveFullMenuTokens, proxyMenuTokens int, meanResponseTokens float64) (*BreakEvenAnalysis, error) {
	b := &BreakEvenAnalysis{
		NaiveFullMenuTokens: naiveFullMenuTokens,
		ProxyMenuTokens:     proxyMenuTokens,
		MeanResponseTokens:  meanResponseTokens,
	}
	numerator := naiveFullMenuTokens - proxyMenuTokens
	if numerator <= 0 {
		b.NoBreakEven = true
		return b, nil
	}
	if meanResponseTokens <= 0 {
		return nil, fmt.Errorf("break-even undefined: mean response tokens must be > 0, got %v (no measured responses?)", meanResponseTokens)
	}
	b.BreakEvenCalls = float64(numerator) / meanResponseTokens
	return b, nil
}
