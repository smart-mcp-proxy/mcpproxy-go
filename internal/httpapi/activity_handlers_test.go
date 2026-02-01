package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// =============================================================================
// Spec 026: Sensitive Data Filtering - Handler Integration Tests
// =============================================================================

// mockActivityController is a mock controller for activity handler tests
type mockActivityController struct {
	baseController
	apiKey     string
	activities []*storage.ActivityRecord
}

func (m *mockActivityController) GetCurrentConfig() any {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *mockActivityController) ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	var result []*storage.ActivityRecord
	for _, a := range m.activities {
		if filter.Matches(a) {
			result = append(result, a)
		}
	}

	// Apply pagination
	total := len(result)
	if filter.Offset > 0 && filter.Offset < len(result) {
		result = result[filter.Offset:]
	} else if filter.Offset >= len(result) {
		result = nil
	}

	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}

	return result, total, nil
}

func (m *mockActivityController) GetActivity(id string) (*storage.ActivityRecord, error) {
	for _, a := range m.activities {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, nil
}

// createTestActivityRecords creates a set of test activity records for testing
func createTestActivityRecords() []*storage.ActivityRecord {
	return []*storage.ActivityRecord{
		// Activity with AWS access key detection (critical severity)
		{
			ID:         "activity-1-aws-key",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "github",
			ToolName:   "create_secret",
			Status:     "success",
			Timestamp:  time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected": true,
					"detections": []interface{}{
						map[string]interface{}{
							"type":     "aws_access_key",
							"severity": "critical",
							"location": "arguments.secret_value",
						},
					},
				},
			},
		},
		// Activity with credit card detection (high severity)
		{
			ID:         "activity-2-credit-card",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "payments",
			ToolName:   "process_payment",
			Status:     "success",
			Timestamp:  time.Date(2024, 6, 15, 13, 0, 0, 0, time.UTC),
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected": true,
					"detections": []interface{}{
						map[string]interface{}{
							"type":     "credit_card",
							"severity": "high",
							"location": "arguments.card_number",
						},
					},
				},
			},
		},
		// Activity with multiple detection types (critical + high)
		{
			ID:         "activity-3-multiple",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "github",
			ToolName:   "store_credentials",
			Status:     "success",
			Timestamp:  time.Date(2024, 6, 15, 14, 0, 0, 0, time.UTC),
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected": true,
					"detections": []interface{}{
						map[string]interface{}{
							"type":     "aws_access_key",
							"severity": "critical",
							"location": "arguments.aws_key",
						},
						map[string]interface{}{
							"type":     "github_token",
							"severity": "high",
							"location": "arguments.gh_token",
						},
					},
				},
			},
		},
		// Activity with medium severity detection
		{
			ID:         "activity-4-medium",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "analytics",
			ToolName:   "send_email",
			Status:     "success",
			Timestamp:  time.Date(2024, 6, 15, 15, 0, 0, 0, time.UTC),
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected": true,
					"detections": []interface{}{
						map[string]interface{}{
							"type":     "email_address",
							"severity": "medium",
							"location": "arguments.email",
						},
					},
				},
			},
		},
		// Activity without sensitive data
		{
			ID:         "activity-5-clean",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "github",
			ToolName:   "get_repo",
			Status:     "success",
			Timestamp:  time.Date(2024, 6, 15, 16, 0, 0, 0, time.UTC),
			Metadata:   map[string]interface{}{"key": "value"},
		},
		// Activity with detected=false
		{
			ID:         "activity-6-not-detected",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "github",
			ToolName:   "list_repos",
			Status:     "success",
			Timestamp:  time.Date(2024, 6, 15, 17, 0, 0, 0, time.UTC),
			Metadata: map[string]interface{}{
				"sensitive_data_detection": map[string]interface{}{
					"detected":   false,
					"detections": []interface{}{},
				},
			},
		},
	}
}

func TestActivityList_SensitiveDataFilter(t *testing.T) {
	logger := zap.NewNop().Sugar()
	activities := createTestActivityRecords()
	mockCtrl := &mockActivityController{
		apiKey:     "test-key",
		activities: activities,
	}
	srv := NewServer(mockCtrl, logger, nil)

	t.Run("sensitive_data=true returns only activities with detections", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?sensitive_data=true", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                         `json:"success"`
			Data    contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)

		// Should return 4 activities with sensitive data (activity-1, activity-2, activity-3, activity-4)
		assert.Equal(t, 4, resp.Data.Total, "Should return 4 activities with sensitive data")

		// Verify all returned activities have HasSensitiveData=true
		for _, activity := range resp.Data.Activities {
			assert.True(t, activity.HasSensitiveData,
				"Activity %s should have HasSensitiveData=true", activity.ID)
		}
	})

	t.Run("sensitive_data=false returns only activities without detections", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?sensitive_data=false", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                         `json:"success"`
			Data    contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)

		// Should return 2 activities without sensitive data (activity-5, activity-6)
		assert.Equal(t, 2, resp.Data.Total, "Should return 2 activities without sensitive data")

		// Verify all returned activities have HasSensitiveData=false
		for _, activity := range resp.Data.Activities {
			assert.False(t, activity.HasSensitiveData,
				"Activity %s should have HasSensitiveData=false", activity.ID)
		}
	})
}

