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

import (
	"fmt"
	"math"
)

// CodeExecToolName is the built-in whose activity records parent a sandbox's
// sub-calls. Sub-calls join to it by parent_id (see group.go).
const CodeExecToolName = "code_execution"

// BaselineMode selects WHICH counterfactual the word "baseline" names. The two
// are not refinements of one another: they answer different questions and
// legitimately produce different numbers from the same log. Before this type
// existed the accounting implemented BaselineProxy and never said so, which is
// the defect it fixes — a saving whose denominator is unstated cannot be
// quoted.
//
// It is a string type for the same reason CostBasis and ExclusionReason are: a
// report prints it verbatim and rows must sort deterministically (SC-002).
type BaselineMode string

const (
	// BaselineDirect is the agent wired STRAIGHT to the upstream MCP servers,
	// with mcpproxy nowhere in the path. Nothing truncates, so a sub-call's
	// full pre-truncation response_bytes is exactly what that agent reads —
	// the recorded truncation is an artefact of the RECORD
	// (subCallActivityResponseLimit cuts the stored string at 8KB while
	// subCallByteSizes measures the whole result), not of the workload.
	//
	// NAME COLLISION, read this before using the word: internal/config's
	// RoutingModeDirect is ALSO "direct" and means the opposite kind of thing
	// — every upstream tool exposed THROUGH the proxy, where responses ARE cut
	// at ToolResponseLimit. Never print this value bare; always gloss it as
	// "no proxy in the path".
	//
	// It is the DEFAULT, and that is a deliberate change of published meaning.
	// tool_response_limit is an MCPPROXY feature: charging the counterfactual
	// agent for it answers "what did code execution save over call_tool_*"
	// rather than "what did mcpproxy save me". It also brings this figure into
	// line with the harness's own denominator — bench/modematrix.go's
	// baselineCell, "the same agent doing the same tasks with every upstream
	// tool inline", which every other published percentage here is measured
	// against. And the withhold it replaces is not statistically neutral: it
	// fires precisely on the LARGEST responses, the ones with most to save, so
	// the surviving population was biased small in a way no exclusion counter
	// showed as a magnitude.
	BaselineDirect BaselineMode = "direct"

	// BaselineProxy is the agent making the same calls through THIS proxy's
	// call_tool_* path, where each content block is cut at
	// config.ToolResponseLimit (20000 by default) before the model reads it.
	// Counting the full pre-truncation size then overstates the baseline, and
	// an overstated baseline inflates the saving — the one direction of error a
	// savings figure must never make — so a truncated sub-call withholds the
	// whole call. This is the behaviour that existed before the selector, bit
	// for bit.
	BaselineProxy BaselineMode = "proxy"
)

// orDefault maps the zero value onto the default in ONE place, so no branch
// downstream has to decide what "" means. An unknown non-empty value also
// resolves to BaselineDirect: a bench library must not panic mid-report. The
// CLI refuses a typo instead of resolving it — see ParseBaselineMode.
func (m BaselineMode) orDefault() BaselineMode {
	if m == BaselineProxy {
		return BaselineProxy
	}
	return BaselineDirect
}

