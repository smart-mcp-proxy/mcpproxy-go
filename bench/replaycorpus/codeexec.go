// codeexec.go — the response-side saving of code execution.
//
// Every other savings figure in this harness is about the MENU: how many tokens
// of tool definitions an agent carries. Code execution's menu is already
// measured and it is the smallest of the three surfaces, but the menu is not
// where code execution actually pays.
//
// It pays on the RESPONSE side. A script issues several upstream calls inside
// the sandbox and only the value it returns enters the model's context; the
// intermediate responses — which an agent driving those tools itself would have
// paid for in full, request and response both — never appear. That difference
// is what this file computes, and nothing else in bench measures it.
package replaycorpus

import "math"

// CodeExecToolName is the built-in whose activity records parent a sandbox's
// sub-calls. Sub-calls join to it by parent_id (see group.go).
const CodeExecToolName = "code_execution"

// CodeExecSaving is the response-side saving of ONE code_execution call.
//
// Baseline is what an agent would have paid making the sub-calls itself; Proxy
// is what it actually paid — the script it sent plus the single result it got
// back. The difference can be NEGATIVE, and that is a real outcome rather than
// an error: a script that dispatches nothing, or whose sub-calls return less
// than the script's own source costs, genuinely loses tokens.
type CodeExecSaving struct {
	// ParentID is the code_execution call's own request id.
	ParentID string `json:"parent_id"`

	SubCalls int `json:"sub_calls"`

	// Basis names both the provenance AND the unit: measured figures are
	// TOKENS, estimated figures are BYTES. They are never mixed — see
	// ReasonMixedCostBasis.
	Basis CostBasis `json:"basis"`

	// Reason explains a CostUnavailable saving. Empty otherwise.
	Reason ExclusionReason `json:"reason,omitempty"`

	// Baseline, Proxy and Saving are populated only when Basis is not
	// CostUnavailable. An unavailable saving carries no numbers at all, because
	// a zero here would read as "code execution saved nothing" — a measurement
	// — when the truth is that no measurement was possible.
	Baseline int `json:"baseline,omitempty"`
	Proxy    int `json:"proxy,omitempty"`
	Saving   int `json:"saving,omitempty"`

	// Split by BILLING DIRECTION, because Saving above is not proportional to
	// cost. A tool call's ARGUMENTS are tokens the model generated (output,
	// billed around 5x input) and its RESPONSE is tokens it reads (input, billed
	// 1x, or 0.1x when the prefix is cached). Code execution trades the cheap
	// direction for the expensive one: it replaces N argument-writings with ONE
	// script, whose cost is fixed regardless of N.
	//
	// Measured consequence, on real traffic at three sub-calls: +262 tokens
	// "saved" and a NET LOSS in money. Anything that quotes the token figure as
	// a saving has to carry this split beside it.
	BaselineOutput int `json:"baseline_output,omitempty"`
	BaselineInput  int `json:"baseline_input,omitempty"`
	ProxyOutput    int `json:"proxy_output,omitempty"`
	ProxyInput     int `json:"proxy_input,omitempty"`
}

// amount returns the cost in the unit its basis implies, and whether a figure
// exists at all.
func (c Cost) amount() (int, bool) {
	switch c.Basis {
	case CostMeasured:
		return c.Tokens, true
	case CostEstimated:
		return c.Bytes, true
	default:
		return 0, false
	}
}

// CodeExecSavingFor computes the response-side saving of one code_execution
// call. parent must be the code_execution record itself; its SubCalls are the
// sandbox dispatches joined by parent_id.
//
// The accounting refuses three things rather than producing a plausible number:
//
//   - a component with no honest figure (CostUnavailable) makes the whole
//     saving unavailable, because a missing sub-call understates the baseline
//     and so quietly flatters code execution;
//   - components on DIFFERENT bases are never summed, since measured figures
//     are tokens and estimated ones are bytes, and adding them produces a
//     number in no unit at all;
//   - a parent that is not a code_execution call, which is a caller error
//     rather than a measurement.
func CodeExecSavingFor(parent *ReplayCall) CodeExecSaving {
	out := CodeExecSaving{}
	if parent == nil {
		return CodeExecSaving{Basis: CostUnavailable, Reason: ReasonNotACall}
	}
	out.ParentID = parent.RequestID
	out.SubCalls = len(parent.SubCalls)
	if parent.ToolName != CodeExecToolName {
		return CodeExecSaving{ParentID: out.ParentID, Basis: CostUnavailable, Reason: ReasonNotACall}
	}

	// Collect every component first so a single pass can decide the basis.
	// The parent's own request is the script source and its response is the
	// value the model received; both are costs code execution really incurred.
	type component struct {
		cost      Cost
		child     bool
		generated bool
	}
	// generated marks a component the MODEL WROTE — a call's arguments — as
	// opposed to one it read. The distinction is the billing direction.
	comps := []component{
		{parent.RequestCost, false, true},
		{parent.ResponseCost, false, false},
	}
	for _, sub := range parent.SubCalls {
		comps = append(comps, component{sub.RequestCost, true, true},
			component{sub.ResponseCost, true, false})
	}

	basis := CostBasis("")
	baselineOut, baselineIn, proxyOut, proxyIn := 0, 0, 0, 0
	// Any truncation anywhere in the call withholds it — see
	// ReasonTruncatedSubCallOverstates. Checked before the components are
	// summed so no partial total is ever built.
	truncatedAnywhere := parent.Flags.Truncated
	for _, sub := range parent.SubCalls {
		if sub != nil && sub.Flags.Truncated {
			truncatedAnywhere = true
		}
	}
	if truncatedAnywhere {
		return CodeExecSaving{ParentID: out.ParentID, SubCalls: out.SubCalls,
			Basis: CostUnavailable, Reason: ReasonTruncatedSubCallOverstates}
	}
	for _, c := range comps {
		amount, ok := c.cost.amount()
		if !ok {
			reason := c.cost.Reason
			if reason == "" {
				reason = ReasonNoByteCounts
			}
			return CodeExecSaving{ParentID: out.ParentID, SubCalls: out.SubCalls,
				Basis: CostUnavailable, Reason: reason}
		}
		if basis == "" {
			basis = c.cost.Basis
		} else if basis != c.cost.Basis {
			return CodeExecSaving{ParentID: out.ParentID, SubCalls: out.SubCalls,
				Basis: CostUnavailable, Reason: ReasonMixedCostBasis}
		}
		// A truncated component's byte length is the FULL pre-truncation size,
		// but the baseline means "what an agent would have paid making this
		// call itself" and that agent receives a cut response. Counting the
		// full size overstates the baseline, and an overstated baseline
		// inflates the saving — so withhold rather than annotate. Annotating
		// leaves the wrong number in the headline with a footnote beside it.
		if c.cost.Truncated {
			return CodeExecSaving{ParentID: out.ParentID, SubCalls: out.SubCalls,
				Basis: CostUnavailable, Reason: ReasonTruncatedSubCallOverstates}
		}
		switch {
		case c.child && c.generated:
			baselineOut += amount
		case c.child:
			baselineIn += amount
		case c.generated:
			proxyOut += amount
		default:
			proxyIn += amount
		}
	}

	out.Basis = basis
	out.BaselineOutput, out.BaselineInput = baselineOut, baselineIn
	out.ProxyOutput, out.ProxyInput = proxyOut, proxyIn
	out.Baseline = baselineOut + baselineIn
	out.Proxy = proxyOut + proxyIn
	out.Saving = out.Baseline - out.Proxy
	return out
}

