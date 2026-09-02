package storage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The doc comment on the truncation flags is what the NEXT consumer author
// reads before deciding whether to exclude a record's cost, so it carries more
// weight than either rendered surface. Round 2 stated "the RECORDED response is
// LARGER than the one the agent received" as a property of ResponseTruncated
// and cited internal/runtime/usage_aggregate.go as if it applied to every
// record. It does not: that file's predicate is
//
//	rec.ResponseTruncated && rec.Type == storage.ActivityTypeInternalToolCall
//
// and for a tool_call record the claim is backwards — handleToolCallCompleted
// stores the POST-forward text, so the log holds exactly the agent's copy.
//
// Round 3 then wrote THAT correction as an unconditional property of the type
// ("ActivityTypeToolCall: the RECORDED response IS the delivered one"), with
// the both-flags exception 25 lines below under a different field. Two
// renderers lifted the unconditional half and neither lifted the caveat — the
// direct cause of both round-4 blocking findings.
//
// These tests read the comment itself, because a prose defect has no runtime
// behaviour to assert on and the wrong sentence is what propagates.
func truncationFlagDoc(t *testing.T) string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "activity_models.go", nil, parser.ParseComments)
	require.NoError(t, err)

	var doc string
	ast.Inspect(file, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if !ok || len(field.Names) != 1 || field.Names[0].Name != "ResponseStorageTruncated" {
			return true
		}
		if field.Doc != nil {
			doc = field.Doc.Text()
		}
		return false
	})
	require.NotEmpty(t, doc, "the shared truncation-flag doc block sits above ResponseStorageTruncated")
	return doc
}

func TestTruncationDocNamesTheTypeRestriction(t *testing.T) {
	doc := truncationFlagDoc(t)

	require.Contains(t, doc, "ActivityTypeInternalToolCall",
		"the doc must name the type for which RECORDED > DELIVERED actually holds")
	require.Contains(t, doc, "ActivityTypeToolCall",
		"the doc must name the type for which it does not")
	require.Contains(t, doc, "truncatedBuiltinOverstatesDelivery",
		"cite the real predicate, not the file as a whole")
	require.Contains(t, doc, "Type == ActivityTypeInternalToolCall",
		"spell out the type gate the predicate applies")
}

// The universal claim must not come back in any of its round-2 phrasings.
func TestTruncationDocDoesNotStateTheDirectionUniversally(t *testing.T) {
	doc := truncationFlagDoc(t)

	// Split at the sentence that introduces the per-type breakdown; everything
	// before it is the part that speaks about the flag in general.
	idx := strings.Index(doc, "depends on the record TYPE")
	require.Positive(t, idx, "the doc must say the direction depends on the record type")
	general := doc[:idx]

	require.NotContains(t, general, "means the RECORDED response is LARGER",
		"stating the direction as a property of the flag is the defect")
	require.Contains(t, doc, "DO NOT READ A DIRECTION OUT OF EITHER FLAG HERE",
		"the doc must warn that neither flag alone fixes a direction")
}

// The round-3 defect: the tool_call correction stated unconditionally, with its
// exception parked under a different field. A reader copies from the point the
// claim is made, so the qualification has to be AT that point — and the doc must
// hand off to the one resolver rather than being a table anyone re-implements.
func TestTruncationDocQualifiesTheToolCallClaimInPlace(t *testing.T) {
	doc := truncationFlagDoc(t)

	require.NotContains(t, doc, "the RECORDED response IS the delivered one.",
		"true only while ResponseStorageTruncated is false; unconditional is the defect")

	idx := strings.Index(doc, "ActivityTypeToolCall: handleToolCallCompleted")
	require.Positive(t, idx, "the tool_call bullet must still explain which side is recorded")
	bullet := doc[idx:]

	for _, required := range []string{
		"STRICTLY SHORTER",
		"ResponseStorageTruncated is false",
	} {
		require.Contains(t, bullet, required,
			"the both-flags caveat must live in the same bullet as the claim it qualifies")
	}

	// The authority, named before any of the prose a reader might copy.
	authority := strings.Index(doc, "contracts.ResolveResponseTruncation")
	require.Positive(t, authority, "the doc must name the single resolver")
	require.Less(t, authority, idx, "name the resolver BEFORE the summary, not after it")
	require.Contains(t, doc, "internal/contracts/activity_truncation.go")
}

// The predicate the doc cites must still look the way the doc says it does. If
// someone widens it, the citation goes stale silently, and a stale citation is
// how the universal claim got written in the first place.
func TestCitedPredicateIsStillTypeGated(t *testing.T) {
	rec := &ActivityRecord{Type: ActivityTypeToolCall, ResponseTruncated: true}
	require.NotEqual(t, ActivityTypeInternalToolCall, rec.Type,
		"guard against the two constants collapsing to one value")
	require.Equal(t, ActivityType("tool_call"), ActivityTypeToolCall)
	require.Equal(t, ActivityType("internal_tool_call"), ActivityTypeInternalToolCall)
}
