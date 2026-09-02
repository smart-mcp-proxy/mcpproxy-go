package contracts

import "fmt"

// ONE place that knows what the two truncation flags mean.
//
// An activity record can be cut twice, by two unrelated settings:
//
//   - the FORWARD cut (`response_truncated`, Spec 103) applies
//     `tool_response_limit` per text block on the way to the agent —
//     internal/server/content_forward.go. Because the recorded text is the JOIN
//     of every forwarded block, the joined body can be far LARGER than
//     `tool_response_limit` even with the flag set.
//   - the STORAGE cut (`response_storage_truncated`, issue #1173) applies
//     `activity_max_response_size` in bytes to whatever text the emitter hands
//     the activity service — internal/runtime/activity_service.go.
//
// Defaults are 20000 and 65536; neither bounds the other, so BOTH flags on one
// record is an ordinary, reachable state and not a corner case.
//
// What the flags say about the record depends on the record TYPE *and* on
// whether the other flag is set, because the emitters record different sides of
// the forward cut:
//
//	                     forward only        storage only       BOTH
//	tool_call            stored == delivered stored < delivered stored < delivered
//	internal_tool_call   stored >  delivered stored < delivered order not fixed
//	prompt_get           (not emitted)       stored < delivered (not emitted)
//
// Four rendering surfaces restated that table in prose and four rounds of
// review found a different cell wrong each time. So no renderer composes a
// sentence from the flags any more: `ResolveResponseTruncation` is the single
// authority, `internal/contracts/activity_truncation_test.go` pins all twelve
// cells, and every consumer — the CLI (`mcpproxy activity show`), the REST
// payload's `response_truncation_notice`, and through it the Web UI drawer —
// renders what it returns.

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
	// StoredVsDeliveredUnknown: two cuts pointing opposite ways under unrelated
	// limits, so the order of the two sizes is not fixed. Never guess a
	// direction here — guessing is what produced the bug this type replaces.
	StoredVsDeliveredUnknown StoredVsDelivered = "unknown"
)

// ResponseBytesSubject says which body, if either, `response_bytes` measures.
//
// `response_bytes` is `rawByteSize(result)` — the PRE-forward, PRE-TOON upstream
// payload (internal/server/mcp.go). Whenever a forward cut happened it is
// strictly larger than both the stored and the delivered body, so quoting it as
// "what the agent received" overstates delivery by exactly what the cut removed.
type ResponseBytesSubject string

const (
	// ResponseBytesDescribesBoth: no cut of either kind, so the pre-cut size is
	// both the stored and the delivered size.
	ResponseBytesDescribesBoth ResponseBytesSubject = "both"
	// ResponseBytesDescribesStored: it measures the text this record holds.
	ResponseBytesDescribesStored ResponseBytesSubject = "stored"
	// ResponseBytesDescribesDelivered: it measures the text the agent received.
	ResponseBytesDescribesDelivered ResponseBytesSubject = "delivered"
	// ResponseBytesDescribesNeither: a forward cut sits between the measurement
	// and both bodies.
	ResponseBytesDescribesNeither ResponseBytesSubject = "neither"
)

