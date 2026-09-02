package contracts

import "fmt"

// ONE place that knows what the two truncation flags mean.
//
// An activity record can be cut twice, by two unrelated mechanisms:
//
//   - the RESPONSE cut (`response_truncated`, Spec 103). Several different
//     emitters set this flag, under different limits, cutting different copies
//     of the text. It is therefore NOT a direction: see ResponseCut below.
//   - the STORAGE cut (`response_storage_truncated`, issue #1173) applies
//     `activity_max_response_size` in bytes to whatever text the emitter hands
//     the activity service — internal/runtime/activity_service.go. This one HAS
//     always had a single direction: it shortens the RECORD and nothing else.
//
// Neither limit bounds the other, so BOTH flags on one record is an ordinary,
// reachable state and not a corner case.
//
// FIVE rounds of review tried to recover the response cut's direction at RENDER
// time — first universally, then per record type, then per (type, flags) pair.
// Every one of them was right about the emitters it knew and wrong about one it
// did not, because the direction is a property of WHICH EMITTER CUT THE TEXT,
// not of the record it happened to write. Round 4's per-type table was defeated
// by code-execution sub-calls, which are emitted as Type=tool_call with
// response_truncated=true and whose cut runs the OPPOSITE way from every other
// tool_call's (internal/server/mcp_code_execution.go, subCallActivityResponseLimit).
//
// So nothing infers any more. The emitter STAMPS the direction on the record
// (ResponseCut), and ResolveResponseTruncation reads the stamp.

// ResponseCut says WHICH COPIES of a response the `response_truncated` cut
// actually shortened. The emitter that performed the cut sets it; no consumer
// derives it.
//
// It is deliberately not a two-way "forward vs log" split. Two of mcpproxy's
// emitters cut on the way into the log and they point OPPOSITE ways: a
// code-execution sub-call's record is a prefix of what the sandbox got, while a
// built-in's record holds the whole text the agent only saw cut. Merging those
// into one value would reintroduce exactly the class of bug this type exists to
// end, so the vocabulary names the bodies, not the plumbing.
type ResponseCut string

const (
	// CutNone means no response cut happened. It is the zero value, so a record
	// that carries no stamp reads as "no cut" — which is only correct while
	// `response_truncated` is false. A record with the flag set and no stamp is
	// a LEGACY record written before this field existed, and the resolver
	// answers it without claiming a direction. See ResolveResponseTruncation.
	CutNone ResponseCut = ""

	// CutShortenedAgentAndRecord: the cut happened on the way to the AGENT and
	// the record holds that same cut copy. Nothing anywhere retains the removed
	// text. This is ordinary upstream tool_call forward truncation
	// (`tool_response_limit`, internal/server/content_forward.go): the recorded
	// text is the join of the forwarded blocks.
	CutShortenedAgentAndRecord ResponseCut = "agent_and_record"

	// CutShortenedAgentOnly: only the AGENT's copy was cut; the record holds the
	// full pre-cut text. The record is therefore LARGER than what was
	// delivered — the one case in which recomputing cost from the log
	// OVERSTATES what mcpproxy cost. Today: the built-in emitters
	// (retrieve_tools, and the internal_tool_call mirror of a call_tool_*
	// dispatch), which record the pre-forward text under `tool_response_limit`.
	CutShortenedAgentOnly ResponseCut = "agent_only"

	// CutShortenedRecordOnly: only the RECORD was cut; the agent received the
	// whole text. The record is a prefix of what was delivered. Today: a
	// code-execution sub-call, cut to subCallActivityResponseLimit (8KB) purely
	// to bound the log — the sandbox script got the entire result.
	CutShortenedRecordOnly ResponseCut = "record_only"
)

// Cuts reports whether this stamp describes an actual cut. It is what derives
// ActivityRecord.ResponseTruncated at emission, so the boolean can never
// disagree with the stamp beside it.
func (c ResponseCut) Cuts() bool { return c != CutNone }

