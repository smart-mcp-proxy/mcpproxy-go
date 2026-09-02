package storage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// The doc comment on the truncation fields is what the NEXT consumer author
// reads before deciding whether to exclude a record's cost, so it carries more
// weight than either rendered surface. Its history is the history of this bug:
//
//	round 2 stated "the RECORDED response is LARGER than the one the agent
//	  received" as a property of ResponseTruncated. False for a tool_call,
//	  whose record holds the POST-forward text.
//	round 3 wrote that correction as an unconditional property of the TYPE
//	  ("ActivityTypeToolCall: the RECORDED response IS the delivered one"),
//	  with the both-flags exception parked 25 lines below.
//	round 4 built a per-(type, flags) table. False for code-execution
//	  sub-calls, which are Type=tool_call records cut on the LOG side.
//
// Every one of those was an attempt to state the direction here, in prose,
// keyed on the type. The direction is not a function of the type, so these
// tests now assert the opposite of what they used to: that the doc REFUSES to
// derive a direction and hands off to the emitter's stamp.
func truncationFieldDoc(t *testing.T, fieldName string) string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "activity_models.go", nil, parser.ParseComments)
	require.NoError(t, err)

	var doc string
	ast.Inspect(file, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if !ok || len(field.Names) != 1 || field.Names[0].Name != fieldName {
			return true
		}
		if field.Doc != nil {
			doc = field.Doc.Text()
		}
		return false
	})
	require.NotEmpty(t, doc, "%s must carry a doc comment", fieldName)
	return doc
}

// The stamp's own doc has to say WHY it exists, or the next author deletes it
// as redundant with the type and re-derives round 4.
func TestStampDocForbidsDerivingTheDirectionFromTheType(t *testing.T) {
	doc := truncationFieldDoc(t, "ResponseTruncationCut")

	require.Contains(t, doc, "DO NOT READ A DIRECTION OUT OF THE RECORD TYPE",
		"the doc must forbid the exact inference five rounds kept making")
	require.Contains(t, doc, "mcp_code_execution.go",
		"name the counterexample: a Type=tool_call record whose cut runs the other way")
	require.Contains(t, doc, "subCallActivityResponseLimit",
		"name the limit that makes that record different, so a reader can check it")
	require.Contains(t, doc, "contracts.ResolveResponseTruncation",
		"the doc must name the single resolver")
	require.Contains(t, doc, "internal/contracts/activity_truncation.go")
}

// An unstamped record is a LEGACY record and nothing else. If the doc ever
// suggests a current emitter can leave the stamp off, a consumer will start
// filling one in from the type again.
func TestStampDocExplainsTheEmptyValue(t *testing.T) {
	doc := truncationFieldDoc(t, "ResponseTruncationCut")

	require.Contains(t, doc, "UNSTATED")
	require.Contains(t, doc, "before this field existed",
		"the empty-with-flag state must be identified as a legacy record")
	require.Contains(t, doc, "contracts.ResponseCut.Cuts",
		"say WHY a current core cannot produce it: the boolean is derived from the stamp")
}

// None of the three superseded claims may come back, in any field's doc.
func TestTruncationDocsCarryNoneOfTheSupersededClaims(t *testing.T) {
	doc := truncationFieldDoc(t, "ResponseTruncationCut") +
		truncationFieldDoc(t, "ResponseStorageTruncated")

	for _, banned := range []string{
		// Round 2: the direction as a property of the flag.
		"means the RECORDED response is LARGER",
		// Round 3: the direction as an unconditional property of the type.
		"the RECORDED response IS the delivered one.",
		// Round 4: the direction as a property of (type, flags).
		"which side was recorded depends on Type",
	} {
		require.NotContains(t, doc, banned, "superseded claim resurfaced: %q", banned)
	}
}

