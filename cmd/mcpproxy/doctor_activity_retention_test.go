package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 124 FR-035: doctor prints the shortfall with the setting to raise, and
// nothing when there is none or the daemon did not report the section.
func TestActivityRetentionLines(t *testing.T) {
	oldest := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC).Format(time.RFC3339)
	diag := map[string]interface{}{
		"activity_retention": map[string]interface{}{
			"window_days": 7, "shortfall": true, "cause": "size_cap",
			"oldest_complete_session_at": oldest, "raise": []interface{}{"activity_max_size_mb"},
		},
	}
	rep, ok := activityRetentionReport(diag)
	require.True(t, ok)
	require.True(t, rep.Shortfall)
	out := strings.Join(activityRetentionLines(rep), "\n")
	assert.Contains(t, out, "7 days")
	assert.Contains(t, out, "the size cap was reached")
	assert.Contains(t, out, "activity_max_size_mb")
	assert.Contains(t, out, "Oldest complete session started at")

	_, ok = activityRetentionReport(map[string]interface{}{})
	assert.False(t, ok, "an older daemon or a scoped caller reports no section")

	clear, ok := activityRetentionReport(map[string]interface{}{"activity_retention": map[string]interface{}{"window_days": 7, "shortfall": false}})
	require.True(t, ok)
	assert.False(t, clear.Shortfall, "nothing is printed without a shortfall")
}
