package replaycorpus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// response_storage_truncated is the OPPOSITE of response_truncated: the
// activity log stored LESS than the agent received, because
// activity_max_response_size cut the text on the way into BBolt (issue #1173).
// response_bytes is measured pre-truncation, so the byte accounting is still
// exactly right.
//
// The loader must therefore NOT treat it as the Spec 103 overstatement. If it
// did — which is what OR-ing the two flags on the write path would produce —
// every internal record over 64KB would take the rec.truncated && rec.internal
// branch and be withheld as truncated_retrieve_tools_overstates, an inverted
// justification, and every code_execution touching such a payload would lose
// its saving. That population is exactly the one the cap creates.
//
// EVERY test here asserts BOTH body postures on the SAME record. That is not
// thoroughness for its own sake — it is what makes them bite. The loader's only
// use of the flag is the bodies-ON withhold branch in responseCost, so a
// bodies-off-only assertion holds identically whether the plumbing exists or
// not: delete `storageTruncated: rec.ResponseStorageTruncated` from admit() and
// a bodies-off test still passes, which is coverage that reads as a guard and
// is not one. The bodies-off half states the invariant (a storage cut costs
// nothing when nothing is tokenized); the bodies-on half is what fails when the
// plumbing goes away.

// TestStorageTruncatedCostIsBodiesGated pins both halves of the record-level
// behaviour: estimated from honest bytes with bodies off, withheld with bodies
// on because the stored text is a prefix.
func TestStorageTruncatedCostIsBodiesGated(t *testing.T) {
	line := record(t, map[string]any{
		"type":                       "internal_tool_call",
		"tool_name":                  "call_tool_read",
		"response":                   "only the first 64KB of a much longer payload",
		"response_storage_truncated": true,
		"response_bytes":             200_000,
	})

	t.Run("bodies off: estimated from the pre-truncation byte length", func(t *testing.T) {
		c := loadString(t, line, testOptions(t.TempDir()))
		call := firstCall(t, c)

		require.Equal(t, CostEstimated, call.ResponseCost.Basis,
			"a storage-truncated record's response_bytes is honest and must still be counted")
		assert.Equal(t, 200_000, call.ResponseCost.Bytes)
		assert.False(t, call.ResponseCost.Truncated,
			"Cost.Truncated is the OVERSTATEMENT annotation; it drives the code-exec withhold "+
				"and does not apply to a record whose byte count is exact")
		assert.False(t, call.Flags.Truncated,
			"the Spec 103 usability flag must not be raised by a storage cut")
		assert.Zero(t, c.Exclusions.Withheld[ReasonStorageTruncatedBodyUnmeasurable],
			"with bodies off nothing is tokenized, so a storage cut costs the loader nothing")
	})

	t.Run("bodies on: withheld, because the stored text is a prefix", func(t *testing.T) {
		opts := testOptions(t.TempDir())
		opts.Bodies = BodiesOnUnmasked

		c := loadString(t, line, opts)
		call := firstCall(t, c)

		require.Equal(t, CostUnavailable, call.ResponseCost.Basis,
			"the stored body is a prefix; tokenizing it as if whole understates the cost")
		assert.Equal(t, ReasonStorageTruncatedBodyUnmeasurable, call.ResponseCost.Reason)
		assert.Zero(t, c.Exclusions.Withheld[ReasonTruncatedRetrieveOverstates],
			"the log holds LESS than the agent received here, not more")
	})
}

// With bodies ON the stored text is a prefix, so it may not be tokenized. The
// cost is withheld under a reason that names the real cause — the capture-time
// cap — rather than being demoted to an estimate whose basis then disagrees
// with its measured siblings and surfaces as mixed_cost_basis.
func TestStorageTruncatedBodyIsWithheldUnderBodiesOn(t *testing.T) {
	opts := testOptions(t.TempDir())
	opts.Bodies = BodiesOnUnmasked

	c := loadString(t, record(t, map[string]any{
		"type":                       "internal_tool_call",
		"tool_name":                  "call_tool_read",
		"response":                   "only the first 64KB of a much longer payload",
		"response_storage_truncated": true,
		"response_bytes":             200_000,
	}), opts)

	call := firstCall(t, c)
	require.Equal(t, CostUnavailable, call.ResponseCost.Basis,
		"tokenizing a prefix as if it were the whole response understates the cost")
	assert.Equal(t, ReasonStorageTruncatedBodyUnmeasurable, call.ResponseCost.Reason)
	assert.NotEqual(t, ReasonTruncatedRetrieveOverstates, call.ResponseCost.Reason,
		"the overstatement reason is the wrong direction and must not be reused")
	assert.Equal(t, 1, c.Exclusions.Withheld[ReasonStorageTruncatedBodyUnmeasurable],
		"the degradation must be counted, not silent")
}

