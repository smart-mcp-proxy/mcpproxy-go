package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `mcpproxy activity show --include-response` prints the stored body. When that
// body was cut, the reader has to be told WHICH cut happened, because the two
// point in opposite directions:
//
//	response_truncated          the agent received LESS than this record holds
//	response_storage_truncated  the agent received MORE than this record holds
//
// Before this, the CLI printed "(response was truncated)" for the first flag
// only. A storage-truncated body therefore ended in `...[truncated]` with no
// explanation at all — the one seam the ResponseStorageTruncated split missed,
// while both REST converters, the CSV export and the Vue drawer carried it.

func TestStorageTruncatedResponseIsExplained(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"response_storage_truncated": true,
		"response_bytes":             float64(200039), // JSON numbers decode as float64
	})

	require.Len(t, notices, 1, "only the storage cut applies to this record")
	assert.Contains(t, notices[0], "activity_max_response_size",
		"the notice must name the setting an operator can change")
	assert.Contains(t, notices[0], "MORE",
		"a storage cut means the agent saw MORE than the log holds — the opposite of the forward cut")
	assert.Contains(t, notices[0], "200039",
		"response_bytes is measured pre-truncation and is how much was actually there")
}

// The forward cut keeps its own wording, and must not be relabelled by the fix
// above: it is the direction where the log holds MORE than the agent consumed.
func TestForwardTruncatedResponseKeepsItsOwnExplanation(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"response_truncated": true,
	})

	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "tool_response_limit")
	assert.Contains(t, notices[0], "LESS")
	assert.NotContains(t, notices[0], "activity_max_response_size")
}

// Both cuts can land on one record — a direct call_tool_* dispatch records the
// full upstream text (forward cut) which is then cut again on the way into
// BBolt (storage cut). Neither may hide the other.
func TestBothTruncationsAreReportedSeparately(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"response_truncated":         true,
		"response_storage_truncated": true,
		"response_bytes":             float64(200039),
	})

	require.Len(t, notices, 2, "two independent facts, two lines")
	joined := strings.Join(notices, "\n")
	assert.Contains(t, joined, "tool_response_limit")
	assert.Contains(t, joined, "activity_max_response_size")
}

// A record with neither flag says nothing: the body printed above it is whole.
func TestUntruncatedResponseGetsNoNotice(t *testing.T) {
	assert.Empty(t, activityTruncationNotices(map[string]interface{}{
		"response_bytes": float64(120),
	}))
}

// A legacy record predating the byte counts still gets the explanation; it
// simply cannot quantify it. Printing "0 bytes" would read as an empty
// response, which is the opposite of what happened.
func TestStorageTruncationWithoutByteCountStillExplains(t *testing.T) {
	notices := activityTruncationNotices(map[string]interface{}{
		"response_storage_truncated": true,
	})

	require.Len(t, notices, 1)
	assert.Contains(t, notices[0], "activity_max_response_size")
	assert.NotContains(t, notices[0], "0 bytes")
}
