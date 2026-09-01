package replaycorpus

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func measured(tokens int) Cost { return Cost{Basis: CostMeasured, Tokens: tokens, Bytes: tokens * 4} }
func estimated(bytes int) Cost { return Cost{Basis: CostEstimated, Bytes: bytes} }
func unavailable() Cost        { return Cost{Basis: CostUnavailable, Reason: ReasonSubCallZeroBytes} }

func codeExecCall(req, resp Cost, subs ...*ReplayCall) *ReplayCall {
	return &ReplayCall{
		RequestID: "parent-1", ToolName: CodeExecToolName, Internal: true,
		RequestCost: req, ResponseCost: resp, SubCalls: subs,
	}
}

func subCall(req, resp Cost) *ReplayCall {
	return &ReplayCall{RequestID: "sub", ParentID: "parent-1", RequestCost: req, ResponseCost: resp}
}

// The headline case: three sandbox sub-calls whose responses never reached the
// model, against the script sent and the one result returned.
func TestCodeExecSaving_MeasuresTheResponseSideDifference(t *testing.T) {
	parent := codeExecCall(measured(120), measured(80),
		subCall(measured(30), measured(900)),
		subCall(measured(25), measured(1500)),
		subCall(measured(40), measured(600)),
	)

	got := CodeExecSavingFor(parent)

	require.Equal(t, CostMeasured, got.Basis)
	assert.Equal(t, 3, got.SubCalls)
	assert.Equal(t, 30+900+25+1500+40+600, got.Baseline, "what the agent would have paid itself")
	assert.Equal(t, 120+80, got.Proxy, "the script it sent plus the result it got")
	assert.Equal(t, 3095-200, got.Saving)
}

// A script that dispatches nothing costs tokens and returns none. That is a
// real loss and must be reported as one rather than filtered out or floored.
func TestCodeExecSaving_NoSubCallsIsARealNegative(t *testing.T) {
	got := CodeExecSavingFor(codeExecCall(measured(500), measured(20)))

	require.Equal(t, CostMeasured, got.Basis)
	assert.Equal(t, 0, got.SubCalls)
	assert.Equal(t, 0, got.Baseline)
	assert.Equal(t, -520, got.Saving, "a script that dispatched nothing lost 520 tokens")
}

// Measured figures are tokens, estimated ones are bytes. Summing them would
// produce a confident number in no unit at all.
func TestCodeExecSaving_NeverSumsAcrossBases(t *testing.T) {
	parent := codeExecCall(measured(100), measured(50),
		subCall(measured(10), estimated(4000)),
	)

	got := CodeExecSavingFor(parent)

	require.Equal(t, CostUnavailable, got.Basis)
	assert.Equal(t, ReasonMixedCostBasis, got.Reason)
	assert.Zero(t, got.Saving, "an unavailable saving carries no number")
	assert.Zero(t, got.Baseline)
}

// A sub-call with no honest figure understates the baseline, which flatters
// code execution. Withhold instead.
func TestCodeExecSaving_UnavailableComponentWithholdsTheWhole(t *testing.T) {
	parent := codeExecCall(measured(100), measured(50),
		subCall(measured(10), measured(900)),
		subCall(measured(10), unavailable()),
	)

	got := CodeExecSavingFor(parent)

	require.Equal(t, CostUnavailable, got.Basis)
	assert.Equal(t, ReasonSubCallZeroBytes, got.Reason, "the reason must survive to the caller")
	assert.Zero(t, got.Saving)
}

// An all-estimated call is computable, in BYTES, and says so.
func TestCodeExecSaving_EstimatedBasisIsBytes(t *testing.T) {
	parent := codeExecCall(estimated(400), estimated(200),
		subCall(estimated(100), estimated(5000)),
	)

	got := CodeExecSavingFor(parent)

	require.Equal(t, CostEstimated, got.Basis)
	assert.Equal(t, 5100-600, got.Saving)
}