// The storage cut is the one that never needed a stamp, and the doc must say so
// for a reason a reader can check — otherwise the next round adds a second
// stamp nobody needs, or drops the first one as symmetric noise.
func TestStorageCutDocClaimsItsSingleDirection(t *testing.T) {
	doc := truncationFieldDoc(t, "ResponseStorageTruncated")

	require.Contains(t, doc, "only ever had one direction")
	require.Contains(t, doc, "prefix of what the emitter handed over")
	require.Contains(t, doc, "depends on the STAMP, not the type",
		"the both-flags answer must point at the stamp")
}

// The predicate the doc cites must still read the stamp. If someone narrows it
// back to a type test, the citation goes stale silently — and a stale citation
// is how the universal claim got written in the first place.
func TestStampedRecordsCarryBothHalvesTogether(t *testing.T) {
	rec := &ActivityRecord{
		Type:                  ActivityTypeToolCall,
		ResponseTruncated:     true,
		ResponseTruncationCut: contracts.CutShortenedRecordOnly,
	}

	// The counterexample as data: a tool_call record whose stamp is the one an
	// ordinary tool_call can never carry.
	require.Equal(t, ActivityTypeToolCall, rec.Type)
	require.Equal(t, contracts.CutShortenedRecordOnly, rec.ResponseTruncationCut)
	require.NotEqual(t, contracts.CutShortenedAgentAndRecord, rec.ResponseTruncationCut,
		"the two tool_call populations must remain distinguishable on the record")

	require.Equal(t, ActivityType("tool_call"), ActivityTypeToolCall)
	require.Equal(t, ActivityType("internal_tool_call"), ActivityTypeInternalToolCall)
}

// The stamp has to survive a BBolt round trip, or every record read back is a
// legacy record and the resolver goes direction-free for the whole log.
func TestStampSurvivesBinaryRoundTrip(t *testing.T) {
	for _, cut := range []contracts.ResponseCut{
		contracts.CutNone,
		contracts.CutShortenedAgentAndRecord,
		contracts.CutShortenedAgentOnly,
		contracts.CutShortenedRecordOnly,
	} {
		original := &ActivityRecord{
			ID:                    "01ABC",
			Type:                  ActivityTypeToolCall,
			ResponseTruncated:     cut.Cuts(),
			ResponseTruncationCut: cut,
		}
		encoded, err := original.MarshalBinary()
		require.NoError(t, err)

		var decoded ActivityRecord
		require.NoError(t, decoded.UnmarshalBinary(encoded))
		require.Equal(t, cut, decoded.ResponseTruncationCut, string(cut))
		require.Equal(t, cut.Cuts(), decoded.ResponseTruncated, string(cut))
	}

	// And the wire name is what every other surface reads it by.
	encoded, err := (&ActivityRecord{
		ResponseTruncated:     true,
		ResponseTruncationCut: contracts.CutShortenedRecordOnly,
	}).MarshalBinary()
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"response_truncation_cut":"record_only"`)
}

// A record written by an older core has no stamp and must decode as UNSTATED,
// not as some default direction.
func TestLegacyRecordDecodesWithNoStamp(t *testing.T) {
	var decoded ActivityRecord
	require.NoError(t, decoded.UnmarshalBinary(
		[]byte(`{"id":"01ABC","type":"tool_call","response_truncated":true}`)))

	require.True(t, decoded.ResponseTruncated)
	require.Equal(t, contracts.CutNone, decoded.ResponseTruncationCut)
	require.False(t,
		contracts.ResolveResponseTruncation(
			decoded.ResponseTruncationCut, decoded.ResponseTruncated, false).Stamped,
		"a legacy record must resolve as unstamped, so nothing claims a direction for it")
}

// The doc block is unusually long, and length is how a wrong sentence hides. If
// it grows a table of directions again, that table will be copied.
func TestStampDocDoesNotRestateADirectionTable(t *testing.T) {
	doc := truncationFieldDoc(t, "ResponseTruncationCut")
	lower := strings.ToLower(doc)

	require.NotContains(t, lower, "stored == delivered",
		"a copyable direction table in a comment is what four renderers copied")
	require.NotContains(t, lower, "stored > delivered")
	require.NotContains(t, lower, "stored < delivered")
}
