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
