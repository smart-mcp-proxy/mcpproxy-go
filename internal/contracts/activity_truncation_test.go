package contracts

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The regression guard for the whole feature.
//
// FIVE review rounds tripped on the same thing: they tried to recover the
// response cut's direction at RENDER time, from the record type. Each was
// correct for the emitter population it knew and wrong about one it did not —
// round 4's per-type table was defeated by code-execution sub-calls, which are
// Type=tool_call records whose cut runs the OPPOSITE way from every other
// tool_call's (a log-side cut at subCallActivityResponseLimit, not a forward
// cut at tool_response_limit).
//
// So the type is gone from this resolver, and the direction arrives as a stamp
// the EMITTER wrote. This table is the full cross product of that stamp
// (including the unstamped legacy state) against the two flags, asserted from
// the emitters, not from anyone's comment:
//
//	internal/server/mcp.go:2730, mcp.go:3185, mcp_routing.go:621
//	    tool_call: response = the join of the FORWARDED blocks, so agent and
//	    record hold the same cut copy  -> CutShortenedAgentAndRecord
//	internal/server/mcp.go:2112 (retrieve_tools), mcp.go:2755 (call_tool_*
//	    mirror): fed the PRE-cut value, agent got it cut
//	                                   -> CutShortenedAgentOnly
//	internal/server/mcp_code_execution.go:816: the sandbox got the whole result,
//	    only the recorded text was cut at 8KB
//	                                   -> CutShortenedRecordOnly
//	internal/runtime/activity_service.go: the storage cut, applied to whatever
//	    the emitter handed over — one direction, always the record
//
// If a cell here ever needs to change, an emitter changed — fix the emitter or
// its stamp, never a renderer.
func TestResolveResponseTruncation_FullCrossProduct(t *testing.T) {
	tests := []struct {
		name         string
		cut          ResponseCut
		forward      bool
		storage      bool
		stamped      bool
		relation     StoredVsDelivered
		bytesSubject ResponseBytesSubject
		wantNotice   bool
		mustContain  []string
		mustNotHave  []string
	}{
		// ---- no cut stamped, no cut flagged ---------------------------
		{
			name:         "unstamped/neither",
			relation:     StoredEqualsDelivered,
			bytesSubject: ResponseBytesDescribesBoth,
			stamped:      true,
		},
		{
			// The one cell that never needed a stamp: this cut has only ever
			// had one direction.
			name:         "unstamped/storage only",
			storage:      true,
			stamped:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesDelivered,
			wantNotice:   true,
			mustContain:  []string{"activity_max_response_size", "MORE"},
			mustNotHave:  []string{"tool_response_limit", "LESS"},
		},
		{
			// A record written before the stamp existed. It must claim NO
			// direction — borrowing another emitter's is the whole bug.
			name:         "unstamped/response only (legacy record)",
			forward:      true,
			relation:     StoredVsDeliveredUnknown,
			bytesSubject: ResponseBytesDescribesNeither,
			wantNotice:   true,
			mustContain:  []string{"older mcpproxy"},
			mustNotHave:  []string{"LESS", "MORE", "agent's own copy", "activity_max_response_size"},
		},
		{
			name:         "unstamped/both (legacy record)",
			forward:      true,
			storage:      true,
			relation:     StoredVsDeliveredUnknown,
			bytesSubject: ResponseBytesDescribesNeither,
			wantNotice:   true,
			mustContain:  []string{"older mcpproxy", "activity_max_response_size"},
			mustNotHave:  []string{"LESS", "MORE", "agent's own copy"},
		},

		// ---- CutShortenedAgentAndRecord --------------------------------
		{
			name:         "agent_and_record/neither",
			cut:          CutShortenedAgentAndRecord,
			stamped:      true,
			relation:     StoredEqualsDelivered,
			bytesSubject: ResponseBytesDescribesBoth,
		},
		{
			name:         "agent_and_record/storage only",
			cut:          CutShortenedAgentAndRecord,
			storage:      true,
			stamped:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesDelivered,
			wantNotice:   true,
			mustContain:  []string{"activity_max_response_size", "MORE"},
			mustNotHave:  []string{"tool_response_limit", "LESS"},
		},
		{
			// The record is the join of the blocks that were forwarded, so it
			// starts life as exactly the delivered copy.
			name:         "agent_and_record/response only",
			cut:          CutShortenedAgentAndRecord,
			forward:      true,
			stamped:      true,
			relation:     StoredEqualsDelivered,
			bytesSubject: ResponseBytesDescribesNeither,
			wantNotice:   true,
			mustContain:  []string{"tool_response_limit", "agent's own copy"},
			mustNotHave:  []string{"LESS", "MORE", "activity_max_response_size"},
		},
		{
			// The cell round 2 and round 3 got wrong: the storage cut shortens
			// the ALREADY-forwarded text again, leaving the record strictly
			// shorter than what the agent got.
			name:         "agent_and_record/both",
			cut:          CutShortenedAgentAndRecord,
			forward:      true,
			storage:      true,
			stamped:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesNeither,
			wantNotice:   true,
			mustContain:  []string{"tool_response_limit", "activity_max_response_size", "MORE"},
			mustNotHave:  []string{"LESS", "agent's own copy"},
		},

		// ---- CutShortenedAgentOnly -------------------------------------
		{
			name:         "agent_only/neither",
			cut:          CutShortenedAgentOnly,
			stamped:      true,
			relation:     StoredEqualsDelivered,
			bytesSubject: ResponseBytesDescribesBoth,
		},
		{
			name:         "agent_only/storage only",
			cut:          CutShortenedAgentOnly,
			storage:      true,
			stamped:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesDelivered,
			wantNotice:   true,
			mustContain:  []string{"activity_max_response_size", "MORE"},
			mustNotHave:  []string{"tool_response_limit", "LESS"},
		},
		{
			// The ONE stamp for which "recorded > delivered" holds.
			name:         "agent_only/response only",
			cut:          CutShortenedAgentOnly,
			forward:      true,
			stamped:      true,
			relation:     StoredLargerThanDelivered,
			bytesSubject: ResponseBytesDescribesStored,
			wantNotice:   true,
			mustContain:  []string{"tool_response_limit", "LESS"},
			mustNotHave:  []string{"MORE", "activity_max_response_size"},
		},
		{
			// Two cuts pointing opposite ways under two unrelated limits. The
			// honest answer is that the order is not decidable.
			name:         "agent_only/both",
			cut:          CutShortenedAgentOnly,
			forward:      true,
			storage:      true,
			stamped:      true,
			relation:     StoredVsDeliveredUnknown,
			bytesSubject: ResponseBytesDescribesNeither,
			wantNotice:   true,
			mustContain:  []string{"tool_response_limit", "activity_max_response_size", "Neither body"},
			mustNotHave:  []string{"LESS", "MORE"},
		},

		// ---- CutShortenedRecordOnly ------------------------------------
		{
			name:         "record_only/neither",
			cut:          CutShortenedRecordOnly,
			stamped:      true,
			relation:     StoredEqualsDelivered,
			bytesSubject: ResponseBytesDescribesBoth,
		},
		{
			name:         "record_only/storage only",
			cut:          CutShortenedRecordOnly,
			storage:      true,
			stamped:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesDelivered,
			wantNotice:   true,
			mustContain:  []string{"activity_max_response_size", "MORE"},
			mustNotHave:  []string{"tool_response_limit", "LESS"},
		},
		{
			// A code-execution sub-call. This is a Type=tool_call record, and
			// round 4's per-type table called it "the agent's own copy" — it is
			// a PREFIX of the agent's copy. Nothing here may say
			// tool_response_limit: this cut is not that limit.
			name:         "record_only/response only",
			cut:          CutShortenedRecordOnly,
			forward:      true,
			stamped:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesDelivered,
			wantNotice:   true,
			mustContain:  []string{"MORE", "activity log"},
			mustNotHave:  []string{"LESS", "agent's own copy", "tool_response_limit", "activity_max_response_size"},
		},
		{
			// Both cuts shorten the SAME body, so they cannot flip the order.
			name:         "record_only/both",
			cut:          CutShortenedRecordOnly,
			forward:      true,
			storage:      true,
			stamped:      true,
			relation:     StoredSmallerThanDelivered,
			bytesSubject: ResponseBytesDescribesDelivered,
			wantNotice:   true,
			mustContain:  []string{"MORE", "activity log", "activity_max_response_size"},
			mustNotHave:  []string{"LESS", "agent's own copy", "tool_response_limit"},
		},
	}

	seen := make(map[string]bool, len(tests))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveResponseTruncation(tc.cut, tc.forward, tc.storage)

			assert.Equal(t, tc.relation, got.Relation, "stored-vs-delivered relation")
			assert.Equal(t, tc.bytesSubject, got.BytesSubject, "what response_bytes measures")
			assert.Equal(t, tc.cut, got.Cut)
			assert.Equal(t, tc.stamped, got.Stamped)
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
		seen[string(tc.cut)+"/"+boolPair(tc.forward, tc.storage)] = true
	}

	// The table must stay the FULL cross product; a cell quietly dropped is how
	// a wrong claim survives a review round.
	for _, c := range []ResponseCut{CutNone, CutShortenedAgentAndRecord, CutShortenedAgentOnly, CutShortenedRecordOnly} {
		for _, f := range []bool{false, true} {
			for _, s := range []bool{false, true} {
				assert.True(t, seen[string(c)+"/"+boolPair(f, s)],
					"missing cell cut=%q response=%v storage=%v", c, f, s)
			}
		}
	}
	assert.Len(t, seen, 16, "4 stamps (incl. unstamped) x 4 flag combinations")
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

