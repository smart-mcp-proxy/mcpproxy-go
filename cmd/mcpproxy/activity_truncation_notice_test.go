package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `mcpproxy activity show --include-response` prints the stored body. When that
// body was cut, the reader has to be told WHICH cut happened — and for the
// forward cut, which DIRECTION it points, because that depends on the record
// TYPE and not on the flag:
//
//	tool_call + response_truncated           the stored body IS the agent's copy
//	                                         (handleToolCallCompleted stores the
//	                                         post-forward text)
//	internal_tool_call + response_truncated  the stored body is LARGER than the
//	                                         agent's copy (the built-in records
//	                                         its full pre-truncation response)
//	response_storage_truncated               the stored body is SMALLER than the
//	                                         agent's copy
//
// A universal "the agent received LESS than this" told the operator the exact
// opposite of what happened for every truncated upstream call, which is the
// dominant population carrying the flag. These tests vary the TYPE, not just
// the flags, so that wording cannot come back.

func TestForwardTruncatedInternalCallSaysTheLogHoldsMore(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":               "internal_tool_call",
		"response_truncated": true,
	})

	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "LESS",
		"a built-in records its full response while the agent gets the cut copy")
	assert.Contains(t, notices[0], "tool_response_limit")
	assert.NotContains(t, notices[0], "activity_max_response_size")
}

func TestForwardTruncatedToolCallDoesNotClaimTheAgentSawLess(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":               "tool_call",
		"response_truncated": true,
		"response_bytes":     float64(200039), // JSON numbers decode as float64
	})

	require.Len(t, notices, 1)
	// The record holds the POST-forward text, so the agent received exactly
	// this — claiming otherwise inverts what happened.
	assert.NotContains(t, notices[0], "LESS",
		"the tool_call record stores the forwarded copy, so the agent received exactly this")
	assert.Contains(t, notices[0], "agent's copy")
	assert.Contains(t, notices[0], "tool_response_limit")
	// response_bytes is pre-truncation, so it describes the UPSTREAM payload,
	// not what was delivered.
	assert.Contains(t, notices[0], "200039")
	assert.Contains(t, notices[0], "upstream")
}

// The two types must not render the same sentence: that identity is exactly the
// universal-claim bug.
func TestForwardTruncationWordingDiffersByRecordType(t *testing.T) {
	internalNotice := activityTruncationNotices(map[string]interface{}{
		"type":               "internal_tool_call",
		"response_truncated": true,
	})
	toolNotice := activityTruncationNotices(map[string]interface{}{
		"type":               "tool_call",
		"response_truncated": true,
	})

	require.Len(t, internalNotice, 1)
	require.Len(t, toolNotice, 1)
	assert.NotEqual(t, internalNotice[0], toolNotice[0])
}

// No other type sets response_truncated today. If one ever does, it gets the
// direction-free statement of fact rather than a borrowed direction.
func TestForwardTruncationOnAnUnknownTypeClaimsNoDirection(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":               "prompt_get",
		"response_truncated": true,
	})

	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "tool_response_limit")
	assert.NotContains(t, notices[0], "LESS")
	assert.NotContains(t, notices[0], "MORE")
}

func TestStorageTruncatedResponseIsExplained(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":                       "tool_call",
		"response_storage_truncated": true,
		"response_bytes":             float64(200039),
	})

	require.Len(t, notices, 1, "only the storage cut applies to this record")
	assert.Contains(t, notices[0], "activity_max_response_size",
		"the notice must name the setting an operator can change")
	assert.Contains(t, notices[0], "MORE",
		"nothing cut the response on the way out, so the agent saw the whole 200039 bytes")
	assert.Contains(t, notices[0], "200039")
}

// The storage cut is direction-stable across types when it is the ONLY cut:
// response_bytes is what was delivered in both cases.
func TestStorageTruncationReadsTheSameForBothTypes(t *testing.T) {
	toolNotices := activityTruncationNotices(map[string]interface{}{
		"type":                       "tool_call",
		"response_storage_truncated": true,
		"response_bytes":             float64(4096),
	})
	internalNotices := activityTruncationNotices(map[string]interface{}{
		"type":                       "internal_tool_call",
		"response_storage_truncated": true,
		"response_bytes":             float64(4096),
	})

	require.Len(t, toolNotices, 1)
	require.Len(t, internalNotices, 1)
	assert.Equal(t, toolNotices[0], internalNotices[0])
}

// Both cuts can land on one record. Neither may hide the other, and the storage
// line must stop quoting response_bytes as "what the agent received": with a
// forward cut in play that number describes the pre-forward upstream payload,
// which is larger than the delivered copy on BOTH types.
func TestBothTruncationsAreReportedSeparately(t *testing.T) {
	for _, recordType := range []string{"tool_call", "internal_tool_call"} {
		notices := activityTruncationNotices(map[string]interface{}{
			"type":                       recordType,
			"response_truncated":         true,
			"response_storage_truncated": true,
			"response_bytes":             float64(200039),
		})

		require.Len(t, notices, 2, "two independent facts, two lines (%s)", recordType)
		assert.Contains(t, notices[0], "tool_response_limit", recordType)
		assert.Contains(t, notices[1], "activity_max_response_size", recordType)
		assert.NotContains(t, notices[1], "200039",
			"with a forward cut in play, response_bytes is neither the stored nor the delivered size (%s)", recordType)
		joined := strings.Join(notices, "\n")
		assert.Contains(t, joined, "tool_response_limit")
	}
}

// A record with neither flag says nothing: the body printed above it is whole.
func TestUntruncatedResponseGetsNoNotice(t *testing.T) {
	assert.Empty(t, activityTruncationNotices(map[string]interface{}{
		"type":           "tool_call",
		"response_bytes": float64(120),
	}))
}

// A legacy record predating the byte counts still gets the explanation; it
// simply cannot quantify it. Printing "0 bytes" would read as an empty
// response, which is the opposite of what happened.
func TestStorageTruncationWithoutByteCountStillExplains(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":                       "tool_call",
		"response_storage_truncated": true,
	})

	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "activity_max_response_size")
	assert.NotContains(t, notices[0], "0 bytes")
}

func TestForwardTruncationWithoutByteCountStillExplains(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"type":               "tool_call",
		"response_truncated": true,
	})

	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "tool_response_limit")
	assert.NotContains(t, notices[0], "0 bytes")
}