// ResponseTruncation is the resolved truth about one record's truncation state.
type ResponseTruncation struct {
	// ForwardTruncated / StorageTruncated echo the inputs so a consumer that
	// holds only this struct can still branch on them.
	ForwardTruncated bool `json:"forward_truncated"`
	StorageTruncated bool `json:"storage_truncated"`

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

// Record types that resolve to a known direction. Kept as literals rather than
// importing internal/storage: this package is the API contract and must stay
// importable from the CLI, the HTTP layer and the frontend generator without
// dragging BBolt in.
const (
	activityTypeToolCall         = "tool_call"
	activityTypeInternalToolCall = "internal_tool_call"
	activityTypePromptGet        = "prompt_get"
)

// Sentence fragments, named so a change lands in one place.
const (
	noticeForwardToolCall = "This is the agent's own copy: the upstream response was cut to " +
		"tool_response_limit before being both forwarded and recorded."

	noticeForwardInternal = "The agent received LESS than this: the built-in recorded its full " +
		"response, and the agent got it cut to tool_response_limit."

	noticeStorageOnly = "The agent received MORE than this: the recorded text was shortened to " +
		"fit activity_max_response_size."

	noticeBothToolCall = "The agent received MORE than this: the upstream response was cut to " +
		"tool_response_limit before being forwarded and recorded, and the recorded copy was " +
		"then shortened AGAIN to fit activity_max_response_size."

	noticeBothInternal = "Neither body is known to be the larger: the built-in recorded its full " +
		"response while the agent got it cut to tool_response_limit, and the record was then " +
		"shortened to fit activity_max_response_size — two unrelated limits."

	noticeForwardUnknownType = "The response was cut to tool_response_limit before being " +
		"forwarded; this record type does not say which side of that cut it recorded."

	noticeBothUnknownType = "The response was cut to tool_response_limit before being forwarded " +
		"and the record was then shortened to fit activity_max_response_size; this record type " +
		"does not say which side of the forward cut it recorded, so neither body is known to be " +
		"the larger."
)

// ResolveResponseTruncation is the single authority on what a record's two
// truncation flags mean. Every rendering surface calls it; none reimplements it.
//
// recordType is ActivityRecord.Type as a string. An unrecognised type is
// treated like prompt_get: the storage cut still has a fixed direction, and the
// forward cut gets a direction-FREE statement rather than a borrowed one, so a
// future emitter cannot silently inherit a claim it never established.
func ResolveResponseTruncation(recordType string, forwardTruncated, storageTruncated bool) ResponseTruncation {
	res := ResponseTruncation{
		ForwardTruncated: forwardTruncated,
		StorageTruncated: storageTruncated,
	}

	if !forwardTruncated && !storageTruncated {
		res.Relation = StoredEqualsDelivered
		res.BytesSubject = ResponseBytesDescribesBoth
		return res
	}

	if !forwardTruncated {
		// The storage cut alone is the one type-independent cell: whatever the
		// emitter handed over WAS delivered, and BBolt kept a prefix of it.
		res.Relation = StoredSmallerThanDelivered
		res.BytesSubject = ResponseBytesDescribesDelivered
		res.Notice = noticeStorageOnly
		return res
	}

	// A forward cut happened, so response_bytes (pre-forward) is larger than
	// the delivered body on every type; only the no-forward-cut cells above can
	// attribute it to a body.
	switch recordType {
	case activityTypeToolCall:
		// handleToolCallCompleted is passed the POST-forward text (the
		// forwardedText call in internal/server/mcp.go), so the record starts
		// life as exactly the agent's copy...
		if storageTruncated {
			// ...and activity_service.go then cuts that already-forwarded text
			// again, strictly shortening it below the delivered body.
			res.Relation = StoredSmallerThanDelivered
			res.BytesSubject = ResponseBytesDescribesNeither
			res.Notice = noticeBothToolCall
			return res
		}
		res.Relation = StoredEqualsDelivered
		res.BytesSubject = ResponseBytesDescribesNeither
		res.Notice = noticeForwardToolCall
		return res

	case activityTypeInternalToolCall:
		// emitActivityInternalToolCallTruncated is fed the PRE-forward result,
		// so this is the only type whose record can hold MORE than was
		// delivered.
		if storageTruncated {
			// The record holds a prefix of the pre-forward text and the agent
			// holds a per-block cut of it. tool_response_limit and
			// activity_max_response_size are unrelated knobs, so which of the
			// two is larger is not decidable from the flags.
			res.Relation = StoredVsDeliveredUnknown
			res.BytesSubject = ResponseBytesDescribesNeither
			res.Notice = noticeBothInternal
			return res
		}
		res.Relation = StoredLargerThanDelivered
		res.BytesSubject = ResponseBytesDescribesStored
		res.Notice = noticeForwardInternal
		return res

	case activityTypePromptGet:
		// No forward cut runs in front of prompt_get, so this flag is not
		// emitted for it today. Treated exactly like a future unknown type.
		return directionFreeForwardCut(res, storageTruncated)

	default:
		return directionFreeForwardCut(res, storageTruncated)
	}
}

// directionFreeForwardCut is the answer for any type that has not established
// which side of the forward cut it records: state what happened, claim no
// direction. A renderer that borrowed tool_call's or internal_tool_call's
// direction here would be asserting something the emitter never established.
func directionFreeForwardCut(res ResponseTruncation, storageTruncated bool) ResponseTruncation {
	res.Relation = StoredVsDeliveredUnknown
	res.BytesSubject = ResponseBytesDescribesNeither
	if storageTruncated {
		res.Notice = noticeBothUnknownType
		return res
	}
	res.Notice = noticeForwardUnknownType
	return res
}

// NoticeWithBytes is Notice plus the one honest sentence about response_bytes.
//
// responseBytes <= 0 means UNKNOWN (legacy records, and every internal_tool_call
// — the built-in emitter never populates the byte fields), so the clause is
// dropped rather than printed as "0 bytes", which would read as an empty
// response.
func (t ResponseTruncation) NoticeWithBytes(responseBytes int) string {
	if t.Notice == "" || responseBytes <= 0 {
		return t.Notice
	}
	switch t.BytesSubject {
	case ResponseBytesDescribesDelivered:
		return t.Notice + fmt.Sprintf(" response_bytes (%d) is what the agent received.", responseBytes)
	case ResponseBytesDescribesStored:
		return t.Notice + fmt.Sprintf(" response_bytes (%d) is the size of this recorded text.", responseBytes)
	case ResponseBytesDescribesNeither:
		return t.Notice + fmt.Sprintf(
			" response_bytes (%d) is the pre-forward upstream size, so it describes neither this record nor the agent's copy.",
			responseBytes)
	case ResponseBytesDescribesBoth:
		// Unreachable: BytesSubject is "both" only in the nothing-was-cut cell,
		// where Notice is empty and the guard above already returned.
		return t.Notice
	default:
		return t.Notice
	}
}
