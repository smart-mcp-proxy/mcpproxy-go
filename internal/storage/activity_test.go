package storage

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func setupTestStorageForActivity(t *testing.T) (*Manager, func()) {
	t.Helper()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "activity_test_*")
	require.NoError(t, err)

	// Create logger
	logger := zap.NewNop().Sugar()

	// Create manager
	manager, err := NewManager(tmpDir, logger)
	require.NoError(t, err)

	cleanup := func() {
		manager.Close()
		os.RemoveAll(tmpDir)
	}

	return manager, cleanup
}

func TestActivityRecord_MarshalUnmarshal(t *testing.T) {
	record := &ActivityRecord{
		ID:         "01HQWX1Y2Z3A4B5C6D7E8F9G0H",
		Type:       ActivityTypeToolCall,
		ServerName: "test-server",
		ToolName:   "test-tool",
		Arguments: map[string]interface{}{
			"arg1": "value1",
		},
		Response:          "test response",
		ResponseTruncated: false,
		Status:            "success",
		DurationMs:        100,
		Timestamp:         time.Now().UTC(),
		SessionID:         "session-123",
		RequestID:         "req-456",
		Metadata: map[string]interface{}{
			"key": "value",
		},
	}

	// Marshal
	data, err := record.MarshalBinary()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Unmarshal
	var result ActivityRecord
	err = result.UnmarshalBinary(data)
	require.NoError(t, err)

	assert.Equal(t, record.ID, result.ID)
	assert.Equal(t, record.Type, result.Type)
	assert.Equal(t, record.ServerName, result.ServerName)
	assert.Equal(t, record.ToolName, result.ToolName)
	assert.Equal(t, record.Status, result.Status)
	assert.Equal(t, record.Response, result.Response)
}

func TestActivityFilter_Validate(t *testing.T) {
	tests := []struct {
		name     string
		filter   ActivityFilter
		wantLimit  int
		wantOffset int
	}{
		{
			name:     "default values",
			filter:   ActivityFilter{},
			wantLimit:  50,
			wantOffset: 0,
		},
		{
			name:     "negative limit becomes default",
			filter:   ActivityFilter{Limit: -5},
			wantLimit:  50,
			wantOffset: 0,
		},
		{
			name:     "limit over 100 capped",
			filter:   ActivityFilter{Limit: 200},
			wantLimit:  100,
			wantOffset: 0,
		},
		{
			name:     "negative offset becomes 0",
			filter:   ActivityFilter{Limit: 50, Offset: -10},
			wantLimit:  50,
			wantOffset: 0,
		},
		{
			name:     "valid values unchanged",
			filter:   ActivityFilter{Limit: 25, Offset: 10},
			wantLimit:  25,
			wantOffset: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.filter.Validate()
			assert.Equal(t, tt.wantLimit, tt.filter.Limit)
			assert.Equal(t, tt.wantOffset, tt.filter.Offset)
		})
	}
}

func TestActivityFilter_Matches(t *testing.T) {
	record := &ActivityRecord{
		Type:       ActivityTypeToolCall,
		ServerName: "github",
		ToolName:   "create_issue",
		SessionID:  "sess-123",
		Status:     "success",
		Timestamp:  time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name    string
		filter  ActivityFilter
		matches bool
	}{
		{
			name:    "empty filter matches all",
			filter:  ActivityFilter{},
			matches: true,
		},
		{
			name:    "single type matches",
			filter:  ActivityFilter{Types: []string{"tool_call"}},
			matches: true,
		},
		{
			name:    "single type does not match",
			filter:  ActivityFilter{Types: []string{"policy_decision"}},
			matches: false,
		},
		{
			name:    "multiple types OR logic - first matches",
			filter:  ActivityFilter{Types: []string{"tool_call", "policy_decision"}},
			matches: true,
		},
		{
			name:    "multiple types OR logic - second matches",
			filter:  ActivityFilter{Types: []string{"policy_decision", "tool_call"}},
			matches: true,
		},
		{
			name:    "multiple types OR logic - none match",
			filter:  ActivityFilter{Types: []string{"policy_decision", "quarantine_change"}},
			matches: false,
		},
		{
			name:    "server matches",
			filter:  ActivityFilter{Server: "github"},
			matches: true,
		},
		{
			name:    "server does not match",
			filter:  ActivityFilter{Server: "gitlab"},
			matches: false,
		},
		{
			name:    "tool matches",
			filter:  ActivityFilter{Tool: "create_issue"},
			matches: true,
		},
		{
			name:    "session matches",
			filter:  ActivityFilter{SessionID: "sess-123"},
			matches: true,
		},
		{
			name:    "status matches",
			filter:  ActivityFilter{Status: "success"},
			matches: true,
		},
		{
			name:    "status does not match",
			filter:  ActivityFilter{Status: "error"},
			matches: false,
		},
		{
			name:    "time range matches",
			filter:  ActivityFilter{
				StartTime: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
				EndTime:   time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC),
			},
			matches: true,
		},
		{
			name:    "before start time",
			filter:  ActivityFilter{
				StartTime: time.Date(2024, 6, 20, 0, 0, 0, 0, time.UTC),
			},
			matches: false,
		},
		{
			name:    "after end time",
			filter:  ActivityFilter{
				EndTime: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			},
			matches: false,
		},
		{
			name:    "multiple filters all match",
			filter:  ActivityFilter{
				Types:  []string{"tool_call"},
				Server: "github",
				Status: "success",
			},
			matches: true,
		},
		{
			name:    "multiple filters one fails",
			filter:  ActivityFilter{
				Types:  []string{"tool_call"},
				Server: "gitlab", // does not match
				Status: "success",
			},
			matches: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.filter.Matches(record)
			assert.Equal(t, tt.matches, result)
		})
	}
}