// ParseBaselineMode turns operator input into the vocabulary, or refuses. Exact
// match only: no case folding, no trimming, no prefix. A typo'd -baseline that
// quietly produced the OTHER figure is the exact failure this change exists to
// remove, so "" is rejected too.
func ParseBaselineMode(s string) (BaselineMode, error) {
	switch BaselineMode(s) {
	case BaselineDirect:
		return BaselineDirect, nil
	case BaselineProxy:
		return BaselineProxy, nil
	default:
		return "", fmt.Errorf("unknown baseline %q: want %q (agent talks to the upstream servers itself, no proxy in the path) or %q (agent calls through this proxy, responses cut at tool_response_limit)",
			s, BaselineDirect, BaselineProxy)
	}
}

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

	// Mode is the counterfactual that produced this figure, stamped on EVERY
	// return path including a withheld one — a withheld line still has to say
	// which baseline refused it, since proxy refuses calls direct accepts. No
	// omitempty: a figure whose denominator is optional is the ambiguity being
	// removed.
	Mode BaselineMode `json:"baseline_mode"`

	// TruncatedSubCalls counts the baseline components that contributed ONLY
	// because BaselineDirect tolerates truncation; BaselineProxy withholds the
	// whole call instead, so it is always 0 there. doc.go's third invariant
	// lets a truncated record contribute but never SILENTLY: this counter and
	// TruncatedBaseline are that annotation, and direct mode is outside the
	// invariant without them.
	TruncatedSubCalls int `json:"truncated_sub_calls,omitempty"`

	// TruncatedBaseline is how much of Baseline those components contributed,
	// in the unit Basis implies. The count alone cannot answer the only
	// question worth asking of a tolerated figure — how much of this rests on
	// data the conservative mode would have thrown away.
	TruncatedBaseline int `json:"truncated_baseline,omitempty"`
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
//
// mode selects the counterfactual the baseline names; the zero value is
// BaselineDirect. The parameter is deliberately required rather than carried on
// Options or inferred from CostBasis: a caller that recompiled clean and
// silently reported the OTHER number is the whole risk this selector exists to
// remove, and reporting one loaded corpus both ways — without a second,
// bodies-on, unmasked read — is the affordance that matters.
func CodeExecSavingFor(parent *ReplayCall, mode BaselineMode) CodeExecSaving {
	mode = mode.orDefault()
	out := CodeExecSaving{Mode: mode}
	if parent == nil {
		return CodeExecSaving{Mode: mode, Basis: CostUnavailable, Reason: ReasonNotACall}
	}
	out.ParentID = parent.RequestID
	out.SubCalls = len(parent.SubCalls)
	if parent.ToolName != CodeExecToolName {
		// Deliberately not routed through withheld(): a not-a-call figure
		// reports SubCalls: 0, because counting the sub-calls of something that
		// is not a code_execution call describes nothing.
		return CodeExecSaving{ParentID: out.ParentID, Mode: mode,
			Basis: CostUnavailable, Reason: ReasonNotACall}
	}
	withheld := func(reason ExclusionReason) CodeExecSaving {
		return CodeExecSaving{ParentID: out.ParentID, SubCalls: out.SubCalls,
			Mode: mode, Basis: CostUnavailable, Reason: reason}
	}

	// Collect every component first so a single pass can decide the basis.
	// The parent's own request is the script source and its response is the
	// value the model received; both are costs code execution really incurred.
	type component struct {
		cost      Cost
		child     bool
		generated bool
		// record is the sub-call record's own Flags.Truncated. Kept beside the
		// Cost so the tolerated set under BaselineDirect is EXACTLY the set
		// BaselineProxy withholds on: a record flagged truncated whose Cost
		// happens to carry no Truncated marker would otherwise contribute with
		// no annotation, which is the silence doc.go's third rule forbids.
		record bool
	}
	// generated marks a component the MODEL WROTE — a call's arguments — as
	// opposed to one it read. The distinction is the billing direction.
	comps := []component{
		{parent.RequestCost, false, true, false},
		{parent.ResponseCost, false, false, false},
	}
	for _, sub := range parent.SubCalls {
		// Guarded HERE because this is the first dereference: the nil check
		// further down used to be the only one, which made it unreachable and
		// gave a false sense that nil entries were handled.
		if sub == nil {
			continue
		}
		comps = append(comps, component{sub.RequestCost, true, true, sub.Flags.Truncated},
			component{sub.ResponseCost, true, false, sub.Flags.Truncated})
	}

	basis := CostBasis("")
	baselineOut, baselineIn, proxyOut, proxyIn := 0, 0, 0, 0
	truncatedSubCalls, truncatedBaseline := 0, 0

	// A truncated PARENT withholds in BOTH modes, and the reason has nothing to
	// do with which agent we are comparing against. The parent's response is
	// the PROXY term — what the model actually read — and mcpproxy really did
	// cut it at ToolResponseLimit (forwardContentResult) while response_bytes
	// records the full pre-truncation size. Counting the full size overstates
	// the proxy term and so UNDERSTATES the saving: conservative, still wrong,
	// and no baseline choice repairs it. (Unreachable on real data:
	// code_execution emits through emitActivityInternalToolCall, which passes
	// responseTruncated=false, and a truncated internal record is already
	// withheld by responseCost as ReasonTruncatedRetrieveOverstates — the
	// OPPOSITE direction, where the log stores more than the agent consumed.)
	if parent.Flags.Truncated {
		return withheld(ReasonTruncatedSubCallOverstates)
	}
	// A truncated SUB-CALL is the only place the two counterfactuals disagree.
	// Under BaselineProxy the agent's own call would have been cut at
	// ToolResponseLimit, so the full size overstates the baseline and the whole
	// call is withheld before any component is summed — no partial total is
	// ever built. Under BaselineDirect nothing truncates and the full size is
	// exactly what that agent reads, so the loop runs and annotates.
	//
	// Consequence for anyone diffing two runs: because this short-circuit is
	// gone under direct, a call that reported truncated_sub_call_overstates_saving
	// may now report mixed_cost_basis or sub_call_zero_bytes — the reason it was
	// always going to hit second. WITHHELD-BY-REASON TALLIES ARE NOT COMPARABLE
	// ACROSS MODES, and a reason that moved represents no change in the data.
	if mode == BaselineProxy {
		for _, sub := range parent.SubCalls {
			if sub != nil && sub.Flags.Truncated {
				return withheld(ReasonTruncatedSubCallOverstates)
			}
		}
	}
	for _, c := range comps {
		amount, ok := c.cost.amount()
		if !ok {
			reason := c.cost.Reason
			if reason == "" {
				reason = ReasonNoByteCounts
			}
			return withheld(reason)
		}
		if basis == "" {
			basis = c.cost.Basis
		} else if basis != c.cost.Basis {
			return withheld(ReasonMixedCostBasis)
		}
		// Truncation only ever cuts a RESPONSE — requestCost never sets
		// Truncated (flags.go) — so charging the request side of a truncated
		// record would count one cut response twice and overstate how much of
		// this figure is soft.
		truncated := !c.generated && (c.cost.Truncated || c.record)
		if truncated {
			// The parent's own components are the proxy term: withheld in both
			// modes, for the reason the pre-pass states. A sub-call's are the
			// baseline term, and under BaselineDirect they are counted at their
			// FULL pre-truncation byte length — which is trustworthy only
			// because the sandbox truncation is record-only
			// (subCallActivityResponseLimit cuts the stored string; the value
			// the sandbox received and subCallByteSizes measured was whole).
			// Never substitute the stored TEXT here: it is capped at 8KB and
			// would understate.
			//
			// The mode is read HERE and nowhere else. responseCost takes no
			// mode on purpose — the counterfactual is a scoring choice, not a
			// classification one, and doc.go's classify-once rule forbids
			// re-deriving a flag downstream. Do not "finish the job" by
			// threading it into the loader.
			if !c.child || mode == BaselineProxy {
				return withheld(ReasonTruncatedSubCallOverstates)
			}
			truncatedSubCalls++
			truncatedBaseline += amount
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
	out.TruncatedSubCalls, out.TruncatedBaseline = truncatedSubCalls, truncatedBaseline
	return out
}

// CodeExecReport aggregates response-side savings, bucketed BY BASIS so a
// measured total and an estimated one are never added together.
type CodeExecReport struct {
	// Mode is the counterfactual every figure below is measured against. It is
	// on the report as well as on each saving so a report read alone still
	// states its own denominator.
	Mode BaselineMode `json:"baseline_mode"`

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

	// How much of Baseline above rests on components BaselineProxy would have
	// thrown away. Always 0 under BaselineProxy. A total that tolerates
	// truncation has to carry the magnitude beside it, not just a count — see
	// CodeExecSaving.TruncatedBaseline.
	TruncatedSubCalls int `json:"truncated_sub_calls,omitempty"`
	TruncatedBaseline int `json:"truncated_baseline,omitempty"`

	// TruncatedCalls and TruncatedCallSaving are the WHOLE-CALL figures, and
	// they are the ones to quote when asking "what does the mode change cost
	// me in confidence". TruncatedBaseline above is only the truncated
	// COMPONENT's contribution, but BaselineProxy does not drop a component —
	// it drops the entire call, its untruncated sub-calls and its proxy term
	// with it. Reporting only the component magnitude therefore understates
	// how much of the published total exists solely because direct mode
	// tolerated truncation.
	TruncatedCalls      int `json:"truncated_calls,omitempty"`
	TruncatedCallSaving int `json:"truncated_call_saving,omitempty"`
}

// CodeExecSavingsFor walks sessions and reports the response-side saving of
// every code_execution call in them, against the counterfactual mode names (the
// zero value is BaselineDirect).
func CodeExecSavingsFor(sessions []*ReplaySession, mode BaselineMode) *CodeExecReport {
	mode = mode.orDefault()
	rep := &CodeExecReport{Mode: mode, Buckets: map[CostBasis]*CodeExecBucket{}}
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
			saving := CodeExecSavingFor(call, mode)
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
			bucket.TruncatedSubCalls += saving.TruncatedSubCalls
			bucket.TruncatedBaseline += saving.TruncatedBaseline
			if saving.TruncatedSubCalls > 0 {
				bucket.TruncatedCalls++
				bucket.TruncatedCallSaving += saving.Saving
			}
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
//
//   - an ESTIMATED saving is BYTE lengths, not tokens, so pricing it per-token
//     would be wrong by roughly the bytes-per-token ratio and would look
//     entirely plausible.
//
//   - a saving that TOLERATED a truncated sub-call is not priced, whichever
//     mode produced it. Today that is belt-and-braces: responseCost refuses to
//     tokenize cut text, so such a saving is CostEstimated and the basis guard
//     above already rejects it. That is a property of the current loader, not
//     of this function, and a future path that tokenized a partially-cut
//     response would silently start pricing tolerated data. The rule money
//     obeys is stated here and ENFORCED here rather than inherited from a
//     coincidence two files away.
func (s CodeExecSaving) PricedSavingUSD(inPerMTok, outPerMTok, cacheMult float64) (float64, bool) {
	if s.Basis != CostMeasured {
		return 0, false
	}
	if s.TruncatedSubCalls > 0 {
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
