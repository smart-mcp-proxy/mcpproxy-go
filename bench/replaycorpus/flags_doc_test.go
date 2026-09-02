package replaycorpus

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ReasonInternalNoByteCounts's doc still described the world before Spec 103:
// "the internal tool-call emission never populates the byte fields, so EVERY
// built-in call is unaccountable with bodies off".
//
// Runtime.EmitActivityInternalToolCallTruncated (internal/runtime/event_bus.go)
// populates request_bytes and response_bytes on every internal emission, and
// TestResponseCost_UntruncatedInternalIsNowAccountable already pins the
// behavioural half of that. The comment is the half a reader reaches first: it
// tells whoever is reading an exclusion report to write off a whole class of
// record as unmeasurable when it is measured, which is how an operator stops
// investigating a gap that is really a stale corpus.
//
// This is the same stale claim T4a removed from NoticeWithBytes
// (TestNoticeWithBytesDocDoesNotDenyBuiltinByteCounts, internal/contracts), one
// constant over — so it is pinned out here rather than merely deleted.
func TestInternalByteGapDocDoesNotClaimTheEmitterNeverMeasures(t *testing.T) {
	src := readFlagsSource(t)
	doc := docCommentAbove(t, src, "ReasonInternalNoByteCounts ExclusionReason")

	assert.NotContains(t, doc, "never populates the byte fields",
		"the emitter does populate them; a doc that says otherwise tells an operator to "+
			"ignore a figure that is there")
	assert.NotContains(t, doc, "EVERY built-in call",
		"the gap is no longer systematic, so it must not be described as universal")
	assert.Contains(t, doc, "EmitActivityInternalToolCallTruncated",
		"name where built-ins DO record their byte counts, so a reader can check it")
	assert.Contains(t, doc, "no longer systematic",
		"say plainly what this reason means today: an old corpus or a genuinely empty body")
}

// readFlagsSource reads this package's flags.go from its own directory.
func readFlagsSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("flags.go")
	require.NoError(t, err)
	return string(data)
}

// docCommentAbove returns the run of comment lines immediately above decl.
func docCommentAbove(t *testing.T, src, decl string) string {
	t.Helper()
	i := strings.Index(src, decl)
	require.Positive(t, i, "declaration %q must exist", decl)
	head := src[:i]
	start := strings.LastIndex(head, "\n\n")
	require.Positive(t, start, "declaration %q must carry a doc comment", decl)
	return head[start:]
}
