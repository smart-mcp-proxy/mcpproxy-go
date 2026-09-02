package contracts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The regression guard for the whole feature.
//
// Four review rounds tripped on the same thing: what the two truncation flags
// mean depends on the record TYPE *and* on whether the other flag is set, and
// every rendering surface restated that in prose, getting a different cell wrong
// each time. This table is the full cross product — 3 record types x 4 flag
// combinations — asserted against the direction matrix derived from the
// emitters and handlers, not from anyone's comment:
//
//	internal/server/content_forward.go:97-111,215-237  per-block forward cut,
//	                                                   recorded text = the JOIN
//	internal/server/mcp.go:2665,2726,2730              tool_call: response =
//	                                                   forwardedText(...) BEFORE
//	                                                   the emit; response_bytes =
//	                                                   rawByteSize(pre-forward)
//	internal/server/mcp.go (call_tool_* internal emit) internal_tool_call is fed
//	                                                   the PRE-forward result
//	internal/runtime/activity_service.go:272-282,624   storage cut, applied to
//	                                                   the emitter's text
//
// If a cell here ever needs to change, the emitters changed — fix them or fix
// ResolveResponseTruncation, never a renderer.
func TestResolveResponseTruncation_FullCrossProduct(t *testing.T) {
	tests := []struct {
		name         string
		recordType   string
		forward      bool
		storage      bool
		relation     StoredVsDelivered
		bytesSubject ResponseBytesSubject
		wantNotice   bool
		mustContain  []string
		mustNotHave  []string
	}{
		// ---- tool_call -------------------------------------------------
		{
			name:         "tool_call/neither",
			recordType:   "tool_call",
			relation:     StoredEqualsDelivered,
			bytesSubject: ResponseBytesDescribesBoth,
		},
		{
			// response = forwardedText(...) runs before the emit, so the record
			// starts life as exactly the delivered copy.
			name:         "tool_call/forward only",
			recordType:   "tool_call",
			forward:      true,
			relation:     StoredEqualsDelivered,
			bytesSubject: ResponseBytesDescribesNeither,
			wantNotice:   true,
			mustContain:  []string{"tool_response_limit", "agent's own copy"},
			mustNotHave:  []string{"LESS", "MORE", "activity_max_response_size"},
		},
		{
			name:         "tool_call/storage only",
			recordType:   "tool_call",
			storage:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesDelivered,
			wantNotice:   true,
			mustContain:  []string{"activity_max_response_size", "MORE"},
			mustNotHave:  []string{"tool_response_limit", "LESS"},
		},
		{
			// The cell every previous round got wrong. activity_service.go:624
			// cuts the ALREADY-forwarded text again, so the record is strictly
			// shorter than what the agent got — the exact opposite of the
			// "this IS the agent's copy" the forward-only cell states.
			name:         "tool_call/both",
			recordType:   "tool_call",
			forward:      true,
			storage:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesNeither,
			wantNotice:   true,
			mustContain:  []string{"tool_response_limit", "activity_max_response_size", "MORE"},
			mustNotHave:  []string{"LESS", "agent's own copy"},
		},

		// ---- internal_tool_call ---------------------------------------
		{
			name:         "internal_tool_call/neither",
			recordType:   "internal_tool_call",
			relation:     StoredEqualsDelivered,
			bytesSubject: ResponseBytesDescribesBoth,
		},
		{
			// The ONE type for which "recorded > delivered" is true: the
			// internal emit is fed the pre-forward result.
			name:         "internal_tool_call/forward only",
			recordType:   "internal_tool_call",
			forward:      true,
			relation:     StoredLargerThanDelivered,
			bytesSubject: ResponseBytesDescribesStored,
			wantNotice:   true,
			mustContain:  []string{"tool_response_limit", "LESS"},
			mustNotHave:  []string{"MORE", "activity_max_response_size"},
		},
		{
			name:         "internal_tool_call/storage only",
			recordType:   "internal_tool_call",
			storage:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesDelivered,
			wantNotice:   true,
			mustContain:  []string{"activity_max_response_size", "MORE"},
			mustNotHave:  []string{"tool_response_limit", "LESS"},
		},
		{
			// Two cuts pointing opposite ways under two unrelated limits. The
			// honest answer is that the order is not decidable; claiming either
			// direction here is a guess.
			name:         "internal_tool_call/both",
			recordType:   "internal_tool_call",
			forward:      true,
			storage:      true,
			relation:     StoredVsDeliveredUnknown,
			bytesSubject: ResponseBytesDescribesNeither,
			wantNotice:   true,
			mustContain:  []string{"tool_response_limit", "activity_max_response_size", "Neither body"},
			mustNotHave:  []string{"LESS", "MORE"},
		},

		// ---- prompt_get ------------------------------------------------
		{
			name:         "prompt_get/neither",
			recordType:   "prompt_get",
			relation:     StoredEqualsDelivered,
			bytesSubject: ResponseBytesDescribesBoth,
		},
		{
			// No forward cut runs in front of prompt_get, so the flag is not
			// emitted for it today. If one ever is, it must state which side it
			// recorded before a renderer claims a direction.
			name:         "prompt_get/forward only",
			recordType:   "prompt_get",
			forward:      true,
			relation:     StoredVsDeliveredUnknown,
			bytesSubject: ResponseBytesDescribesNeither,
			wantNotice:   true,
			mustContain:  []string{"tool_response_limit"},
			mustNotHave:  []string{"LESS", "MORE", "activity_max_response_size"},
		},
		{
			// The only combination prompt_get actually reaches today.
			name:         "prompt_get/storage only",
			recordType:   "prompt_get",
			storage:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesDelivered,
			wantNotice:   true,
			mustContain:  []string{"activity_max_response_size", "MORE"},
			mustNotHave:  []string{"tool_response_limit", "LESS"},
		},
		{
			name:         "prompt_get/both",
			recordType:   "prompt_get",
			forward:      true,
			storage:      true,
			relation:     StoredVsDeliveredUnknown,
			bytesSubject: ResponseBytesDescribesNeither,
			wantNotice:   true,
			mustContain:  []string{"tool_response_limit", "activity_max_response_size"},
			mustNotHave:  []string{"LESS", "MORE"},
		},
	}

	seen := make(map[string]bool, len(tests))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveResponseTruncation(tc.recordType, tc.forward, tc.storage)

			assert.Equal(t, tc.relation, got.Relation, "stored-vs-delivered relation")
			assert.Equal(t, tc.bytesSubject, got.BytesSubject, "what response_bytes measures")
			assert.Equal(t, tc.forward, got.ForwardTruncated)
			assert.Equal(t, tc.storage, got.StorageTruncated)
			assert.Equal(t, tc.forward || tc.storage, got.Truncated())

			if !tc.wantNotice {
				assert.Empty(t, got.Notice, "nothing was cut, so there is nothing to say")
				return
			}
			require.NotEmpty(t, got.Notice)
			for _, want := range tc.mustContain {
				assert.Contains(t, got.Notice, want)
			}
			for _, unwanted := range tc.mustNotHave {
				assert.NotContains(t, got.Notice, unwanted)
			}
		})
		seen[tc.recordType+"/"+boolPair(tc.forward, tc.storage)] = true
	}

	// The table must stay the FULL cross product; a cell quietly dropped is how
	// a wrong claim survives a review round.
	for _, rt := range []string{"tool_call", "internal_tool_call", "prompt_get"} {
		for _, f := range []bool{false, true} {
			for _, s := range []bool{false, true} {
				assert.True(t, seen[rt+"/"+boolPair(f, s)],
					"missing cell %s forward=%v storage=%v", rt, f, s)
			}
		}
	}
	assert.Len(t, seen, 12, "3 record types x 4 flag combinations")
}