// Superseded by TestCodeExecSaving_TruncatedSubCallWithholdsRatherThanInflates.
// This previously asserted that truncation merely set an annotation flag while
// the saving was still computed and published — which is exactly the defect
// cross-model review caught, so the assertion is inverted rather than dropped.
func TestCodeExecSaving_TruncationWithholds(t *testing.T) {
	sub := subCall(measured(10), Cost{Basis: CostMeasured, Tokens: 900, Truncated: true})
	got := CodeExecSavingFor(codeExecCall(measured(100), measured(50), sub))
	require.Equal(t, CostUnavailable, got.Basis, "a truncated sub-call understates nothing — it OVERstates the baseline")
	assert.Equal(t, ReasonTruncatedSubCallOverstates, got.Reason)

	flagged := subCall(measured(10), measured(900))
	flagged.Flags.Truncated = true
	got2 := CodeExecSavingFor(codeExecCall(measured(100), measured(50), flagged))
	assert.Equal(t, CostUnavailable, got2.Basis, "the call-level flag must withhold too")

	parentCut := codeExecCall(measured(100), measured(50), subCall(measured(10), measured(900)))
	parentCut.Flags.Truncated = true
	assert.Equal(t, CostUnavailable, CodeExecSavingFor(parentCut).Basis,
		"a truncated PARENT understates the proxy cost, which also inflates the saving")
}

func TestCodeExecSaving_RejectsNonCodeExecParents(t *testing.T) {
	other := &ReplayCall{RequestID: "x", ToolName: "retrieve_tools", RequestCost: measured(1), ResponseCost: measured(1)}
	got := CodeExecSavingFor(other)
	require.Equal(t, CostUnavailable, got.Basis)
	assert.Equal(t, ReasonNotACall, got.Reason)

	assert.Equal(t, CostUnavailable, CodeExecSavingFor(nil).Basis)
}

// The aggregate must bucket by basis rather than producing one blended total,
// and must never silently drop the calls it could not measure.
func TestCodeExecSavingsFor_BucketsByBasisAndCountsWithheld(t *testing.T) {
	sessions := []*ReplaySession{{
		WorkSessionID: "w1",
		Calls: []*ReplayCall{
			codeExecCall(measured(100), measured(50), subCall(measured(10), measured(900))),
			codeExecCall(estimated(400), estimated(200), subCall(estimated(100), estimated(5000))),
			codeExecCall(measured(100), measured(50), subCall(measured(10), unavailable())),
			{RequestID: "rt", ToolName: "retrieve_tools", RequestCost: measured(5), ResponseCost: measured(5)},
		},
	}}

	rep := CodeExecSavingsFor(sessions)

	require.Len(t, rep.Buckets, 2, "measured and estimated must stay separate")
	assert.Equal(t, 760, rep.Buckets[CostMeasured].Saving)
	assert.Equal(t, 4500, rep.Buckets[CostEstimated].Saving)
	assert.Equal(t, 1, rep.Buckets[CostMeasured].Calls, "the unavailable call must not land in a bucket")
	assert.Equal(t, 1, rep.Withheld[ReasonSubCallZeroBytes])
	assert.Len(t, rep.Savings, 3, "retrieve_tools is not a code_execution call")
}

// Sub-calls hang off their parent, never in Calls. If the aggregate ever walked
// AllCalls it would treat each sub-call as a parent of its own.
func TestCodeExecSavingsFor_DoesNotRevisitSubCalls(t *testing.T) {
	sub := subCall(measured(10), measured(900))
	sub.ToolName = CodeExecToolName // adversarial: a sub-call that looks like a parent
	sessions := []*ReplaySession{{
		WorkSessionID: "w1",
		Calls:         []*ReplayCall{codeExecCall(measured(100), measured(50), sub)},
		CallCount:     1, SubCallCount: 1,
	}}

	rep := CodeExecSavingsFor(sessions)

	assert.Len(t, rep.Savings, 1, "exactly one code_execution call was made")
	assert.Equal(t, 760, rep.Buckets[CostMeasured].Saving)
}

