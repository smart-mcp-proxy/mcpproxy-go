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

// TestStorageTruncatedRecordIsEstimatedFromBytes covers the default posture.
// Bodies off means nothing is tokenized anyway, so a cut body costs the loader
// nothing at all and the pre-truncation byte length stands as the estimate.
func TestStorageTruncatedRecordIsEstimatedFromBytes(t *testing.T) {
	c := loadString(t, record(t, map[string]any{
		"type":                       "internal_tool_call",
		"tool_name":                  "call_tool_read",
		"response_storage_truncated": true,
		"response_bytes":             200_000,
	}), testOptions(t.TempDir()))

	call := firstCall(t, c)
	require.Equal(t, CostEstimated, call.ResponseCost.Basis,
		"a storage-truncated record's response_bytes is honest and must still be counted")
	assert.Equal(t, 200_000, call.ResponseCost.Bytes)
	assert.False(t, call.ResponseCost.Truncated,
		"Cost.Truncated is the OVERSTATEMENT annotation; it drives the code-exec withhold "+
			"and does not apply to a record whose byte count is exact")
	assert.False(t, call.Flags.Truncated,
		"the Spec 103 usability flag must not be raised by a storage cut")
	assert.Zero(t, c.Exclusions.Withheld[ReasonTruncatedRetrieveOverstates],
		"the log holds LESS than the agent received here, not more")
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
// Under the default bodies-off posture every component is estimated, the bases
// agree, and a storage cut changes nothing — the saving is still computed.
func TestCodeExecSavingSurvivesStorageTruncation(t *testing.T) {
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
		"response_storage_truncated": true,
	})

	c := loadString(t, parentLine+"\n"+subLine+"\n", testOptions(t.TempDir()))

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
}

// The Spec 103 direction must STILL withhold, so the test above cannot pass by
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