// Valid reports whether c is one of the declared values. Used when reading a
// stamp back off the wire: an unrecognised string is treated as unstated rather
// than trusted, so a newer core's value cannot be misread by an older one.
func (c ResponseCut) Valid() bool {
	switch c {
	case CutNone, CutShortenedAgentAndRecord, CutShortenedAgentOnly, CutShortenedRecordOnly:
		return true
	default:
		return false
	}
}

// ValidResponseCuts is the closed vocabulary, for API documentation and for the
// TypeScript mirror in cmd/generate-types.
var ValidResponseCuts = []string{
	string(CutShortenedAgentAndRecord),
	string(CutShortenedAgentOnly),
	string(CutShortenedRecordOnly),
}

// StoredVsDelivered is how the RECORDED response body compares to the body the
// agent actually received.
type StoredVsDelivered string

const (
	// StoredEqualsDelivered: the record holds exactly the agent's copy.
	StoredEqualsDelivered StoredVsDelivered = "equal"
	// StoredSmallerThanDelivered: the record is a prefix of the agent's copy.
	StoredSmallerThanDelivered StoredVsDelivered = "smaller"
	// StoredLargerThanDelivered: the agent's copy is a prefix of the record.
	StoredLargerThanDelivered StoredVsDelivered = "larger"
	// StoredVsDeliveredUnknown: either two cuts pointing opposite ways under
	// unrelated limits, so the order of the two sizes is not fixed, or a legacy
	// record whose emitter left no stamp. Never guess a direction here —
	// guessing is what produced the five rounds this type replaces.
	StoredVsDeliveredUnknown StoredVsDelivered = "unknown"
)

// ResponseBytesSubject says which body, if either, `response_bytes` measures.
//
// `response_bytes` is always the emitter's PRE-cut measurement of the response,
// so which body it describes falls straight out of the stamp: the bodies the
// cut did not shorten still match it.
type ResponseBytesSubject string

const (
	// ResponseBytesDescribesBoth: no cut of either kind, so the pre-cut size is
	// both the stored and the delivered size.
	ResponseBytesDescribesBoth ResponseBytesSubject = "both"
	// ResponseBytesDescribesStored: it measures the text this record holds.
	ResponseBytesDescribesStored ResponseBytesSubject = "stored"
	// ResponseBytesDescribesDelivered: it measures the text the agent received.
	ResponseBytesDescribesDelivered ResponseBytesSubject = "delivered"
	// ResponseBytesDescribesNeither: a cut sits between the measurement and
	// both bodies.
	ResponseBytesDescribesNeither ResponseBytesSubject = "neither"
)

// ResponseTruncation is the resolved truth about one record's truncation state.
type ResponseTruncation struct {
	// Cut echoes the stamp the emitter left, so a consumer holding only this
	// struct can still branch on it.
	Cut ResponseCut `json:"cut"`

	// ForwardTruncated / StorageTruncated echo the inputs so a consumer that
	// holds only this struct can still branch on them.
	ForwardTruncated bool `json:"forward_truncated"`
	StorageTruncated bool `json:"storage_truncated"`

	// Stamped is false for a record whose response cut carries no direction —
	// only ever a record written by a core older than the stamp. Everything
	// this struct says about such a record is deliberately direction-free.
	Stamped bool `json:"stamped"`

	// Relation is the order of the stored and delivered bodies.
	Relation StoredVsDelivered `json:"relation"`

	// BytesSubject is which body `response_bytes` measures.
	BytesSubject ResponseBytesSubject `json:"bytes_subject"`

	// Notice is the human sentence for this cell, or "" when nothing was cut.
	// It always names the setting(s) an operator can change.
	Notice string `json:"notice"`
}

// Truncated reports whether either cut happened.
func (t ResponseTruncation) Truncated() bool {
	return t.ForwardTruncated || t.StorageTruncated
}