// REGRESSION GUARD for the emission change that accompanies this file.
//
// Internal built-in records used to carry NO byte lengths at all, so a
// truncated retrieve_tools was withheld simply because there was nothing to
// count. Now that the internal emission populates request_bytes/response_bytes,
// that accidental protection is gone and only the explicit truncation branch
// remains. It must still fire: for a built-in the log stores the FULL response
// while the agent consumed the CUT text, so the pre-truncation byte length
// describes something LARGER than what was paid for, and counting it would
// OVERSTATE mcpproxy's own cost.
func TestResponseCost_TruncatedInternalStillWithheldNowThatBytesExist(t *testing.T) {
	rep := &ExclusionReport{}
	rec := &decodedRecord{
		toolName: "retrieve_tools", internal: true, truncated: true,
		responseBytes: 48_000, // present now, and deliberately large
		response:      "full pre-truncation text the agent never saw",
	}

	got := responseCost(rec, false, &Options{Bodies: BodiesOff}, func(string) int { return 1 }, rep)

	require.Equal(t, CostUnavailable, got.Basis,
		"a truncated built-in must stay withheld even though it now has a byte length")
	assert.Equal(t, ReasonTruncatedRetrieveOverstates, got.Reason)
	assert.Zero(t, got.Bytes, "an unavailable cost carries no number")
	assert.Equal(t, 1, rep.Withheld[ReasonTruncatedRetrieveOverstates])
}

// The other half: an UNtruncated built-in that now has byte lengths becomes
// accountable, where before it was withheld as ReasonInternalNoByteCounts. That
// is the intended gain, so pin it — otherwise a future revert is invisible.
func TestResponseCost_UntruncatedInternalIsNowAccountable(t *testing.T) {
	rep := &ExclusionReport{}
	rec := &decodedRecord{toolName: "retrieve_tools", internal: true, responseBytes: 4096}

	got := responseCost(rec, false, &Options{Bodies: BodiesOff}, func(string) int { return 1 }, rep)

	require.Equal(t, CostEstimated, got.Basis)
	assert.Equal(t, 4096, got.Bytes)
	assert.Empty(t, rep.Withheld[ReasonInternalNoByteCounts],
		"the byte-gap that motivated the emission change should no longer fire")
}

// A PARAMETERLESS tool call records no arguments at all. With bodies on that is
// not a recording gap — the record exists and its byte length is the serialized
// empty object — so the cost is KNOWN and must be measured like its siblings.
//
// Before this, such a call fell through to the byte-length estimate while every
// tool that DID take arguments was measured, so the two bases collided and any
// aggregate containing a parameterless tool was withheld entirely. Found on real
// traffic: filesystem:list_allowed_directories and memory:read_graph poisoned a
// whole code_execution whose third sub-call was measured normally.
func TestRequestCost_ParameterlessCallIsMeasuredNotEstimated(t *testing.T) {
	opts := &Options{Bodies: BodiesOnUnmasked}
	count := func(s string) int { return len(s) }

	parameterless := &decodedRecord{toolName: "read_graph", requestBytes: 2} // "{}"
	got := requestCost(parameterless, true, opts, count)
	require.Equal(t, CostMeasured, got.Basis,
		"an empty argument set is known exactly, not estimated")
	assert.Equal(t, 2, got.Bytes)

	withArgs := requestCost(&decodedRecord{toolName: "list_directory",
		arguments: `{"path":"/tmp"}`, requestBytes: 15}, true, opts, count)
	require.Equal(t, CostMeasured, withArgs.Basis)

	assert.Equal(t, got.Basis, withArgs.Basis,
		"siblings in one script must share a basis or the aggregate is withheld")
}

// The rule must NOT swallow a genuine recording gap. Empty arguments beside a
// LARGE byte length means content really is missing, and claiming a measured
// figure there would invent one.
func TestRequestCost_EmptyArgsWithLargePayloadStaysEstimated(t *testing.T) {
	opts := &Options{Bodies: BodiesOnUnmasked}
	count := func(s string) int { return len(s) }

	got := requestCost(&decodedRecord{toolName: "x", requestBytes: 4096}, true, opts, count)
	assert.Equal(t, CostEstimated, got.Basis,
		"4096 bytes of arguments that are not present is a gap, not a parameterless call")

	none := requestCost(&decodedRecord{toolName: "x"}, true, opts, count)
	assert.Equal(t, CostUnavailable, none.Basis, "no bytes and no body is still unknown")
}

