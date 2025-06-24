package cache

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

func setupTestDB(t *testing.T) *bbolt.DB {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := bbolt.Open(dbPath, 0644, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	return db
}

func TestNewManager(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := zap.NewNop()

	manager, err := NewManager(db, logger)
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	if manager.db != db {
		t.Error("Database reference not set correctly")
	}

	if manager.stats == nil {
		t.Error("Stats not initialized")
	}
}

func TestGenerateKey(t *testing.T) {
	args1 := map[string]interface{}{
		"param1": "value1",
		"param2": 42,
	}
	args2 := map[string]interface{}{
		"param1": "value1",
		"param2": 43,
	}

	timestamp := time.Now()

	key1 := GenerateKey("test_tool", args1, timestamp)
	key2 := GenerateKey("test_tool", args1, timestamp)
	key3 := GenerateKey("test_tool", args2, timestamp)
	key4 := GenerateKey("different_tool", args1, timestamp)

	// Same inputs should produce same key
	if key1 != key2 {
		t.Error("Same inputs should produce same key")
	}

	// Different args should produce different keys
	if key1 == key3 {
		t.Error("Different args should produce different keys")
	}

	// Different tool names should produce different keys
	if key1 == key4 {
		t.Error("Different tool names should produce different keys")
	}

	// Keys should be hex strings of appropriate length
	if len(key1) != 64 { // SHA256 hex = 64 chars
		t.Errorf("Expected key length 64, got %d", len(key1))
	}
}

func TestStoreAndGet(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	manager, err := NewManager(db, logger)
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	// Test data
	key := "test_key"
	toolName := "test_tool"
	args := map[string]interface{}{"param": "value"}
	content := `{"data": [{"id": 1}, {"id": 2}], "total": 2}`
	recordPath := "data"
	totalRecords := 2

	// Store data
	err = manager.Store(key, toolName, args, content, recordPath, totalRecords)
	if err != nil {
		t.Fatalf("Failed to store data: %v", err)
	}

	// Retrieve data
	record, err := manager.Get(key)
	if err != nil {
		t.Fatalf("Failed to retrieve data: %v", err)
	}

	// Verify data
	if record.Key != key {
		t.Errorf("Expected key %s, got %s", key, record.Key)
	}
	if record.ToolName != toolName {
		t.Errorf("Expected tool name %s, got %s", toolName, record.ToolName)
	}
	if record.FullContent != content {
		t.Errorf("Expected content %s, got %s", content, record.FullContent)
	}
	if record.RecordPath != recordPath {
		t.Errorf("Expected record path %s, got %s", recordPath, record.RecordPath)
	}
	if record.TotalRecords != totalRecords {
		t.Errorf("Expected total records %d, got %d", totalRecords, record.TotalRecords)
	}
	if record.TotalSize != len(content) {
		t.Errorf("Expected total size %d, got %d", len(content), record.TotalSize)
	}

	// Access count should be incremented
	if record.AccessCount != 1 {
		t.Errorf("Expected access count 1, got %d", record.AccessCount)
	}
}

func TestGetNonExistent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	manager, err := NewManager(db, logger)
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	_, err = manager.Get("nonexistent_key")
	if err == nil {
		t.Error("Expected error for nonexistent key")
	}
}

func TestGetRecords(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	manager, err := NewManager(db, logger)
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	// Test data with records array
	key := "test_records"
	content := `{"data": [{"id": 1, "name": "item1"}, {"id": 2, "name": "item2"}, {"id": 3, "name": "item3"}, {"id": 4, "name": "item4"}], "total": 4}`

	err = manager.Store(key, "test_tool", map[string]interface{}{}, content, "data", 4)
	if err != nil {
		t.Fatalf("Failed to store data: %v", err)
	}

	// Test pagination
	response, err := manager.GetRecords(key, 0, 2)
	if err != nil {
		t.Fatalf("Failed to get records: %v", err)
	}

	if len(response.Records) != 2 {
		t.Errorf("Expected 2 records, got %d", len(response.Records))
	}

	if response.Meta.TotalRecords != 4 {
		t.Errorf("Expected total records 4, got %d", response.Meta.TotalRecords)
	}

	if response.Meta.Offset != 0 {
		t.Errorf("Expected offset 0, got %d", response.Meta.Offset)
	}

	if response.Meta.Limit != 2 {
		t.Errorf("Expected limit 2, got %d", response.Meta.Limit)
	}

	// Test second page
	response2, err := manager.GetRecords(key, 2, 2)
	if err != nil {
		t.Fatalf("Failed to get second page: %v", err)
	}

	if len(response2.Records) != 2 {
		t.Errorf("Expected 2 records on second page, got %d", len(response2.Records))
	}

	if response2.Meta.Offset != 2 {
		t.Errorf("Expected offset 2, got %d", response2.Meta.Offset)
	}

	// Test beyond available records
	response3, err := manager.GetRecords(key, 10, 2)
	if err != nil {
		t.Fatalf("Failed to get records beyond limit: %v", err)
	}

	if len(response3.Records) != 0 {
		t.Errorf("Expected 0 records beyond limit, got %d", len(response3.Records))
	}
}