// The resolver no longer takes a record type, and that is the point: the two
// tool_call populations disagree about direction, so any answer derived from
// the type is wrong for one of them.
//
// This is the round-4 regression, stated as an assertion: an ordinary tool_call
// forward truncation and a code-execution sub-call are BOTH Type=tool_call with
// response_truncated=true, and their relations point opposite ways.
func TestResolveResponseTruncation_TwoToolCallPopulationsDisagree(t *testing.T) {
	ordinary := ResolveResponseTruncation(CutShortenedAgentAndRecord, true, false)
	subCall := ResolveResponseTruncation(CutShortenedRecordOnly, true, false)

	require.NotEqual(t, ordinary.Relation, subCall.Relation,
		"the two tool_call populations must not resolve to the same direction")
	assert.Equal(t, StoredEqualsDelivered, ordinary.Relation)
	assert.Equal(t, StoredSmallerThanDelivered, subCall.Relation)
	assert.NotEqual(t, ordinary.Notice, subCall.Notice)
}

// An unrecognised stamp — a value from a NEWER core read by an older binary —
// must be treated as unstamped, never trusted and never mapped onto a
// neighbouring direction.
func TestResolveResponseTruncation_UnknownStampClaimsNoDirection(t *testing.T) {
	legacy := ResolveResponseTruncation(CutNone, true, false)
	for _, unknown := range []ResponseCut{"agent", "record", "some_future_cut", "AGENT_ONLY"} {
		got := ResolveResponseTruncation(unknown, true, false)
		assert.Equal(t, StoredVsDeliveredUnknown, got.Relation, string(unknown))
		assert.False(t, got.Stamped, string(unknown))
		assert.NotContains(t, got.Notice, "LESS", string(unknown))
		assert.NotContains(t, got.Notice, "MORE", string(unknown))
		assert.Equal(t, legacy.Notice, got.Notice, string(unknown))
	}
}

