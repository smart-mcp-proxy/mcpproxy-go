package replaycorpus

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These two doc comments are consumer-facing REASONING, not decoration: they are
// what a maintainer reads before deciding whether a byte figure may be counted,
// and both stated the truncation direction universally when it is not universal.
//
// A wrong sentence in a doc comment cannot be caught by exercising behaviour —
// the behaviour was already right — so the source text is the thing under test.
// It is worth a test because the failure mode here is precisely that a false
// claim gets copied out of a comment into a renderer, which has now happened
// four times.
func TestTruncationDocsDoNotRestateTheUniversalClaim(t *testing.T) {
	for _, tc := range []struct {
		file string
		// falsified is the exact wording that was wrong. Both halves of each
		// were backwards: an upstream tool_call is cut on the way OUT (not into
		// the log), and its record holds the agent's copy or less (never the
		// whole thing the agent consumed).
		falsified []string
		// required pins the correction to the ONE authority, so a future edit
		// re-derives nothing.
		required []string
	}{
		{
			file: "flags.go",
			falsified: []string{
				"truncation cuts the STORED text while the agent consumed the whole thing",
				"so the pre-truncation byte length is an honest estimate",
			},
			required: []string{
				"contracts.ResolveResponseTruncation",
				"internal/contracts/activity_truncation.go",
			},
		},
		{
			file: "load.go",
			falsified: []string{
				"storageTruncated is the OPPOSITE of truncated",
			},
			required: []string{
				"contracts.ResolveResponseTruncation",
				"internal/contracts/activity_truncation.go",
			},
		},
	} {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			src := string(raw)

			for _, claim := range tc.falsified {
				assert.NotContains(t, src, claim,
					"this claim is false for every record type; see the direction matrix in "+
						"internal/contracts/activity_truncation.go")
			}
			for _, want := range tc.required {
				assert.Contains(t, src, want,
					"the corrected doc must point at the single resolver, not restate the table")
			}
		})
	}
}

// Round 4's residue: this doc still asserted that a code-execution sub-call
// records BOTH byte counts as zero. That stopped being true when
// subCallByteSizes began measuring them (internal/server/mcp_code_execution.go);
// the only sub-call path that still records a zero response is a policy
// REFUSAL, and that zero is TRUE rather than unknown.
//
// Worth pinning because the claim reads as a systematic pipeline gap, and a
// maintainer acting on it would build an estimator for a population that no
// longer needs one — or, worse, distrust byte figures that are correct.
func TestSubCallZeroBytesDocIsNotStatedAsSystematic(t *testing.T) {
	raw, err := os.ReadFile("flags.go")
	require.NoError(t, err)
	src := string(raw)

	assert.NotContains(t, src, "records BOTH byte counts as zero",
		"sub-calls carry byte counts now; the only live zero is a policy refusal")
	assert.Contains(t, src, "emitSubCallRefused",
		"name the one path that still records a zero response, so the claim is checkable")
	assert.Contains(t, src, "subCallByteSizes",
		"name the function that made the systematic gap stop being systematic")
}

// The decoded record must carry the emitter's stamp, or every consumer in this
// package is back to guessing from `internal`.
func TestDecodedRecordCarriesTheStamp(t *testing.T) {
	assert.Contains(t, mustRead(t, "load.go"), "cut:              rec.ResponseTruncationCut",
		"the loader must copy the stamp off the exported record")
}

func mustRead(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	require.NoError(t, err)
	return string(raw)
}
