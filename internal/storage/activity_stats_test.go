package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageSnapshot_SaveLoadRoundTrip(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// Absent snapshot -> (nil, nil), not an error.
	data, err := m.LoadUsageSnapshot()
	require.NoError(t, err)
	assert.Nil(t, data)

	payload := []byte(`{"tools":{"github-search":{"calls":42}}}`)
	require.NoError(t, m.SaveUsageSnapshot(payload))

	got, err := m.LoadUsageSnapshot()
	require.NoError(t, err)
	assert.Equal(t, payload, got)

	// Overwrite replaces the versioned key.
	payload2 := []byte(`{"tools":{}}`)
	require.NoError(t, m.SaveUsageSnapshot(payload2))
	got2, err := m.LoadUsageSnapshot()
	require.NoError(t, err)
	assert.Equal(t, payload2, got2)
}

func TestScanAllActivities_VisitsEveryRecordOncePerFullScan(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		rec := &ActivityRecord{
			Type:          ActivityTypeToolCall,
			ServerName:    "github",
			ToolName:      "search",
			Status:        "success",
			ResponseBytes: 100 + i,
			Timestamp:     base.Add(time.Duration(i) * time.Minute),
		}
		require.NoError(t, m.SaveActivity(rec))
	}
	// A non-tool-call record is still visited by the generic scan; the caller
	// (the aggregate) is responsible for filtering by type.
	require.NoError(t, m.SaveActivity(&ActivityRecord{
		Type:      ActivityTypeSystemStart,
		Status:    "success",
		Timestamp: base,
	}))

	visited := 0
	toolCalls := 0
	err := m.ScanAllActivities(func(rec *ActivityRecord) {
		visited++
		if rec.Type == ActivityTypeToolCall {
			toolCalls++
		}
	})
	require.NoError(t, err)
	assert.Equal(t, 6, visited, "every record visited exactly once")
	assert.Equal(t, 5, toolCalls)
}

func TestScanAllActivities_EmptyBucketIsNotAnError(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	visited := 0
	err := m.ScanAllActivities(func(*ActivityRecord) { visited++ })
	require.NoError(t, err)
	assert.Equal(t, 0, visited)
}
