package replaycorpus

import (
	"sort"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// ExclusionReason names one reason a record, or one cost component of a record,
// could not contribute to a replay figure. It is a CLOSED vocabulary and a
// string type so a report can print it verbatim and so rows sort
// deterministically — a replay report has to be byte-reproducible (SC-002), and
// a map's iteration order is not.
type ExclusionReason string

const (
	// ReasonNotACall marks a record that is not a tool call at all — a
	// quarantine change, a policy decision, a server change. The activity log
	// is wider than tool calls, so these arrive in every export and are
	// dropped rather than counted as zero-cost calls.
	ReasonNotACall ExclusionReason = "not_a_call"

	// ReasonMissingTool marks a call record with no tool name. It cannot be
	// resolved against any mode's tool surface, so it can carry no menu cost.
	ReasonMissingTool ExclusionReason = "missing_tool_name"

	// ReasonUnattributed marks a record with no work_session_id and no parent
	// to inherit one from. The work session is the unit of work for US1, so an
	// unattributed record belongs to no unit and is reported as such rather
	// than being folded into an arbitrary one.
	ReasonUnattributed ExclusionReason = "unattributed"

	// ReasonOrphanedSubCall marks a sub-call whose parent fell outside the
	// exported window AND which carries no work_session_id of its own, so
	// there is no unit of work left to attribute it to.
	//
	// It is separated from ReasonUnattributed on purpose. Both are records
	// with no session, but they tell the operator different things: an
	// unattributed record never had a session, whereas this one had a parent
	// that the EXPORT WINDOW cut off. The second is fixable — re-export with a
	// wider window and the record comes back — and folding it into the first
	// would hide the one exclusion reason the operator can actually act on.
	ReasonOrphanedSubCall ExclusionReason = "orphaned_sub_call"

	// ReasonTruncated marks a record whose stored response was cut at capture.
	// It is a FLAG, not a drop: the record still contributes its call shape and
	// an annotated byte-length estimate. It is counted so that no truncated
	// record can contribute without the report saying so.
	ReasonTruncated ExclusionReason = "truncated"

	// ReasonBodiesMissing marks a record loaded with bodies off — the default.
	// The content is absent, so no response cost is measurable, only estimated.
	ReasonBodiesMissing ExclusionReason = "bodies_missing"

	// ReasonSensitive marks a record carrying the sensitive-data flag. This is
	// a BEST-EFFORT reducer, never a guarantee: there is no persisted
	// sensitivity field, the flag is derived at export from detection metadata
	// added asynchronously AFTER the record is first persisted, so a freshly
	// exported record may be sensitive and not yet flagged. Anything relying on
	// it must say so (Principle IV, contracts/replay-input.md §4).
	ReasonSensitive ExclusionReason = "sensitive"

	// ReasonUnreplayable marks a call whose tool no longer resolves against the
	// supplied fleet. Only ever set when a fleet resolver was supplied — with
	// no fleet to resolve against, "unreplayable" would be an assertion the
	// loader has no basis for.
	ReasonUnreplayable ExclusionReason = "unreplayable"

	// ReasonNoByteCounts marks a response cost withheld because the record
	// carries no pre-truncation byte length. Zero bytes means UNKNOWN, not
	// free; treating it as free would silently understate the workload.
	ReasonNoByteCounts ExclusionReason = "no_byte_counts"

	// ReasonInternalNoByteCounts is ReasonNoByteCounts for a built-in call.
	//
	// It is no longer systematic. The internal tool-call emission DID leave the
	// byte fields empty — so every built-in call, retrieve_tools above all, was
	// unaccountable with bodies off — until Runtime.EmitActivityInternalToolCallTruncated
	// (internal/runtime/event_bus.go) began measuring both counts on every
	// internal emission. Today it fires on corpora captured before that, and on
	// a live record whose request or response was genuinely empty (an error
	// path, where the zero is true rather than unknown).
	//
	// It keeps its own name for the same reason ReasonSubCallZeroBytes does: an
	// operator can then tell an old corpus from a live gap, instead of reading
	// one undifferentiated pile that looks like data loss.
	ReasonInternalNoByteCounts ExclusionReason = "internal_no_byte_counts"

	// ReasonSubCallZeroBytes is the second known gap: a code-execution sub-call
	// with no byte length, so the byte-estimate fallback has nothing to work
	// from.
	//
	// It is no longer systematic. Sub-calls DID record both counts as zero
	// until subCallByteSizes began measuring them
	// (internal/server/mcp_code_execution.go); today it fires on corpora
	// captured before that, and on the one live path that still records zero
	// response bytes — a policy REFUSAL (emitSubCallRefused), where no response
	// existed and the zero is true rather than unknown. It keeps its own name
	// so an operator can tell an old corpus from a live gap.
	ReasonSubCallZeroBytes ExclusionReason = "sub_call_zero_bytes"

	// ReasonTruncatedRetrieveOverstates is the asymmetry that makes truncated
	// built-in records worse than useless.
	//
	// The direction of `response_truncated` is NOT derivable from the record
	// type. It is stamped on the record by the emitter that performed the cut
	// (`response_truncation_cut`, contracts.ResponseCut), because several
	// emitters set the flag and they point different ways — a code-execution
	// sub-call is a `tool_call` record whose cut runs the opposite way from an
	// ordinary one's. contracts.ResolveResponseTruncation
	// (internal/contracts/activity_truncation.go) is the single authority on
	// what a record's truncation state means; nothing here restates its table.
	//
	// The stamp this reason fires on is contracts.CutShortenedAgentOnly:
	//
	//	stored > delivered — the log holds the FULL pre-cut response while
	//	the agent consumed the cut text, so BOTH the stored text and
	//	`response_bytes` describe something larger than what was paid for.
	//
	// Counting either would overstate mcpproxy's cost — flattering, and still
	// wrong — so the cost is withheld and counted.
	//
	// The other two stamps are kept rather than excluded:
	//
	//   - CutShortenedAgentAndRecord (ordinary upstream forward truncation):
	//     the record holds the agent's copy (or, with the storage cut too,
	//     strictly less than it) and never more. `response_bytes` is then the
	//     PRE-cut upstream size: larger than both bodies, and so not an honest
	//     estimate of what the agent consumed either — it is an honest estimate
	//     of the UPSTREAM PAYLOAD, which is the quantity the byte figures are
	//     defined as.
	//   - CutShortenedRecordOnly (a sandbox sub-call): the whole response was
	//     delivered, so `response_bytes` is honest about delivery and only the
	//     log copy is short.
	//
	// The alternative permitted by the contract is to re-apply the recorded
	// truncation limit before counting; that needs a limit the export does not
	// carry, so exclusion is what is implementable here.
	ReasonTruncatedRetrieveOverstates ExclusionReason = "truncated_retrieve_tools_overstates"

	// ReasonMixedCostBasis marks a figure withheld because its components did
	// not share an accounting basis. A measured cost is a TOKEN count and an
	// estimated one is a BYTE length; adding them yields a number in no unit at
	// all, and it would look entirely plausible. This is the never-sum rule
	// applied at the point of addition rather than at publication.
	ReasonMixedCostBasis ExclusionReason = "mixed_cost_basis"

	// ReasonStorageTruncatedBodyUnmeasurable marks a response cost withheld
	// under bodies-on because activity_max_response_size cut the STORED body on
	// the way into the activity log (issue #1173). It is the mirror of
	// ReasonTruncatedRetrieveOverstates: there the log holds MORE than the
	// agent received, here it holds LESS.
	//
	// Why withheld rather than estimated. The record's response_bytes is
	// measured pre-truncation and is perfectly honest, so with bodies OFF a
	// storage-truncated record needs no special handling at all — it is
	// estimated from bytes like every other record and nothing is lost. With
	// bodies ON it cannot simply fall through to that estimate: the siblings it
	// is summed with are MEASURED (tokens), the fallback is ESTIMATED (bytes),
	// and computeSaving withholds any aggregate mixing the two. It would still
	// be withheld — just reported as mixed_cost_basis, which names the symptom
	// and hides the cause, on exactly the >64KB payloads where a code-execution
	// saving is largest. Naming the cause here is the difference between a
	// benchmark that says "one of your records was cut in storage" and one that
	// says "your bases disagreed".
	//
	// The remedy is a capture-time setting, not a loader heuristic: record the
	// corpus with "activity_max_response_size": 0 so bodies reach the log
	// whole. That is what makes the off switch a requirement rather than a
	// nicety for a --bodies=on-unmasked run.
	ReasonStorageTruncatedBodyUnmeasurable ExclusionReason = "storage_truncated_body_unmeasurable"

	// ReasonTruncatedSubCallOverstates marks a code-execution saving withheld
	// because one of the sandbox's sub-calls was truncated. response_bytes is
	// the FULL pre-truncation size, but the baseline it feeds means "what an
	// agent would have paid making this call itself" — and that agent receives
	// a response cut to ToolResponseLimit. Charging the baseline the full size
	// overstates it, and an overstated baseline INFLATES the saving, which is
	// the one direction of error a savings figure must never make.
	//
	// Withholding, rather than substituting the cut length, follows the policy
	// ReasonTruncatedRetrieveOverstates already sets. The alternative worth
	// considering later is to count the truncated component at its STORED
	// length and publish the result as a lower bound — more informative, and it
	// needs a saving type that can express "at least".
	ReasonTruncatedSubCallOverstates ExclusionReason = "truncated_sub_call_overstates_saving"
)

// ExclusionRow is one line of an exclusion report: a reason and how many times
// it fired. Rows are what a report renders; the maps behind them are what the
// loader accumulates.
type ExclusionRow struct {
	Reason ExclusionReason `json:"reason"`
	Count  int             `json:"count"`
}

// ExclusionReport accounts for everything that did NOT contribute, in three
// distinct senses that a single counter would blur:
//
//   - Dropped — records that entered no session at all.
//   - Withheld — RESPONSE costs suppressed on records that WERE admitted. Only
//     the response side is counted: a request cost withheld on the same record
//     has the same cause, is visible on the call's RequestCost, and counting it
//     too would make "12 withheld" mean neither 12 records nor 12 responses.
//   - Flagged — records admitted and counted, but carrying a usability flag a
//     report must surface beside the figure they contributed to.
//
// The distinction is load-bearing for FR-003/SC-008: "12 exclusions" means
// nothing if a dropped record, a suppressed response cost and a truncation
// annotation are all folded into one number.
type ExclusionReport struct {
	Dropped  map[ExclusionReason]int `json:"dropped,omitempty"`
	Withheld map[ExclusionReason]int `json:"withheld,omitempty"`
	Flagged  map[ExclusionReason]int `json:"flagged,omitempty"`

	// OrphanedSubCalls counts sub-calls whose parent_id resolved to no
	// request_id in the export — typically because the parent code_execution
	// fell outside the exported window.
	//
	// Two outcomes, and this counter spans BOTH so the attribution loss is
	// visible either way:
	//   - the sub-call carries its own work_session_id: it is KEPT at top
	//     level, because dropping it would understate the workload;
	//   - it does not: there is no unit of work left to attribute it to, so it
	//     is dropped under ReasonOrphanedSubCall — which is a DIFFERENT and
	//     more actionable reason than ReasonUnattributed, since a wider export
	//     window would recover it.
	// Sandbox sub-calls commonly fall in the second case: they inherit their
	// session from the parent, so losing the parent loses the session too.
	OrphanedSubCalls int `json:"orphaned_sub_calls,omitempty"`
}

func (r *ExclusionReport) drop(reason ExclusionReason)     { bump(&r.Dropped, reason) }
func (r *ExclusionReport) withhold(reason ExclusionReason) { bump(&r.Withheld, reason) }
func (r *ExclusionReport) flag(reason ExclusionReason)     { bump(&r.Flagged, reason) }

func bump(m *map[ExclusionReason]int, reason ExclusionReason) {
	if *m == nil {
		*m = make(map[ExclusionReason]int)
	}
	(*m)[reason]++
}

// DroppedRows returns the dropped-record counts sorted by reason.
func (r *ExclusionReport) DroppedRows() []ExclusionRow { return rows(r.Dropped) }

// WithheldRows returns the withheld cost-component counts sorted by reason.
func (r *ExclusionReport) WithheldRows() []ExclusionRow { return rows(r.Withheld) }

// FlaggedRows returns the admitted-but-flagged counts sorted by reason.
func (r *ExclusionReport) FlaggedRows() []ExclusionRow { return rows(r.Flagged) }

// TotalDropped is how many records entered no session.
func (r *ExclusionReport) TotalDropped() int { return total(r.Dropped) }

// TotalWithheld is how many cost components were suppressed.
func (r *ExclusionReport) TotalWithheld() int { return total(r.Withheld) }

// TotalFlagged is how many usability flags were raised across admitted records.
func (r *ExclusionReport) TotalFlagged() int { return total(r.Flagged) }

func rows(m map[ExclusionReason]int) []ExclusionRow {
	out := make([]ExclusionRow, 0, len(m))
	for reason, count := range m {
		out = append(out, ExclusionRow{Reason: reason, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reason < out[j].Reason })
	return out
}

func total(m map[ExclusionReason]int) int {
	sum := 0
	for _, count := range m {
		sum += count
	}
	return sum
}

// Flags is the usability classification of a call or a session, computed once
// at load and never re-derived downstream. A session's flags are the union of
// its calls': one truncated call makes the whole unit of work suspect, because
// the unit's total is what a report quotes.
type Flags struct {
	Truncated     bool `json:"truncated,omitempty"`
	BodiesMissing bool `json:"bodies_missing,omitempty"`
	Sensitive     bool `json:"sensitive,omitempty"`
	Unreplayable  bool `json:"unreplayable,omitempty"`
}

// Usable reports whether nothing is wrong with this call or session. It is
// deliberately strict — a caller that wants to use a partly-flagged session
// must look at WHICH flag is set and say so in its report, rather than
// consulting one boolean.
func (f Flags) Usable() bool { return f == Flags{} }

// Reasons lists the set flags as exclusion reasons, in a fixed order, so a
// report can print why a session is not usable without re-deriving anything.
func (f Flags) Reasons() []ExclusionReason {
	var out []ExclusionReason
	if f.Truncated {
		out = append(out, ReasonTruncated)
	}
	if f.BodiesMissing {
		out = append(out, ReasonBodiesMissing)
	}
	if f.Sensitive {
		out = append(out, ReasonSensitive)
	}
	if f.Unreplayable {
		out = append(out, ReasonUnreplayable)
	}
	return out
}

func (f *Flags) merge(other Flags) {
	f.Truncated = f.Truncated || other.Truncated
	f.BodiesMissing = f.BodiesMissing || other.BodiesMissing
	f.Sensitive = f.Sensitive || other.Sensitive
	f.Unreplayable = f.Unreplayable || other.Unreplayable
}

// CostBasis is the provenance of one cost figure. The three values must never
// be presented interchangeably: FR-013 requires measured and estimated figures
// to coexist inside one block, which only works if each carries its own basis.
type CostBasis string

const (
	// CostMeasured means the text was tokenized inside this package. Only
	// reachable with bodies on.
	CostMeasured CostBasis = "measured"

	// CostEstimated means the figure is derived from a pre-truncation BYTE
	// LENGTH. Byte lengths are not token counts; this is an estimate and must
	// be labelled as one wherever it appears.
	CostEstimated CostBasis = "estimated"

	// CostUnavailable means no honest figure exists. It is reported as
	// unavailable with a Reason — never as zero, which would read as a free
	// call and understate the workload.
	CostUnavailable CostBasis = "unavailable"
)

// Cost is one accounted cost component of a call: its request, or its response.
type Cost struct {
	Basis CostBasis `json:"basis"`

	// Tokens is populated only when Basis is CostMeasured. An estimate carries
	// no token count on purpose: converting bytes to tokens here would hide the
	// conversion's assumptions inside a number that looks measured.
	Tokens int `json:"tokens,omitempty"`

	// Bytes is the pre-truncation byte LENGTH, populated for measured and
	// estimated figures alike so a report can show the basis it worked from.
	Bytes int `json:"bytes,omitempty"`

	// Truncated marks an estimate taken from a record whose stored content was
	// cut. This is the annotation that keeps FR-002 honest: a truncated record
	// may contribute, but never silently.
	Truncated bool `json:"truncated,omitempty"`

	// Reason explains a CostUnavailable figure. Empty otherwise.
	Reason ExclusionReason `json:"reason,omitempty"`
}

// ReplayCall is one recorded tool call. It carries identity, sizes and derived
// measurements — and, by construction, NO content: see the package doc's second
// invariant.
type ReplayCall struct {
	ID         string    `json:"id"`
	RequestID  string    `json:"request_id,omitempty"`
	ParentID   string    `json:"parent_id,omitempty"`
	ServerName string    `json:"server_name,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	Status     string    `json:"status,omitempty"`
	Timestamp  time.Time `json:"timestamp"`

	// Internal marks a built-in proxy tool call (retrieve_tools, call_tool_*,
	// code_execution) as opposed to an upstream one. It decides which
	// byte-coverage gap applies and whether the truncation asymmetry bites.
	Internal bool `json:"internal,omitempty"`

	// RequestBytes and ResponseBytes are byte LENGTHS measured pre-truncation,
	// NOT token counts. Zero means unknown, not free.
	RequestBytes  int `json:"request_bytes,omitempty"`
	ResponseBytes int `json:"response_bytes,omitempty"`

	Flags        Flags `json:"flags,omitempty"`
	RequestCost  Cost  `json:"request_cost"`
	ResponseCost Cost  `json:"response_cost"`

	// SubCalls are the calls a code_execution sandbox issued, joined by
	// parent_id. They live here and NOT in their session's top-level Calls, so
	// that a naive sum over Calls cannot double-count them.
	SubCalls []*ReplayCall `json:"sub_calls,omitempty"`
}

// classify computes the usability flags for one call and records them in the
// exclusion report. It runs exactly once, during load; nothing downstream
// re-derives a flag, because a second derivation is a second chance to disagree
// with the first.
func (c *ReplayCall) classify(rec *decodedRecord, opts *Options, rep *ExclusionReport) {
	if rec.truncated {
		c.Flags.Truncated = true
		rep.flag(ReasonTruncated)
	}
	if opts.Bodies == BodiesOff {
		c.Flags.BodiesMissing = true
		rep.flag(ReasonBodiesMissing)
	}
	if rec.sensitive {
		c.Flags.Sensitive = true
		rep.flag(ReasonSensitive)
	}
	// Only assertable against a supplied fleet. With no resolver the honest
	// answer is "not checked", not "replayable".
	if opts.FleetResolver != nil && !opts.FleetResolver(c.ServerName, c.ToolName) {
		c.Flags.Unreplayable = true
		rep.flag(ReasonUnreplayable)
	}
}

// responseCost decides what, if anything, this record's response may contribute,
// and records any withholding. The ordering of the branches IS the policy:
//
//  1. A flagged-sensitive body is never tokenized, whatever the body policy.
//  2. A truncated built-in response is excluded outright — both its stored text
//     and its byte length describe more than the agent consumed.
//  3. With bodies on, a body the activity log itself cut is excluded: it is a
//     PREFIX, so tokenizing it understates, and the byte estimate that would
//     otherwise stand in has a different basis from its measured siblings. With
//     bodies off this does not apply — nothing is tokenized and the
//     pre-truncation byte length is honest — so the branch is bodies-gated.
//  4. An untruncated body, with bodies on, is measured.
//  5. Anything else falls back to the pre-truncation byte length as an
//     explicitly annotated estimate.
//  6. With no byte length there is no honest figure: unavailable, and counted
//     under the specific gap that caused it.
func responseCost(rec *decodedRecord, isSubCall bool, opts *Options, count func(string) int, rep *ExclusionReport) Cost {
	bodiesOn := opts.Bodies == BodiesOnUnmasked

	if rec.sensitive && bodiesOn {
		rep.withhold(ReasonSensitive)
		return Cost{Basis: CostUnavailable, Reason: ReasonSensitive}
	}
	if recordOverstatesDelivery(rec) {
		rep.withhold(ReasonTruncatedRetrieveOverstates)
		return Cost{Basis: CostUnavailable, Reason: ReasonTruncatedRetrieveOverstates}
	}
	if bodiesOn && rec.storageTruncated {
		rep.withhold(ReasonStorageTruncatedBodyUnmeasurable)
		return Cost{Basis: CostUnavailable, Reason: ReasonStorageTruncatedBodyUnmeasurable}
	}
	if bodiesOn && !rec.truncated && rec.response != "" {
		return Cost{Basis: CostMeasured, Tokens: count(rec.response), Bytes: len(rec.response)}
	}
	if rec.responseBytes > 0 {
		return Cost{Basis: CostEstimated, Bytes: rec.responseBytes, Truncated: rec.truncated}
	}
	reason := byteGapReason(rec, isSubCall)
	rep.withhold(reason)
	return Cost{Basis: CostUnavailable, Reason: reason}
}

// requestCost is responseCost for the arguments side. It is simpler because the
// truncation asymmetry does not apply — arguments are recorded whole — but it
// obeys the same sensitive-body and unknown-bytes rules.
// It does not bump the exclusion report — see ExclusionReport.Withheld for why
// the response side is the one that is counted — but an unavailable request
// cost still carries its Reason, so nothing about it is silent.
func requestCost(rec *decodedRecord, isSubCall bool, opts *Options, count func(string) int) Cost {
	bodiesOn := opts.Bodies == BodiesOnUnmasked

	if rec.sensitive && bodiesOn {
		return Cost{Basis: CostUnavailable, Reason: ReasonSensitive}
	}
	if bodiesOn && rec.arguments != "" {
		return Cost{Basis: CostMeasured, Tokens: count(rec.arguments), Bytes: len(rec.arguments)}
	}
	// A PARAMETERLESS call records no arguments at all, and with bodies on that
	// is not a recording gap: the record is present and its byte length is the
	// serialized empty object, so the cost is known exactly rather than
	// estimated. Falling through to the byte estimate gave such a call a
	// different BASIS from its siblings, and since measured figures are tokens
	// while estimated ones are bytes, any aggregate holding both is withheld —
	// so a single parameterless tool poisoned a whole script. Most fleets have
	// several (list_allowed_directories, read_graph, get_current_time).
	//
	// Bounded by emptyArgsMaxBytes so it cannot swallow a real gap: empty
	// arguments beside a LARGE byte length means content genuinely is missing,
	// and that must stay an estimate.
	if bodiesOn && rec.arguments == "" && rec.requestBytes > 0 && rec.requestBytes <= emptyArgsMaxBytes {
		return Cost{Basis: CostMeasured, Tokens: count(emptyArgsJSON), Bytes: rec.requestBytes}
	}
	if rec.requestBytes > 0 {
		return Cost{Basis: CostEstimated, Bytes: rec.requestBytes}
	}
	return Cost{Basis: CostUnavailable, Reason: byteGapReason(rec, isSubCall)}
}

// emptyArgsJSON is what an argument-less call serializes to on the wire, and
// emptyArgsMaxBytes is the largest request that can still BE one. "{}" is two
// bytes and "null" is four; anything larger had content that is not in the
// record, which is a gap rather than a parameterless call.
const (
	emptyArgsJSON     = "{}"
	emptyArgsMaxBytes = 4
)

// recordOverstatesDelivery reports whether this record's stored text and
// response_bytes describe something LARGER than what the agent consumed — the
// one condition under which counting the record inflates mcpproxy's cost.
//
// It reads the emitter's stamp and never the record type. The type fallback
// covers corpora exported before the stamp existed: `truncated && internal` was
// this predicate's whole body then, and it was correct for the emitter
// population that wrote those records — an internal_tool_call was the only
// CutShortenedAgentOnly emitter, and a sandbox sub-call (a `tool_call`) has
// always been excluded from it. Do not extend the fallback; a new emitter
// reaching it without a stamp is a bug in the emitter.
func recordOverstatesDelivery(rec *decodedRecord) bool {
	if !rec.truncated {
		return false
	}
	switch rec.cut {
	case contracts.CutShortenedAgentOnly:
		return true
	case contracts.CutShortenedAgentAndRecord, contracts.CutShortenedRecordOnly:
		return false
	default:
		// CutNone (a pre-stamp export) or a value from a newer core.
		return rec.internal
	}
}

// byteGapReason names WHICH known gap left this record without a byte length.
// The two once-systematic gaps keep their own names so an operator reading the
// exclusion report can separate a corpus captured before the emitters measured
// their bodies from a live recording gap, rather than reading one
// undifferentiated pile that looks like data loss. Neither is systematic any
// more — see the two constants.
func byteGapReason(rec *decodedRecord, isSubCall bool) ExclusionReason {
	switch {
	case isSubCall:
		return ReasonSubCallZeroBytes
	case rec.internal:
		return ReasonInternalNoByteCounts
	default:
		return ReasonNoByteCounts
	}
}