// A truncated SUB-CALL must withhold the whole saving, not merely annotate it.
//
// Found by cross-model review. The sandbox stores at most 8KB of a sub-call's
// response (subCallActivityResponseLimit) while response_bytes records the FULL
// pre-truncation size. Baseline means "what an agent would have paid making this
// call itself", and an agent calling through call_tool_* receives a response cut
// to ToolResponseLimit — so charging the baseline the full size overstates it,
// and overstating the baseline INFLATES the claimed saving. That is the one
// direction of error a savings figure must never make.
//
// With bodies on this was already caught indirectly: the truncated component
// falls to an estimate while its siblings are measured, and the never-sum guard
// withholds. With bodies OFF everything is estimated, the bases agree, and the
// inflated number went straight into the total.
func TestCodeExecSaving_TruncatedSubCallWithholdsRatherThanInflates(t *testing.T) {
	huge := subCall(estimated(40), Cost{Basis: CostEstimated, Bytes: 250_000, Truncated: true})
	parent := codeExecCall(estimated(400), estimated(200),
		subCall(estimated(50), estimated(900)),
		huge,
	)

	got := CodeExecSavingFor(parent)

	require.Equal(t, CostUnavailable, got.Basis,
		"the full pre-truncation size would inflate the baseline and so the saving")
	assert.Equal(t, ReasonTruncatedSubCallOverstates, got.Reason)
	assert.Zero(t, got.Saving)
	assert.Zero(t, got.Baseline)
}

// The aggregate must not carry an inflated total either — the withheld call is
// counted under its reason and contributes nothing to any bucket.
func TestCodeExecSavingsFor_TruncatedSubCallNeverReachesTheTotal(t *testing.T) {
	clean := codeExecCall(estimated(400), estimated(200), subCall(estimated(50), estimated(900)))
	dirty := codeExecCall(estimated(400), estimated(200),
		subCall(estimated(40), Cost{Basis: CostEstimated, Bytes: 250_000, Truncated: true}))

	rep := CodeExecSavingsFor([]*ReplaySession{{WorkSessionID: "w", Calls: []*ReplayCall{clean, dirty}}})

	require.NotNil(t, rep.Buckets[CostEstimated])
	assert.Equal(t, 1, rep.Buckets[CostEstimated].Calls)
	assert.Equal(t, 950-600, rep.Buckets[CostEstimated].Saving,
		"only the clean call contributes; 250k must not appear in the total")
	assert.Equal(t, 1, rep.Withheld[ReasonTruncatedSubCallOverstates])
}

// Tool-call ARGUMENTS are tokens the model generated (output, billed ~5x) and
// RESPONSES are tokens it reads (input, billed 1x, or 0.1x cached). Summing them
// into one "tokens saved" figure prices a 5x asymmetry at 1x.
//
// This is the case that proves it matters, taken from measured traffic: at three
// sub-calls the token count says +262 saved while the money says LOSS, because
// code execution trades cheap input for expensive output — the script is a fixed
// cost regardless of how many calls it replaces.
func TestCodeExecSaving_SplitsByBillingDirection(t *testing.T) {
	parent := codeExecCall(measured(78), measured(11), // script out, result in
		subCall(measured(12), measured(105)),
		subCall(measured(12), measured(105)),
		subCall(measured(12), measured(105)),
	)

	got := CodeExecSavingFor(parent)

	require.Equal(t, CostMeasured, got.Basis)
	assert.Equal(t, 36, got.BaselineOutput, "three calls of arguments the model would have written")
	assert.Equal(t, 315, got.BaselineInput, "three responses it would have read")
	assert.Equal(t, 78, got.ProxyOutput, "the script it wrote instead")
	assert.Equal(t, 11, got.ProxyInput)

	assert.Equal(t, got.BaselineOutput+got.BaselineInput, got.Baseline, "the totals must stay consistent")
	assert.Equal(t, got.ProxyOutput+got.ProxyInput, got.Proxy)
	assert.Equal(t, 262, got.Saving, "the raw token count is positive...")

	// ...and the money is negative once output is priced at 5x and input is cached.
	priced, ok := got.PricedSavingUSD(3.0, 15.0, 0.1)
	require.True(t, ok)
	assert.Negative(t, priced,
		"+262 tokens is a LOSS in money: 42 extra output tokens outweigh 304 cached input tokens")

	uncached, _ := got.PricedSavingUSD(3.0, 15.0, 1.0)
	assert.Positive(t, uncached, "without caching the same call is a small win — caching moves break-even right")
}