func TestGetRecordsRootArray(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	manager, err := NewManager(db, logger)
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	// Test data with root array
	key := "test_array"
	content := `[{"id": 1}, {"id": 2}, {"id": 3}]`

	err = manager.Store(key, "test_tool", map[string]interface{}{}, content, "", 3)
	if err != nil {
		t.Fatalf("Failed to store data: %v", err)
	}

	response, err := manager.GetRecords(key, 0, 2)
	if err != nil {
		t.Fatalf("Failed to get records: %v", err)
	}

	if len(response.Records) != 2 {
		t.Errorf("Expected 2 records, got %d", len(response.Records))
	}

	if response.Meta.TotalRecords != 3 {
		t.Errorf("Expected total records 3, got %d", response.Meta.TotalRecords)
	}
}

func TestExpiredRecords(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	manager, err := NewManager(db, logger)
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	// Create a record with short TTL
	key := "expired_key"
	content := `{"data": "test"}`

	err = manager.Store(key, "test_tool", map[string]interface{}{}, content, "", 0)
	if err != nil {
		t.Fatalf("Failed to store data: %v", err)
	}

	// Manually expire the record
	err = db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		data := bucket.Get([]byte(key))
		if data == nil {
			return nil
		}

		var record Record
		if err := record.UnmarshalBinary(data); err != nil {
			return err
		}

		// Set expiry to past
		record.ExpiresAt = time.Now().Add(-time.Hour)

		data, err := record.MarshalBinary()
		if err != nil {
			return err
		}

		return bucket.Put([]byte(key), data)
	})
	if err != nil {
		t.Fatalf("Failed to manually expire record: %v", err)
	}

	// Try to get expired record
	_, err = manager.Get(key)
	if err == nil {
		t.Error("Expected error for expired record")
	}
}

func TestCleanup(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	manager, err := NewManager(db, logger)
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	// Store some records
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("test_key_%d", i)
		err = manager.Store(key, "test_tool", map[string]interface{}{}, `{"data": "test"}`, "", 0)
		if err != nil {
			t.Fatalf("Failed to store data: %v", err)
		}
	}

	// Manually expire some records
	err = db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		cursor := bucket.Cursor()

		count := 0
		for key, value := cursor.First(); key != nil && count < 2; key, value = cursor.Next() {
			var record Record
			if err := record.UnmarshalBinary(value); err != nil {
				continue
			}

			// Expire first two records
			record.ExpiresAt = time.Now().Add(-time.Hour)

			data, err := record.MarshalBinary()
			if err != nil {
				continue
			}

			_ = bucket.Put(key, data)
			count++
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Failed to manually expire records: %v", err)
	}

	// Run cleanup
	err = manager.cleanup()
	if err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	// Verify cleanup worked - should have 1 record left
	var remainingCount int
	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		cursor := bucket.Cursor()

		for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
			remainingCount++
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Failed to count remaining records: %v", err)
	}

	if remainingCount != 1 {
		t.Errorf("Expected 1 remaining record after cleanup, got %d", remainingCount)
	}
}

func TestStats(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	manager, err := NewManager(db, logger)
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	// Initially stats should be empty
	stats := manager.GetStats()
	if stats.TotalEntries != 0 {
		t.Errorf("Expected 0 initial entries, got %d", stats.TotalEntries)
	}

	// Store some data
	content := `{"data": "test data"}`
	err = manager.Store("test_key", "test_tool", map[string]interface{}{}, content, "", 0)
	if err != nil {
		t.Fatalf("Failed to store data: %v", err)
	}

	// Stats should be updated
	stats = manager.GetStats()
	if stats.TotalEntries != 1 {
		t.Errorf("Expected 1 entry after store, got %d", stats.TotalEntries)
	}

	if stats.TotalSizeBytes != len(content) {
		t.Errorf("Expected size %d, got %d", len(content), stats.TotalSizeBytes)
	}

	// Retrieve data (should increment hit count)
	_, err = manager.Get("test_key")
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}

	stats = manager.GetStats()
	if stats.HitCount != 1 {
		t.Errorf("Expected 1 hit, got %d", stats.HitCount)
	}

	// Try to get nonexistent data (should increment miss count)
	_, err = manager.Get("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent key")
	}

	stats = manager.GetStats()
	if stats.MissCount != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.MissCount)
	}
}