func TestActivityList_DetectionTypeFilter(t *testing.T) {
	logger := zap.NewNop().Sugar()
	activities := createTestActivityRecords()
	mockCtrl := &mockActivityController{
		apiKey:     "test-key",
		activities: activities,
	}
	srv := NewServer(mockCtrl, logger, nil)

	t.Run("detection_type=aws_access_key filters correctly", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?detection_type=aws_access_key", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                         `json:"success"`
			Data    contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Should return 2 activities with aws_access_key (activity-1, activity-3)
		assert.Equal(t, 2, resp.Data.Total, "Should return 2 activities with aws_access_key detection")

		// Verify all returned activities contain aws_access_key in DetectionTypes
		for _, activity := range resp.Data.Activities {
			assert.Contains(t, activity.DetectionTypes, "aws_access_key",
				"Activity %s should contain aws_access_key in DetectionTypes", activity.ID)
		}
	})

	t.Run("detection_type=credit_card filters correctly", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?detection_type=credit_card", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Should return 1 activity with credit_card (activity-2)
		assert.Equal(t, 1, resp.Data.Total, "Should return 1 activity with credit_card detection")
		assert.Contains(t, resp.Data.Activities[0].DetectionTypes, "credit_card")
	})

	t.Run("detection_type=nonexistent returns empty", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?detection_type=nonexistent_type", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, 0, resp.Data.Total, "Should return 0 activities for nonexistent detection type")
	})
}

func TestActivityList_SeverityFilter(t *testing.T) {
	logger := zap.NewNop().Sugar()
	activities := createTestActivityRecords()
	mockCtrl := &mockActivityController{
		apiKey:     "test-key",
		activities: activities,
	}
	srv := NewServer(mockCtrl, logger, nil)

	t.Run("severity=critical filters correctly", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?severity=critical", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                         `json:"success"`
			Data    contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Should return 2 activities with critical severity (activity-1, activity-3)
		assert.Equal(t, 2, resp.Data.Total, "Should return 2 activities with critical severity")

		for _, activity := range resp.Data.Activities {
			assert.Equal(t, "critical", activity.MaxSeverity,
				"Activity %s should have MaxSeverity=critical", activity.ID)
		}
	})

	t.Run("severity=high filters correctly", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?severity=high", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Should return 1 activity with high severity as max (activity-2)
		// Note: activity-3 has critical as max, not high
		assert.Equal(t, 1, resp.Data.Total, "Should return 1 activity with high as max severity")
	})

	t.Run("severity=medium filters correctly", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?severity=medium", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Should return 1 activity with medium severity (activity-4)
		assert.Equal(t, 1, resp.Data.Total, "Should return 1 activity with medium severity")
		assert.Equal(t, "medium", resp.Data.Activities[0].MaxSeverity)
	})
}

func TestActivityList_CombinedFilters(t *testing.T) {
	logger := zap.NewNop().Sugar()
	activities := createTestActivityRecords()
	mockCtrl := &mockActivityController{
		apiKey:     "test-key",
		activities: activities,
	}
	srv := NewServer(mockCtrl, logger, nil)

	t.Run("sensitive_data + detection_type combination", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/activity?sensitive_data=true&detection_type=aws_access_key", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Should return 2 activities (activity-1, activity-3)
		assert.Equal(t, 2, resp.Data.Total)

		for _, activity := range resp.Data.Activities {
			assert.True(t, activity.HasSensitiveData)
			assert.Contains(t, activity.DetectionTypes, "aws_access_key")
		}
	})

	t.Run("sensitive_data + severity combination", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/activity?sensitive_data=true&severity=critical", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Should return 2 activities with critical severity (activity-1, activity-3)
		assert.Equal(t, 2, resp.Data.Total)

		for _, activity := range resp.Data.Activities {
			assert.True(t, activity.HasSensitiveData)
			assert.Equal(t, "critical", activity.MaxSeverity)
		}
	})

	t.Run("detection_type + severity combination", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/activity?detection_type=aws_access_key&severity=critical", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Should return 2 activities (activity-1, activity-3)
		assert.Equal(t, 2, resp.Data.Total)

		for _, activity := range resp.Data.Activities {
			assert.Contains(t, activity.DetectionTypes, "aws_access_key")
			assert.Equal(t, "critical", activity.MaxSeverity)
		}
	})

	t.Run("all three filters combined", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/activity?sensitive_data=true&detection_type=github_token&severity=critical", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Should return 1 activity (activity-3) - has github_token and critical severity
		assert.Equal(t, 1, resp.Data.Total)
		assert.Equal(t, "activity-3-multiple", resp.Data.Activities[0].ID)
	})

	t.Run("combined with server filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/activity?sensitive_data=true&server=github", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Should return 2 activities from github with sensitive data (activity-1, activity-3)
		assert.Equal(t, 2, resp.Data.Total)

		for _, activity := range resp.Data.Activities {
			assert.Equal(t, "github", activity.ServerName)
			assert.True(t, activity.HasSensitiveData)
		}
	})
}