// A withheld saving has no numbers to price, and must not return a confident 0.
func TestCodeExecSaving_PricedSavingRefusesWithheldFigures(t *testing.T) {
	parent := codeExecCall(measured(100), measured(50), subCall(measured(10), unavailable()))
	got := CodeExecSavingFor(parent)
	require.Equal(t, CostUnavailable, got.Basis)

	_, ok := got.PricedSavingUSD(3.0, 15.0, 0.1)
	assert.False(t, ok, "no measurement means no price, not a free call")
}

// An ESTIMATED saving is bytes, not tokens. Pricing bytes per-token would be
// wrong by roughly the bytes-per-token ratio and would look entirely plausible.
func TestCodeExecSaving_PricedSavingRefusesByteEstimates(t *testing.T) {
	parent := codeExecCall(estimated(400), estimated(200), subCall(estimated(100), estimated(5000)))
	got := CodeExecSavingFor(parent)
	require.Equal(t, CostEstimated, got.Basis)

	_, ok := got.PricedSavingUSD(3.0, 15.0, 0.1)
	assert.False(t, ok, "byte lengths cannot be priced as tokens")
}

// A stored-script invocation sends a NAME, not source. mcpproxy records the
// resolved source under "code" too, so the audit trail shows what ran — but
// billing that source as model-generated output charges ~5,200 tokens per run
// for a request the model wrote in ~17. Found by cross-model review, and it hits
// the exact configuration stored scripts exist to make cheap.
func TestBillableArguments_DropsServerResolvedScriptSource(t *testing.T) {
	stored := map[string]interface{}{
		"script": "jira-triage",
		"input":  map[string]interface{}{"board": "GCLOUD2"},
		"code":   "const huge = 'twenty kilobytes of resolved source';",
	}
	billable, resolved := billableArguments(stored)
	require.True(t, resolved, "the record carried content the model did not send")
	assert.NotContains(t, billable, "code", "the model never generated the source")
	assert.Equal(t, "jira-triage", billable["script"], "it did send the name")
	assert.Contains(t, billable, "input")
	assert.Contains(t, stored, "code", "the caller's map must not be mutated")

	// An INLINE call genuinely sent its source and must keep it.
	inline := map[string]interface{}{"code": "call_tool('a','b',{})", "language": "javascript"}
	back, resolved2 := billableArguments(inline)
	assert.False(t, resolved2)
	assert.Contains(t, back, "code", "an inline call really did generate this")

	// A script name with no resolved source recorded: nothing to strip.
	nameOnly := map[string]interface{}{"script": "x"}
	_, resolved3 := billableArguments(nameOnly)
	assert.False(t, resolved3)

	// An empty script name is an inline call.
	_, resolved4 := billableArguments(map[string]interface{}{"script": "", "code": "x"})
	assert.False(t, resolved4)
}

// NaN, infinity and negative prices propagate silently through the arithmetic
// and would be reported as a confident dollar figure.
func TestCodeExecSaving_PricedSavingRejectsNonsensePrices(t *testing.T) {
	got := CodeExecSavingFor(codeExecCall(measured(78), measured(11),
		subCall(measured(12), measured(105))))
	require.Equal(t, CostMeasured, got.Basis)

	_, ok := got.PricedSavingUSD(3.0, 15.0, 0.1)
	require.True(t, ok, "the sane case must still price")

	for _, bad := range []struct {
		name              string
		in, out, cacheMul float64
	}{
		{"NaN cache multiplier", 3, 15, math.NaN()},
		{"infinite output price", 3, math.Inf(1), 0.1},
		{"NaN input price", math.NaN(), 15, 0.1},
		{"negative price", -3, 15, 0.1},
		{"negative multiplier", 3, 15, -0.1},
	} {
		_, ok := got.PricedSavingUSD(bad.in, bad.out, bad.cacheMul)
		assert.False(t, ok, "%s must be refused, not reported", bad.name)
	}
}