// Sentence fragments, named so a change lands in one place.
const (
	noticeAgentAndRecord = "This is the agent's own copy: the response was cut once, to " +
		"tool_response_limit, before being both forwarded and recorded."

	noticeAgentOnly = "The agent received LESS than this: the emitter recorded its full " +
		"response, and the agent got it cut to tool_response_limit."

	noticeRecordOnly = "The agent received MORE than this: the whole response was delivered, " +
		"and only the recorded copy was shortened to bound the activity log."

	noticeStorageOnly = "The agent received MORE than this: the recorded text was shortened to " +
		"fit activity_max_response_size."

	noticeBothAgentAndRecord = "The agent received MORE than this: the response was cut to " +
		"tool_response_limit before being forwarded and recorded, and the recorded copy was " +
		"then shortened AGAIN to fit activity_max_response_size."

	noticeBothAgentOnly = "Neither body is known to be the larger: the emitter recorded its full " +
		"response while the agent got it cut to tool_response_limit, and the record was then " +
		"shortened to fit activity_max_response_size — two unrelated limits."

	noticeBothRecordOnly = "The agent received MORE than this: the whole response was delivered, " +
		"and the recorded copy was shortened twice — once to bound the activity log, and again " +
		"to fit activity_max_response_size."

	noticeUnstamped = "The response was cut, but this record was written by an older mcpproxy " +
		"that did not say which copy was shortened, so neither body is known to be the larger."

	noticeUnstampedBoth = "The response was cut and the record was then shortened to fit " +
		"activity_max_response_size. This record was written by an older mcpproxy that did not " +
		"say which copy the first cut shortened, so neither body is known to be the larger."
)

// ResolveResponseTruncation is the single authority on what a record's
// truncation state means. Every rendering surface calls it; none reimplements
// it, and none infers a direction.
//
// cut is ActivityRecord.ResponseTruncationCut — the stamp the emitter left.
// responseTruncated and storageTruncated are the record's two booleans.
//
// The stamp and the boolean are emitted together (ResponseCut.Cuts derives the
// boolean), so they can only disagree on a record written before the stamp
// existed: responseTruncated true, cut empty. That case gets a direction-FREE
// answer. Saying less is correct; saying something false is what five rounds of
// review were about.
//
// An unrecognised stamp — a value from a NEWER core, read by an older binary —
// is treated the same way, for the same reason.
func ResolveResponseTruncation(cut ResponseCut, responseTruncated, storageTruncated bool) ResponseTruncation {
	if !cut.Valid() {
		cut = CutNone
	}
	res := ResponseTruncation{
		Cut:              cut,
		ForwardTruncated: responseTruncated,
		StorageTruncated: storageTruncated,
		Stamped:          !responseTruncated || cut.Cuts(),
	}

	if !responseTruncated && !storageTruncated {
		res.Relation = StoredEqualsDelivered
		res.BytesSubject = ResponseBytesDescribesBoth
		return res
	}

	if !responseTruncated {
		// The storage cut alone is the one cell that needs no stamp: whatever
		// the emitter handed over WAS delivered, and BBolt kept a prefix of it.
		// This cut has only ever had one direction.
		res.Relation = StoredSmallerThanDelivered
		res.BytesSubject = ResponseBytesDescribesDelivered
		res.Notice = noticeStorageOnly
		return res
	}

	switch cut {
	case CutShortenedAgentAndRecord:
		// One cut, applied to the text on its way out; the record is the join
		// of the forwarded blocks, so it starts life as exactly the agent's
		// copy. response_bytes was measured before that cut, so it is larger
		// than both bodies.
		if storageTruncated {
			res.Relation = StoredSmallerThanDelivered
			res.BytesSubject = ResponseBytesDescribesNeither
			res.Notice = noticeBothAgentAndRecord
			return res
		}
		res.Relation = StoredEqualsDelivered
		res.BytesSubject = ResponseBytesDescribesNeither
		res.Notice = noticeAgentAndRecord
		return res

	case CutShortenedAgentOnly:
		// The record kept the full pre-cut text, so it holds MORE than was
		// delivered and response_bytes measures the record.
		if storageTruncated {
			// The record is now a prefix of the pre-cut text and the agent
			// holds a per-block cut of it, under two unrelated limits: which is
			// larger is not decidable, and response_bytes describes neither.
			res.Relation = StoredVsDeliveredUnknown
			res.BytesSubject = ResponseBytesDescribesNeither
			res.Notice = noticeBothAgentOnly
			return res
		}
		res.Relation = StoredLargerThanDelivered
		res.BytesSubject = ResponseBytesDescribesStored
		res.Notice = noticeAgentOnly
		return res

	case CutShortenedRecordOnly:
		// Only the log copy was shortened, so the delivered body is the whole
		// response and response_bytes — measured pre-cut — describes it. A
		// second, storage-side cut shortens the same record again and cannot
		// flip the order.
		res.Relation = StoredSmallerThanDelivered
		res.BytesSubject = ResponseBytesDescribesDelivered
		if storageTruncated {
			res.Notice = noticeBothRecordOnly
			return res
		}
		res.Notice = noticeRecordOnly
		return res

	case CutNone:
		// responseTruncated is true and no stamp came with it: a legacy record.
		return unstampedCut(res, storageTruncated)

	default:
		// Unreachable: Valid() above narrowed cut to the four constants.
		return unstampedCut(res, storageTruncated)
	}
}