// The code-execution saving is the headline Spec 103 figure and the thing the
// OR-ing would have damaged most: a parent whose sub-call returned 200KB is
// exactly where the saving is largest.
//
// Same construction as above and for the same reason. Bodies off, every
// component is estimated, the bases agree, and a storage cut changes nothing —
// the saving is still computed. Bodies on, the cut component is unmeasurable
// and the aggregate is withheld under the reason that names WHY, which is the
// half that fails if the flag stops reaching the loader.
func TestCodeExecSavingUnderStorageTruncationIsBodiesGated(t *testing.T) {
	parentLine := marshalLine(t, map[string]any{
		"id": "p-1", "type": "internal_tool_call", "tool_name": CodeExecToolName,
		"status": "success", "work_session_id": "ws-1", "request_id": "parent-1",
		"timestamp": "2026-08-01T10:00:00Z", "request_bytes": 400, "response_bytes": 200,
		"arguments": map[string]any{"code": "await call_tool()"},
		"response":  "42",
	})
	subLine := marshalLine(t, map[string]any{
		"id": "s-1", "type": "tool_call", "server_name": "github", "tool_name": "list_issues",
		"status": "success", "work_session_id": "ws-1", "request_id": "sub-1",
		"parent_id": "parent-1", "timestamp": "2026-08-01T10:00:01Z",
		"request_bytes": 50, "response_bytes": 200_000,
		"arguments":                  map[string]any{"repo": "x"},
		"response":                   "only the first 64KB of a much longer payload",
		"response_storage_truncated": true,
	})
	corpus := parentLine + "\n" + subLine + "\n"

	t.Run("bodies off: the saving survives a storage-cut component", func(t *testing.T) {
		c := loadString(t, corpus, testOptions(t.TempDir()))

		parent := firstCall(t, c)
		require.Equal(t, CodeExecToolName, parent.ToolName)
		require.Len(t, parent.SubCalls, 1)

		got := CodeExecSavingFor(parent)
		require.Equal(t, CostEstimated, got.Basis,
			"a storage-truncated component must not withhold the whole saving; "+
				"its byte count is exact, so the aggregate is computable")
		assert.NotEqual(t, ReasonTruncatedSubCallOverstates, got.Reason)
		assert.Equal(t, 50+200_000, got.Baseline)
		assert.Equal(t, 400+200, got.Proxy)
	})

	t.Run("bodies on: the saving is withheld naming the storage cut", func(t *testing.T) {
		opts := testOptions(t.TempDir())
		opts.Bodies = BodiesOnUnmasked

		got := CodeExecSavingFor(firstCall(t, loadString(t, corpus, opts)))

		require.Equal(t, CostUnavailable, got.Basis,
			"one component of the sum is a prefix, so the sum is not measurable")
		assert.Equal(t, ReasonStorageTruncatedBodyUnmeasurable, got.Reason,
			"the withhold must name the capture-time cap, not mixed_cost_basis: "+
				"the operator's remedy is activity_max_response_size, and a symptom "+
				"name does not point at it")
	})
}

// The Spec 103 direction must STILL withhold, so the tests above cannot pass by
// the overstatement guard having been removed.
func TestCodeExecSavingStillWithheldBySpec103Truncation(t *testing.T) {
	parentLine := marshalLine(t, map[string]any{
		"id": "p-1", "type": "internal_tool_call", "tool_name": CodeExecToolName,
		"status": "success", "work_session_id": "ws-1", "request_id": "parent-1",
		"timestamp": "2026-08-01T10:00:00Z", "request_bytes": 400, "response_bytes": 200,
	})
	subLine := marshalLine(t, map[string]any{
		"id": "s-1", "type": "tool_call", "server_name": "github", "tool_name": "list_issues",
		"status": "success", "work_session_id": "ws-1", "request_id": "sub-1",
		"parent_id": "parent-1", "timestamp": "2026-08-01T10:00:01Z",
		"request_bytes": 50, "response_bytes": 200_000,
		"response_truncated": true,
	})

	c := loadString(t, parentLine+"\n"+subLine+"\n", testOptions(t.TempDir()))
	got := CodeExecSavingFor(firstCall(t, c))

	require.Equal(t, CostUnavailable, got.Basis)
	assert.Equal(t, ReasonTruncatedSubCallOverstates, got.Reason)
}
