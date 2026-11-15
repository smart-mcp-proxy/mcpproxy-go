package management

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 124 FR-035: the doctor section.
func TestBuildActivityRetentionReport(t *testing.T) {
	window := 7 * 24 * time.Hour

	clear := BuildActivityRetentionReport(nil, window)
	assert.Equal(t, 7, clear.WindowDays)
	assert.False(t, clear.Shortfall)
	assert.Empty(t, clear.Cause)
	assert.Empty(t, clear.Raise)

	observed := time.Date(2026, 11, 15, 3, 0, 0, 0, time.UTC)
	oldest := observed.Add(-36 * time.Hour)
	cases := map[string]string{
		storage.ShortfallAgeCap:    "activity_retention_days",
		storage.ShortfallRecordCap: "activity_max_records",
		storage.ShortfallSizeCap:   "activity_max_size_mb",
	}
	for cause, setting := range cases {
		rep := BuildActivityRetentionReport(&storage.RetentionShortfall{
			ObservedAt: observed, Cause: cause, LastInWindowRemovalAt: observed, OldestCompleteSessionAt: oldest,
		}, window)
		require.True(t, rep.Shortfall, cause)
		assert.Equal(t, cause, rep.Cause)
		assert.Equal(t, []string{setting}, rep.Raise, "the setting to raise is named for the cause")
		require.NotNil(t, rep.ObservedAt)
		assert.Equal(t, observed, *rep.ObservedAt)
		require.NotNil(t, rep.OldestCompleteSessionAt)
		assert.Equal(t, oldest, *rep.OldestCompleteSessionAt)

		line := activityRetentionWarningLine(rep)
		assert.Contains(t, line, "7 days")
		assert.Contains(t, line, setting)
	}

	// No complete session at all: the field is left out, not set to zero time.
	rep := BuildActivityRetentionReport(&storage.RetentionShortfall{ObservedAt: observed, Cause: storage.ShortfallSizeCap}, window)
	assert.Nil(t, rep.OldestCompleteSessionAt)
}