// CodeExecReport aggregates response-side savings, bucketed BY BASIS so a
// measured total and an estimated one are never added together.
type CodeExecReport struct {
	// Buckets is keyed by basis: "measured" totals are tokens, "estimated"
	// totals are bytes.
	Buckets map[CostBasis]*CodeExecBucket `json:"buckets"`

	// Withheld counts the calls that produced no figure, by reason.
	Withheld map[ExclusionReason]int `json:"withheld,omitempty"`

	// Savings is the per-call detail, in input order.
	Savings []CodeExecSaving `json:"savings,omitempty"`
}

// CodeExecBucket is the total for one accounting basis.
type CodeExecBucket struct {
	Calls    int `json:"calls"`
	SubCalls int `json:"sub_calls"`
	Baseline int `json:"baseline"`
	Proxy    int `json:"proxy"`
	Saving   int `json:"saving"`
}

// CodeExecSavingsFor walks sessions and reports the response-side saving of
// every code_execution call in them.
func CodeExecSavingsFor(sessions []*ReplaySession) *CodeExecReport {
	rep := &CodeExecReport{Buckets: map[CostBasis]*CodeExecBucket{}}
	for _, session := range sessions {
		if session == nil {
			continue
		}
		// Only top-level calls: a code_execution never nests inside another
		// sandbox, and walking AllCalls would revisit sub-calls as parents.
		for _, call := range session.Calls {
			if call == nil || call.ToolName != CodeExecToolName {
				continue
			}
			saving := CodeExecSavingFor(call)
			rep.Savings = append(rep.Savings, saving)
			if saving.Basis == CostUnavailable {
				if rep.Withheld == nil {
					rep.Withheld = map[ExclusionReason]int{}
				}
				rep.Withheld[saving.Reason]++
				continue
			}
			bucket := rep.Buckets[saving.Basis]
			if bucket == nil {
				bucket = &CodeExecBucket{}
				rep.Buckets[saving.Basis] = bucket
			}
			bucket.Calls++
			bucket.SubCalls += saving.SubCalls
			bucket.Baseline += saving.Baseline
			bucket.Proxy += saving.Proxy
			bucket.Saving += saving.Saving
		}
	}
	return rep
}

// PricedSavingUSD converts the saving to money, which is the only form in which
// the input/output asymmetry is visible.
//
// inPerMTok and outPerMTok are USD per million tokens; cacheMult scales the
// INPUT side only (0.1 for a cached prefix, 1.0 for uncached). It returns
// ok=false rather than a number whenever pricing would be dishonest:
//
//   - a withheld saving has no figures at all, and 0.0 would read as "code
//     execution was free" instead of "nothing was measurable";
//   - an ESTIMATED saving is BYTE lengths, not tokens, so pricing it per-token
//     would be wrong by roughly the bytes-per-token ratio and would look
//     entirely plausible.
func (s CodeExecSaving) PricedSavingUSD(inPerMTok, outPerMTok, cacheMult float64) (float64, bool) {
	if s.Basis != CostMeasured {
		return 0, false
	}
	// Reject prices that cannot produce a meaningful figure. NaN and +/-Inf
	// propagate silently through the arithmetic and would be reported as a
	// confident dollar amount; a negative price is not a price. ok=false is the
	// same answer this function gives for anything it cannot honestly compute.
	for _, v := range []float64{inPerMTok, outPerMTok, cacheMult} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, false
		}
	}
	cost := func(outTok, inTok int) float64 {
		return (float64(outTok)*outPerMTok + float64(inTok)*inPerMTok*cacheMult) / 1e6
	}
	return cost(s.BaselineOutput, s.BaselineInput) - cost(s.ProxyOutput, s.ProxyInput), true
}