// unstampedCut is the answer for a record whose response cut carries no
// direction: state what happened, claim no direction. Borrowing another
// emitter's direction here is precisely the mistake that produced five rounds
// of review.
func unstampedCut(res ResponseTruncation, storageTruncated bool) ResponseTruncation {
	res.Stamped = false
	res.Relation = StoredVsDeliveredUnknown
	res.BytesSubject = ResponseBytesDescribesNeither
	if storageTruncated {
		res.Notice = noticeUnstampedBoth
		return res
	}
	res.Notice = noticeUnstamped
	return res
}

// NoticeWithBytes is Notice plus the one honest sentence about response_bytes.
//
// responseBytes <= 0 means UNKNOWN — a legacy record predating the measurement,
// or a policy-refused code-execution sub-call, which records a true zero
// (emitSubCallRefused, internal/server/mcp_code_execution.go). It is NOT the
// case that built-ins leave the byte fields empty: EmitActivityInternalToolCall
// Truncated populates response_bytes on every internal emission
// (internal/runtime/event_bus.go) and ActivityService.handleInternalToolCall
// reads it back. The clause is dropped rather than printed as "0 bytes", which
// would read as an empty response.
func (t ResponseTruncation) NoticeWithBytes(responseBytes int) string {
	if t.Notice == "" || responseBytes <= 0 {
		return t.Notice
	}
	switch t.BytesSubject {
	case ResponseBytesDescribesDelivered:
		// "What the agent received" is the emitter's measurement of the payload
		// it forwarded. Two off-by-default features rewrite the payload AFTER
		// that measurement: spotlightForwarded adds source delimiters
		// (Spec 054), and TOON re-encodes text blocks (Spec 084). With either
		// enabled this is the delivered body's size to within what they added,
		// not to the byte — which is why the clause says "measures" rather than
		// asserting an exact identity.
		return t.Notice + fmt.Sprintf(" response_bytes (%d) measures the response the agent received.", responseBytes)
	case ResponseBytesDescribesStored:
		return t.Notice + fmt.Sprintf(" response_bytes (%d) is the size of this recorded text.", responseBytes)
	case ResponseBytesDescribesNeither:
		return t.Notice + fmt.Sprintf(
			" response_bytes (%d) is the pre-cut upstream size, so it describes neither this record nor the agent's copy.",
			responseBytes)
	case ResponseBytesDescribesBoth:
		// Unreachable: BytesSubject is "both" only in the nothing-was-cut cell,
		// where Notice is empty and the guard above already returned.
		return t.Notice
	default:
		return t.Notice
	}
}