// ResponseCut.Cuts is what derives the record's boolean at emission, so the two
// can never disagree on a record written by a current core.
func TestResponseCut_CutsAndValid(t *testing.T) {
	assert.False(t, CutNone.Cuts())
	for _, c := range []ResponseCut{CutShortenedAgentAndRecord, CutShortenedAgentOnly, CutShortenedRecordOnly} {
		assert.True(t, c.Cuts(), string(c))
		assert.True(t, c.Valid(), string(c))
	}
	assert.True(t, CutNone.Valid())
	assert.False(t, ResponseCut("nonsense").Valid())
	assert.Len(t, ValidResponseCuts, 3)
}

// response_bytes is the emitter's PRE-cut measurement. Attaching it to "what the
// agent received" whenever the cut shortened the agent's copy overstates
// delivery by exactly what the cut removed.
func TestNoticeWithBytes_NeverAttributesPreCutBytesToABody(t *testing.T) {
	for _, cut := range []ResponseCut{CutNone, CutShortenedAgentAndRecord, CutShortenedAgentOnly, CutShortenedRecordOnly} {
		for _, storage := range []bool{false, true} {
			got := ResolveResponseTruncation(cut, true, storage)
			line := got.NoticeWithBytes(200039)
			require.Contains(t, line, "200039", "%s storage=%v", cut, storage)
			if got.BytesSubject == ResponseBytesDescribesNeither {
				assert.Contains(t, line, "describes neither", "%s storage=%v", cut, storage)
				assert.NotContains(t, line, "the response the agent received",
					"%s storage=%v", cut, storage)
			}
		}
	}
}

// With no cut on the agent's copy the pre-cut size IS what was delivered, and
// saying so is the whole point of quoting it. True for the storage-only cell
// and for a record_only cut, whose delivered body was never shortened at all.
func TestNoticeWithBytes_QuotesDeliveredSizeWhenTheAgentsCopyWasNotCut(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cut     ResponseCut
		forward bool
		storage bool
	}{
		{"storage only", CutNone, false, true},
		{"sub-call log cut", CutShortenedRecordOnly, true, false},
		{"sub-call log cut plus storage", CutShortenedRecordOnly, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveResponseTruncation(tc.cut, tc.forward, tc.storage)
			assert.Equal(t, ResponseBytesDescribesDelivered, got.BytesSubject)
			assert.Contains(t, got.NoticeWithBytes(4096),
				"response_bytes (4096) measures the response the agent received")
		})
	}
}