func TestActivityResponse_SensitiveDataFields(t *testing.T) {
	logger := zap.NewNop().Sugar()
	activities := createTestActivityRecords()
	mockCtrl := &mockActivityController{
		apiKey:     "test-key",
		activities: activities,
	}
	srv := NewServer(mockCtrl, logger, nil)

	t.Run("response includes has_sensitive_data field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Find an activity with sensitive data and verify the field
		foundSensitive := false
		foundClean := false
		for _, activity := range resp.Data.Activities {
			if activity.ID == "activity-1-aws-key" {
				assert.True(t, activity.HasSensitiveData, "should have has_sensitive_data=true")
				foundSensitive = true
			}
			if activity.ID == "activity-5-clean" {
				assert.False(t, activity.HasSensitiveData, "should have has_sensitive_data=false")
				foundClean = true
			}
		}
		assert.True(t, foundSensitive, "Should find activity with sensitive data")
		assert.True(t, foundClean, "Should find activity without sensitive data")
	})

	t.Run("response includes detection_types field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Find activity-3 which has multiple detection types
		for _, activity := range resp.Data.Activities {
			if activity.ID == "activity-3-multiple" {
				assert.Len(t, activity.DetectionTypes, 2, "Should have 2 detection types")
				assert.Contains(t, activity.DetectionTypes, "aws_access_key")
				assert.Contains(t, activity.DetectionTypes, "github_token")
				return
			}
		}
		t.Fatal("Should find activity-3-multiple in response")
	})

	t.Run("response includes max_severity field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.ActivityListResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Verify various severity levels
		severityMap := map[string]string{
			"activity-1-aws-key":     "critical",
			"activity-2-credit-card": "high",
			"activity-3-multiple":    "critical", // Has both critical and high, critical is max
			"activity-4-medium":      "medium",
			"activity-5-clean":       "",         // No sensitive data
		}

		for _, activity := range resp.Data.Activities {
			if expectedSeverity, ok := severityMap[activity.ID]; ok {
				assert.Equal(t, expectedSeverity, activity.MaxSeverity,
					"Activity %s should have MaxSeverity=%s", activity.ID, expectedSeverity)
			}
		}
	})

	t.Run("JSON serialization preserves sensitive data fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?sensitive_data=true", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// Parse raw JSON to verify field presence
		var rawResp map[string]interface{}
		err := json.NewDecoder(w.Body).Decode(&rawResp)
		require.NoError(t, err)

		data := rawResp["data"].(map[string]interface{})
		activities := data["activities"].([]interface{})
		require.NotEmpty(t, activities)

		firstActivity := activities[0].(map[string]interface{})

		// Verify fields exist in JSON
		_, hasField := firstActivity["has_sensitive_data"]
		assert.True(t, hasField, "JSON should include has_sensitive_data field")

		_, hasField = firstActivity["detection_types"]
		assert.True(t, hasField, "JSON should include detection_types field")

		_, hasField = firstActivity["max_severity"]
		assert.True(t, hasField, "JSON should include max_severity field")
	})
}

func TestActivityDetail_SensitiveDataFields(t *testing.T) {
	logger := zap.NewNop().Sugar()
	activities := createTestActivityRecords()
	mockCtrl := &mockActivityController{
		apiKey:     "test-key",
		activities: activities,
	}
	srv := NewServer(mockCtrl, logger, nil)

	t.Run("detail endpoint includes sensitive data fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/activity-1-aws-key", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                          `json:"success"`
			Data    contracts.ActivityDetailResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)

		activity := resp.Data.Activity
		assert.True(t, activity.HasSensitiveData)
		assert.Contains(t, activity.DetectionTypes, "aws_access_key")
		assert.Equal(t, "critical", activity.MaxSeverity)
	})

	t.Run("detail endpoint for clean activity has correct fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/activity/activity-5-clean", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                          `json:"success"`
			Data    contracts.ActivityDetailResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)

		activity := resp.Data.Activity
		assert.False(t, activity.HasSensitiveData)
		assert.Nil(t, activity.DetectionTypes)
		assert.Empty(t, activity.MaxSeverity)
	})
}