func boolPair(a, b bool) string {
	return strings.Join([]string{boolStr(a), boolStr(b)}, ",")
}

func boolStr(b bool) string {
	if b {
		return "t"
	}
	return "f"
}

// An unrecognised type must inherit prompt_get's direction-free treatment, not
// tool_call's or internal_tool_call's. A new emitter has to establish which side
// it records; it must not silently acquire a claim.
func TestResolveResponseTruncation_UnknownTypeClaimsNoDirection(t *testing.T) {
	for _, unknown := range []string{"", "policy_decision", "some_future_type"} {
		got := ResolveResponseTruncation(unknown, true, false)
		assert.Equal(t, StoredVsDeliveredUnknown, got.Relation, unknown)
		assert.NotContains(t, got.Notice, "LESS", unknown)
		assert.NotContains(t, got.Notice, "MORE", unknown)
		assert.Equal(t, ResolveResponseTruncation("prompt_get", true, false), got, unknown)
	}
}

// response_bytes is the PRE-forward upstream size. Attaching it to "what the
// agent received" whenever a forward cut happened overstates delivery by exactly
// what the cut removed, on every type.
func TestNoticeWithBytes_NeverAttributesPreForwardBytesToABody(t *testing.T) {
	for _, recordType := range []string{"tool_call", "internal_tool_call", "prompt_get"} {
		for _, storage := range []bool{false, true} {
			got := ResolveResponseTruncation(recordType, true, storage)
			line := got.NoticeWithBytes(200039)
			require.Contains(t, line, "200039", "%s storage=%v", recordType, storage)
			if got.BytesSubject == ResponseBytesDescribesNeither {
				assert.Contains(t, line, "describes neither", "%s storage=%v", recordType, storage)
				assert.NotContains(t, line, "is what the agent received",
					"%s storage=%v", recordType, storage)
			}
		}
	}
}

// With no forward cut in play the pre-truncation size IS what was delivered,
// and saying so is the whole point of quoting it.
func TestNoticeWithBytes_StorageOnlyQuotesDeliveredSize(t *testing.T) {
	got := ResolveResponseTruncation("tool_call", false, true)
	assert.Equal(t, ResponseBytesDescribesDelivered, got.BytesSubject)
	assert.Contains(t, got.NoticeWithBytes(4096), "response_bytes (4096) is what the agent received")
}

// A forward-truncated built-in records the pre-forward text, so the pre-forward
// byte count measures THIS record — the one cell where that is true.
func TestNoticeWithBytes_ForwardTruncatedBuiltinQuotesTheStoredSize(t *testing.T) {
	got := ResolveResponseTruncation("internal_tool_call", true, false)
	assert.Equal(t, ResponseBytesDescribesStored, got.BytesSubject)
	assert.Contains(t, got.NoticeWithBytes(4096), "is the size of this recorded text")
}

// Zero means UNKNOWN, not free: legacy records predate the measurement and the
// internal emitter never populates the byte fields at all. Printing "0 bytes"
// would read as an empty response.
func TestNoticeWithBytes_UnknownByteCountIsOmittedNotPrinted(t *testing.T) {
	for _, n := range []int{0, -1} {
		got := ResolveResponseTruncation("tool_call", true, true)
		line := got.NoticeWithBytes(n)
		assert.Equal(t, got.Notice, line)
		assert.NotContains(t, line, "0 bytes")
		assert.NotContains(t, line, "response_bytes")
	}
}

// Nothing cut means nothing to say, in every combination of type and bytes.
func TestNoticeWithBytes_NoCutSaysNothing(t *testing.T) {
	got := ResolveResponseTruncation("tool_call", false, false)
	assert.Empty(t, got.Notice)
	assert.Empty(t, got.NoticeWithBytes(120))
}
