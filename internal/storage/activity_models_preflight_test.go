package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The preflight record kind must be filterable like every other kind: activity
// filtering compares the Type field against the ValidActivityTypes allowlist the
// CLI and REST surfaces validate against, so a type that is not in the list is
// storable but not queryable (spec 098 FR-014 / US3).
func TestPreflightActivityTypeIsInAllowlist(t *testing.T) {
	assert.Contains(t, ValidActivityTypes, string(ActivityTypePreflight))
	assert.Equal(t, ActivityType("preflight"), ActivityTypePreflight)
}

func TestActivityFilterMatchesPreflightType(t *testing.T) {
	record := &ActivityRecord{
		Type:      ActivityTypePreflight,
		Status:    ActivityStatusBlocked,
		RequestID: "req-098",
		Timestamp: time.Now(),
	}

	filter := DefaultActivityFilter()
	filter.Types = []string{string(ActivityTypePreflight)}
	assert.True(t, filter.Matches(record))

	// The request id is the documented correlation handle
	// (`activity list --request-id <id>`), so it must filter as a first-class
	// field rather than out of Metadata.
	byRequest := DefaultActivityFilter()
	byRequest.RequestID = "req-098"
	assert.True(t, byRequest.Matches(record))

	byOtherRequest := DefaultActivityFilter()
	byOtherRequest.RequestID = "req-other"
	assert.False(t, byOtherRequest.Matches(record))

	byOtherType := DefaultActivityFilter()
	byOtherType.Types = []string{string(ActivityTypeToolCall)}
	assert.False(t, byOtherType.Matches(record))
}

// The metadata payload is JSON round-tripped through BBolt, so the documented
// shape has to survive marshal/unmarshal with its nested map and slice intact.
func TestPreflightMetadataRoundTrip(t *testing.T) {
	original := &ActivityRecord{
		ID:        "01J000000000000000000000",
		Type:      ActivityTypePreflight,
		Source:    ActivitySourceCLI,
		Status:    ActivityStatusBlocked,
		RequestID: "req-098",
		Timestamp: time.Now().UTC().Truncate(time.Second),
		Metadata: map[string]interface{}{
			MetadataKeyPreflightVerdict:  "blocked",
			MetadataKeyPreflightIDsCount: 2,
			MetadataKeyPreflightReasons:  map[string]interface{}{"server_quarantined": 1},
			MetadataKeyPreflightPerTool: []interface{}{
				map[string]interface{}{
					PreflightPerToolKeyID:     "gh:create_issue",
					PreflightPerToolKeyStatus: "ready",
				},
				map[string]interface{}{
					PreflightPerToolKeyID:     "slack:post",
					PreflightPerToolKeyStatus: "unavailable",
					PreflightPerToolKeyReason: "server_quarantined",
				},
			},
		},
	}

	blob, err := original.MarshalBinary()
	require.NoError(t, err)

	var decoded ActivityRecord
	require.NoError(t, decoded.UnmarshalBinary(blob))

	assert.Equal(t, ActivityTypePreflight, decoded.Type)
	assert.Equal(t, "req-098", decoded.RequestID)
	assert.Equal(t, "blocked", decoded.Metadata[MetadataKeyPreflightVerdict])
	assert.InDelta(t, 2, decoded.Metadata[MetadataKeyPreflightIDsCount], 0.0001)

	reasons, ok := decoded.Metadata[MetadataKeyPreflightReasons].(map[string]interface{})
	require.True(t, ok)
	assert.InDelta(t, 1, reasons["server_quarantined"], 0.0001)

	perTool, ok := decoded.Metadata[MetadataKeyPreflightPerTool].([]interface{})
	require.True(t, ok)
	require.Len(t, perTool, 2)
	second, ok := perTool[1].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "slack:post", second[PreflightPerToolKeyID])
	assert.Equal(t, "unavailable", second[PreflightPerToolKeyStatus])
	assert.Equal(t, "server_quarantined", second[PreflightPerToolKeyReason])

	// A ready entry omits the reason key entirely.
	first, ok := perTool[0].(map[string]interface{})
	require.True(t, ok)
	_, hasReason := first[PreflightPerToolKeyReason]
	assert.False(t, hasReason)
}