// A built-in records the pre-cut text, so the pre-cut byte count measures THIS
// record — the one cell where that is true.
func TestNoticeWithBytes_AgentOnlyCutQuotesTheStoredSize(t *testing.T) {
	got := ResolveResponseTruncation(CutShortenedAgentOnly, true, false)
	assert.Equal(t, ResponseBytesDescribesStored, got.BytesSubject)
	assert.Contains(t, got.NoticeWithBytes(4096), "is the size of this recorded text")
}

// Zero means UNKNOWN, not free: legacy records predate the measurement, and a
// policy-refused sub-call records a true zero. Printing "0 bytes" would read as
// an empty response.
func TestNoticeWithBytes_UnknownByteCountIsOmittedNotPrinted(t *testing.T) {
	for _, n := range []int{0, -1} {
		got := ResolveResponseTruncation(CutShortenedAgentAndRecord, true, true)
		line := got.NoticeWithBytes(n)
		assert.Equal(t, got.Notice, line)
		assert.NotContains(t, line, "0 bytes")
		assert.NotContains(t, line, "response_bytes")
	}
}

// The doc on NoticeWithBytes claimed built-ins never populate the byte fields.
// They do — event_bus.go sets response_bytes on every internal emission — and a
// comment that says otherwise tells a reader to ignore a figure that is there.
func TestNoticeWithBytesDocDoesNotDenyBuiltinByteCounts(t *testing.T) {
	doc := readSourceFile(t, "activity_truncation.go")
	i := strings.Index(doc, "func (t ResponseTruncation) NoticeWithBytes")
	require.Positive(t, i, "NoticeWithBytes must exist")
	// The doc comment is the run of lines immediately above the func.
	head := doc[:i]
	start := strings.LastIndex(head, "\n\n")
	require.Positive(t, start)
	comment := head[start:]

	assert.NotContains(t, comment, "never populates the byte fields")
	assert.Contains(t, comment, "EmitActivityInternalToolCall",
		"the doc must name where built-ins DO populate response_bytes")
}

// "response_bytes is what the agent received" was inexact even where the
// subject IS the delivered body: the emitter measures the payload BEFORE
// spotlightForwarded adds source delimiters (Spec 054) and before TOON
// re-encodes text blocks (Spec 084). Both are off by default, so the claim is
// qualified rather than deleted — deleting it would leave the only cost signal
// on a bodies-off export unexplained.
func TestDeliveredBytesClauseIsQualifiedNotAbsolute(t *testing.T) {
	src := readSourceFile(t, "activity_truncation.go")

	assert.NotContains(t, src, "is what the agent received.",
		"the unqualified claim is inexact once Spec 054 or Spec 084 is on")
	assert.Contains(t, src, "measures the response the agent received",
		"the clause must survive: it is the only cost signal a bodies-off export carries")

	for _, cite := range []string{"spotlightForwarded", "Spec 054", "TOON", "Spec 084"} {
		assert.Contains(t, src, cite,
			"name what rewrites the payload after the measurement, so a reader can check it")
	}
}

// The byte-count doc on the API record claimed every code-execution sub-call
// records both counts as zero. subCallByteSizes measures them; only a policy
// refusal still records a zero response, and that zero is TRUE.
func TestActivityRecordByteDocDoesNotClaimSubCallsRecordZero(t *testing.T) {
	src := readSourceFile(t, "activity.go")

	assert.NotContains(t, src, "code-execution sub-calls record both as zero")
	assert.Contains(t, src, "subCallByteSizes")
	assert.Contains(t, src, "emitSubCallRefused",
		"name the one path where a zero is real, so a consumer can distinguish it")
}

// Nothing cut means nothing to say, whatever the stamp.
func TestNoticeWithBytes_NoCutSaysNothing(t *testing.T) {
	for _, cut := range []ResponseCut{CutNone, CutShortenedAgentAndRecord, CutShortenedAgentOnly, CutShortenedRecordOnly} {
		got := ResolveResponseTruncation(cut, false, false)
		assert.Empty(t, got.Notice, string(cut))
		assert.Empty(t, got.NoticeWithBytes(120), string(cut))
	}
}

// readSourceFile reads a file from this package's own directory. Used by the
// doc-assertion above: a comment that contradicts the code is exactly the class
// of defect five review rounds were spent on, so the false claims are pinned
// out rather than merely deleted.
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	require.NoError(t, err)
	return string(data)
}