func TestGetRecordsComplexPath(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	manager, err := NewManager(db, logger)
	if err != nil {
		t.Fatalf("Failed to create cache manager: %v", err)
	}
	defer manager.Close()

	// Test data that mimics the user's problem - JSON with nested structure and parsed JSON strings
	key := "test_complex_path"
	content := `[{"type":"text","text":"{\"totalDataChart\": [[1633132800, 3], [1633219200, 5], [1633305600, 10], [1633392000, 100]]}"}]`
	recordPath := "[0].text(parsed).totalDataChart"

	err = manager.Store(key, "test_tool", map[string]interface{}{}, content, recordPath, 4)
	if err != nil {
		t.Fatalf("Failed to store data: %v", err)
	}

	// Test pagination
	response, err := manager.GetRecords(key, 0, 2)
	if err != nil {
		t.Fatalf("Failed to get records: %v", err)
	}

	if len(response.Records) != 2 {
		t.Errorf("Expected 2 records, got %d", len(response.Records))
	}

	if response.Meta.TotalRecords != 4 {
		t.Errorf("Expected total records 4, got %d", response.Meta.TotalRecords)
	}

	// Verify the actual record structure - should be [timestamp, value] arrays
	firstRecord, ok := response.Records[0].([]interface{})
	if !ok {
		t.Errorf("Expected first record to be an array, got %T", response.Records[0])
	} else if len(firstRecord) != 2 {
		t.Errorf("Expected first record to have 2 elements, got %d", len(firstRecord))
	}
}

func TestParsePathSegments(t *testing.T) {
	tests := []struct {
		path     string
		expected []PathSegment
	}{
		{
			path: "data",
			expected: []PathSegment{
				{Type: "object", Key: "data"},
			},
		},
		{
			path: "[0].text",
			expected: []PathSegment{
				{Type: "array", Index: 0},
				{Type: "object", Key: "text"},
			},
		},
		{
			path: "[0].text(parsed).totalDataChart",
			expected: []PathSegment{
				{Type: "array", Index: 0},
				{Type: "object", Key: "text"},
				{Type: "parsed"},
				{Type: "object", Key: "totalDataChart"},
			},
		},
		{
			path: "nested.array[2](parsed).items",
			expected: []PathSegment{
				{Type: "object", Key: "nested"},
				{Type: "object", Key: "array"},
				{Type: "array", Index: 2},
				{Type: "parsed"},
				{Type: "object", Key: "items"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			result := parsePathSegments(test.path)

			if len(result) != len(test.expected) {
				t.Errorf("Expected %d segments, got %d", len(test.expected), len(result))
				return
			}

			for i, expected := range test.expected {
				if result[i].Type != expected.Type {
					t.Errorf("Segment %d: expected type %s, got %s", i, expected.Type, result[i].Type)
				}
				if result[i].Key != expected.Key {
					t.Errorf("Segment %d: expected key %s, got %s", i, expected.Key, result[i].Key)
				}
				if result[i].Index != expected.Index {
					t.Errorf("Segment %d: expected index %d, got %d", i, expected.Index, result[i].Index)
				}
			}
		})
	}
}

func TestExtractRecordsArrayComplexPaths(t *testing.T) {
	tests := []struct {
		name        string
		data        interface{}
		path        string
		expectCount int
		expectError bool
	}{
		{
			name: "simple object path",
			data: map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{"id": 1},
					map[string]interface{}{"id": 2},
				},
			},
			path:        "data",
			expectCount: 2,
		},
		{
			name: "array index path",
			data: []interface{}{
				map[string]interface{}{
					"items": []interface{}{
						map[string]interface{}{"id": 1},
						map[string]interface{}{"id": 2},
						map[string]interface{}{"id": 3},
					},
				},
			},
			path:        "[0].items",
			expectCount: 3,
		},
		{
			name: "parsed JSON path",
			data: []interface{}{
				map[string]interface{}{
					"text": `{"totalDataChart": [[1633132800, 3], [1633219200, 5], [1633305600, 10]]}`,
				},
			},
			path:        "[0].text(parsed).totalDataChart",
			expectCount: 3,
		},
		{
			name: "invalid array index",
			data: []interface{}{
				map[string]interface{}{"id": 1},
			},
			path:        "[5].items",
			expectError: true,
		},
		{
			name: "invalid key",
			data: map[string]interface{}{
				"data": []interface{}{},
			},
			path:        "nonexistent",
			expectError: true,
		},
		{
			name: "invalid parsed JSON",
			data: []interface{}{
				map[string]interface{}{
					"text": "not valid json",
				},
			},
			path:        "[0].text(parsed).items",
			expectError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := extractRecordsArray(test.data, test.path)

			if test.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(result) != test.expectCount {
				t.Errorf("Expected %d records, got %d", test.expectCount, len(result))
			}
		})
	}
}