func TestTruncateResponse(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		maxSize   int
		want      string
		truncated bool
	}{
		{
			name:      "short response unchanged",
			response:  "hello",
			maxSize:   100,
			want:      "hello",
			truncated: false,
		},
		{
			name:      "exact size unchanged",
			response:  "hello",
			maxSize:   5,
			want:      "hello",
			truncated: false,
		},
		{
			name:      "long response truncated",
			response:  "this is a very long response that exceeds the limit",
			maxSize:   10,
			want:      "this is a ...[truncated]",
			truncated: true,
		},
		{
			name:      "default max size when zero",
			response:  "short",
			maxSize:   0,
			want:      "short",
			truncated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, wasTruncated := truncateResponse(tt.response, tt.maxSize)
			assert.Equal(t, tt.truncated, wasTruncated)
			if tt.truncated {
				assert.Contains(t, result, "...[truncated]")
				assert.LessOrEqual(t, len(result)-len("...[truncated]"), tt.maxSize)
			} else {
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestSaveAndGetActivity(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// Create and save activity
	record := &ActivityRecord{
		Type:       ActivityTypeToolCall,
		ServerName: "test-server",
		ToolName:   "test-tool",
		Status:     "success",
		Response:   "test response",
		DurationMs: 150,
	}

	err := manager.SaveActivity(record)
	require.NoError(t, err)
	assert.NotEmpty(t, record.ID, "ID should be generated")
	assert.False(t, record.Timestamp.IsZero(), "Timestamp should be set")

	// Retrieve by ID
	retrieved, err := manager.GetActivity(record.ID)
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.Equal(t, record.ID, retrieved.ID)
	assert.Equal(t, record.Type, retrieved.Type)
	assert.Equal(t, record.ServerName, retrieved.ServerName)
	assert.Equal(t, record.ToolName, retrieved.ToolName)
	assert.Equal(t, record.Status, retrieved.Status)
	assert.Equal(t, record.Response, retrieved.Response)
}

func TestSaveActivity_WithID(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	record := &ActivityRecord{
		ID:         "custom-id-123",
		Type:       ActivityTypeToolCall,
		ServerName: "test",
		Status:     "success",
		Timestamp:  time.Now().UTC(),
	}

	err := manager.SaveActivity(record)
	require.NoError(t, err)
	assert.Equal(t, "custom-id-123", record.ID, "Custom ID should be preserved")

	// Verify retrieval
	retrieved, err := manager.GetActivity("custom-id-123")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, "custom-id-123", retrieved.ID)
}

func TestGetActivity_NotFound(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	retrieved, err := manager.GetActivity("nonexistent-id")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestListActivities_Pagination(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// Create 10 activities
	for i := 0; i < 10; i++ {
		record := &ActivityRecord{
			Type:       ActivityTypeToolCall,
			ServerName: "server",
			Status:     "success",
			Timestamp:  time.Now().UTC().Add(time.Duration(i) * time.Second),
		}
		err := manager.SaveActivity(record)
		require.NoError(t, err)
	}

	// Get first page
	filter := ActivityFilter{Limit: 3, Offset: 0}
	records, total, err := manager.ListActivities(filter)
	require.NoError(t, err)
	assert.Equal(t, 10, total)
	assert.Len(t, records, 3)

	// Get second page
	filter.Offset = 3
	records, total, err = manager.ListActivities(filter)
	require.NoError(t, err)
	assert.Equal(t, 10, total)
	assert.Len(t, records, 3)

	// Get last page
	filter.Offset = 9
	records, total, err = manager.ListActivities(filter)
	require.NoError(t, err)
	assert.Equal(t, 10, total)
	assert.Len(t, records, 1)
}

func TestListActivities_Filtering(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// Create diverse activities
	activities := []*ActivityRecord{
		{Type: ActivityTypeToolCall, ServerName: "github", Status: "success"},
		{Type: ActivityTypeToolCall, ServerName: "github", Status: "error"},
		{Type: ActivityTypeToolCall, ServerName: "gitlab", Status: "success"},
		{Type: ActivityTypePolicyDecision, ServerName: "github", Status: "blocked"},
	}

	for _, a := range activities {
		a.Timestamp = time.Now().UTC()
		err := manager.SaveActivity(a)
		require.NoError(t, err)
	}

	// Filter by single type
	filter := ActivityFilter{Types: []string{"tool_call"}, Limit: 50}
	records, total, err := manager.ListActivities(filter)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, records, 3)

	// Filter by server
	filter = ActivityFilter{Server: "github", Limit: 50}
	_, total, err = manager.ListActivities(filter)
	require.NoError(t, err)
	assert.Equal(t, 3, total)

	// Filter by status
	filter = ActivityFilter{Status: "success", Limit: 50}
	_, total, err = manager.ListActivities(filter)
	require.NoError(t, err)
	assert.Equal(t, 2, total)

	// Combined filters with single type
	filter = ActivityFilter{Types: []string{"tool_call"}, Server: "github", Limit: 50}
	_, total, err = manager.ListActivities(filter)
	require.NoError(t, err)
	assert.Equal(t, 2, total)

	// Multi-type filter (Spec 024)
	filter = ActivityFilter{Types: []string{"tool_call", "policy_decision"}, Limit: 50}
	_, total, err = manager.ListActivities(filter)
	require.NoError(t, err)
	assert.Equal(t, 4, total) // 3 tool_call + 1 policy_decision
}

func TestDeleteActivity(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// Create activity
	record := &ActivityRecord{
		Type:   ActivityTypeToolCall,
		Status: "success",
	}
	err := manager.SaveActivity(record)
	require.NoError(t, err)

	// Verify exists
	retrieved, err := manager.GetActivity(record.ID)
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	// Delete
	err = manager.DeleteActivity(record.ID)
	require.NoError(t, err)

	// Verify deleted
	retrieved, err = manager.GetActivity(record.ID)
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestDeleteActivity_NotFound(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// Delete non-existent - should not error
	err := manager.DeleteActivity("nonexistent")
	require.NoError(t, err)
}

func TestCountActivities(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// Initially zero
	count, err := manager.CountActivities()
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Add some
	for i := 0; i < 5; i++ {
		err := manager.SaveActivity(&ActivityRecord{
			Type:   ActivityTypeToolCall,
			Status: "success",
		})
		require.NoError(t, err)
	}

	count, err = manager.CountActivities()
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

func TestPruneOldActivities(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// Create activities with different timestamps
	now := time.Now().UTC()

	// Old activity (2 hours ago)
	oldRecord := &ActivityRecord{
		Type:      ActivityTypeToolCall,
		Status:    "success",
		Timestamp: now.Add(-2 * time.Hour),
	}
	err := manager.SaveActivity(oldRecord)
	require.NoError(t, err)

	// Recent activity (30 min ago)
	recentRecord := &ActivityRecord{
		Type:      ActivityTypeToolCall,
		Status:    "success",
		Timestamp: now.Add(-30 * time.Minute),
	}
	err = manager.SaveActivity(recentRecord)
	require.NoError(t, err)

	// Verify both exist
	count, err := manager.CountActivities()
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Prune older than 1 hour
	deleted, err := manager.PruneOldActivities(1 * time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	// Verify only recent remains
	count, err = manager.CountActivities()
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify the recent one still exists
	retrieved, err := manager.GetActivity(recentRecord.ID)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
}

func TestPruneExcessActivities(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// Create 10 activities
	for i := 0; i < 10; i++ {
		err := manager.SaveActivity(&ActivityRecord{
			Type:      ActivityTypeToolCall,
			Status:    "success",
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
		})
		require.NoError(t, err)
	}

	// Verify count
	count, err := manager.CountActivities()
	require.NoError(t, err)
	assert.Equal(t, 10, count)

	// Prune to max 5 (90% = 4.5 -> 4)
	deleted, err := manager.PruneExcessActivities(5, 0.9)
	require.NoError(t, err)
	assert.Equal(t, 6, deleted) // 10 - 4 = 6

	// Verify remaining
	count, err = manager.CountActivities()
	require.NoError(t, err)
	assert.Equal(t, 4, count)
}

func TestListActivities_Order(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// Create activities with known order
	timestamps := []time.Time{
		time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 2, 12, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 3, 12, 0, 0, 0, time.UTC),
	}

	for i, ts := range timestamps {
		err := manager.SaveActivity(&ActivityRecord{
			Type:      ActivityTypeToolCall,
			Status:    "success",
			Timestamp: ts,
			Metadata:  map[string]interface{}{"order": i + 1},
		})
		require.NoError(t, err)
	}

	// List should return newest first
	records, _, err := manager.ListActivities(ActivityFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, records, 3)

	// Verify order (newest first)
	assert.True(t, records[0].Timestamp.After(records[1].Timestamp))
	assert.True(t, records[1].Timestamp.After(records[2].Timestamp))
}

// =============================================================================
// Spec 026: Sensitive Data Detection Filter Tests
// =============================================================================

func TestActivityFilter_Matches_SensitiveData(t *testing.T) {
	recordWithDetection := &ActivityRecord{
		Type:       ActivityTypeToolCall,
		ServerName: "github",
		ToolName:   "create_secret",
		Status:     "success",
		Timestamp:  time.Now().UTC(),
		Metadata: map[string]interface{}{
			"sensitive_data_detection": map[string]interface{}{
				"detected": true,
				"detections": []interface{}{
					map[string]interface{}{
						"type":     "aws_access_key",
						"severity": "critical",
						"location": "arguments.key",
					},
					map[string]interface{}{
						"type":     "credit_card",
						"severity": "medium",
						"location": "arguments.card",
					},
				},
			},
		},
	}

	recordWithoutDetection := &ActivityRecord{
		Type:       ActivityTypeToolCall,
		ServerName: "github",
		ToolName:   "get_repo",
		Status:     "success",
		Timestamp:  time.Now().UTC(),
		Metadata: map[string]interface{}{
			"sensitive_data_detection": map[string]interface{}{
				"detected":   false,
				"detections": []interface{}{},
			},
		},
	}

	recordNoMetadata := &ActivityRecord{
		Type:       ActivityTypeToolCall,
		ServerName: "github",
		ToolName:   "list_repos",
		Status:     "success",
		Timestamp:  time.Now().UTC(),
		Metadata:   nil,
	}

	t.Run("sensitive_data=true matches record with detections", func(t *testing.T) {
		sensitiveTrue := true
		filter := ActivityFilter{SensitiveData: &sensitiveTrue}
		assert.True(t, filter.Matches(recordWithDetection))
	})

	t.Run("sensitive_data=true does not match record without detections", func(t *testing.T) {
		sensitiveTrue := true
		filter := ActivityFilter{SensitiveData: &sensitiveTrue}
		assert.False(t, filter.Matches(recordWithoutDetection))
	})

	t.Run("sensitive_data=true does not match record with nil metadata", func(t *testing.T) {
		sensitiveTrue := true
		filter := ActivityFilter{SensitiveData: &sensitiveTrue}
		assert.False(t, filter.Matches(recordNoMetadata))
	})

	t.Run("sensitive_data=false matches record without detections", func(t *testing.T) {
		sensitiveFalse := false
		filter := ActivityFilter{SensitiveData: &sensitiveFalse}
		assert.True(t, filter.Matches(recordWithoutDetection))
	})

	t.Run("sensitive_data=false does not match record with detections", func(t *testing.T) {
		sensitiveFalse := false
		filter := ActivityFilter{SensitiveData: &sensitiveFalse}
		assert.False(t, filter.Matches(recordWithDetection))
	})

	t.Run("sensitive_data=nil matches all records", func(t *testing.T) {
		filter := ActivityFilter{SensitiveData: nil}
		assert.True(t, filter.Matches(recordWithDetection))
		assert.True(t, filter.Matches(recordWithoutDetection))
		assert.True(t, filter.Matches(recordNoMetadata))
	})

	t.Run("detection_type filter matches specific type", func(t *testing.T) {
		filter := ActivityFilter{DetectionType: "aws_access_key"}
		assert.True(t, filter.Matches(recordWithDetection))
	})

	t.Run("detection_type filter does not match different type", func(t *testing.T) {
		filter := ActivityFilter{DetectionType: "github_token"}
		assert.False(t, filter.Matches(recordWithDetection))
	})

	t.Run("severity filter matches highest severity", func(t *testing.T) {
		filter := ActivityFilter{Severity: "critical"}
		assert.True(t, filter.Matches(recordWithDetection))
	})

	t.Run("severity filter does not match when max is different", func(t *testing.T) {
		filter := ActivityFilter{Severity: "high"}
		assert.False(t, filter.Matches(recordWithDetection))
	})

	t.Run("combined sensitive data filters", func(t *testing.T) {
		sensitiveTrue := true
		filter := ActivityFilter{
			SensitiveData: &sensitiveTrue,
			DetectionType: "aws_access_key",
			Severity:      "critical",
		}
		assert.True(t, filter.Matches(recordWithDetection))

		// Change severity to not match
		filter.Severity = "high"
		assert.False(t, filter.Matches(recordWithDetection))
	})
}

func TestExtractSensitiveDataInfo_Storage(t *testing.T) {
	t.Run("extracts info from record with detections", func(t *testing.T) {
		record := &ActivityRecord{
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected": true,
					"detections": []interface{}{
						map[string]interface{}{"type": "stripe_key", "severity": "high"},
						map[string]interface{}{"type": "aws_secret_key", "severity": "critical"},
					},
				},
			},
		}

		detected, types, maxSeverity := extractSensitiveDataInfo(record)

		assert.True(t, detected)
		assert.Len(t, types, 2)
		assert.Contains(t, types, "stripe_key")
		assert.Contains(t, types, "aws_secret_key")
		assert.Equal(t, "critical", maxSeverity)
	})

	t.Run("returns empty for nil metadata", func(t *testing.T) {
		record := &ActivityRecord{Metadata: nil}
		detected, types, maxSeverity := extractSensitiveDataInfo(record)

		assert.False(t, detected)
		assert.Nil(t, types)
		assert.Empty(t, maxSeverity)
	})

	t.Run("returns empty for detected=false", func(t *testing.T) {
		record := &ActivityRecord{
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected":   false,
					"detections": []interface{}{},
				},
			},
		}

		detected, types, maxSeverity := extractSensitiveDataInfo(record)

		assert.False(t, detected)
		assert.Nil(t, types)
		assert.Empty(t, maxSeverity)
	})

	t.Run("deduplicates detection types", func(t *testing.T) {
		record := &ActivityRecord{
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected": true,
					"detections": []interface{}{
						map[string]interface{}{"type": "aws_access_key", "severity": "critical"},
						map[string]interface{}{"type": "aws_access_key", "severity": "critical"},
						map[string]interface{}{"type": "aws_access_key", "severity": "critical"},
					},
				},
			},
		}

		_, types, _ := extractSensitiveDataInfo(record)
		assert.Len(t, types, 1)
		assert.Equal(t, "aws_access_key", types[0])
	})
}

func TestCalculateMaxSeverity_Storage(t *testing.T) {
	tests := []struct {
		name       string
		severities []string
		expected   string
	}{
		{
			name:       "critical is highest",
			severities: []string{"low", "medium", "high", "critical"},
			expected:   "critical",
		},
		{
			name:       "high without critical",
			severities: []string{"low", "medium", "high"},
			expected:   "high",
		},
		{
			name:       "medium without higher",
			severities: []string{"low", "medium"},
			expected:   "medium",
		},
		{
			name:       "only low",
			severities: []string{"low"},
			expected:   "low",
		},
		{
			name:       "empty returns empty",
			severities: []string{},
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detections := make([]interface{}, len(tt.severities))
			for i, sev := range tt.severities {
				detections[i] = map[string]interface{}{
					"type":     "test",
					"severity": sev,
				}
			}

			detection := map[string]interface{}{
				"detections": detections,
			}

			result := calculateMaxSeverity(detection)
			assert.Equal(t, tt.expected, result)
		})
	}
}
