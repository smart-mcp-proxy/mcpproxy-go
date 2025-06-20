package truncate

import (
	"strings"
	"testing"
)

func TestNewTruncator(t *testing.T) {
	truncator := NewTruncator(1000)

	if truncator.limit != 1000 {
		t.Errorf("Expected limit 1000, got %d", truncator.limit)
	}
}

func TestShouldTruncate(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		content  string
		expected bool
	}{
		{"Below limit", 1000, "short content", false},
		{"At limit", 10, "1234567890", false},
		{"Above limit", 10, "12345678901", true},
		{"Disabled (limit 0)", 0, "any content", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncator := NewTruncator(tt.limit)
			result := truncator.ShouldTruncate(tt.content)

			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestAnalyzeJSONStructure(t *testing.T) {
	truncator := NewTruncator(1000)

	tests := []struct {
		name          string
		content       string
		expectError   bool
		expectedPath  string
		expectedCount int
	}{
		{
			name:          "Root array",
			content:       `[{"id": 1}, {"id": 2}, {"id": 3}]`,
			expectError:   false,
			expectedPath:  "",
			expectedCount: 3,
		},
		{
			name:          "Object with data array",
			content:       `{"data": [{"id": 1}, {"id": 2}], "total": 2}`,
			expectError:   false,
			expectedPath:  "data",
			expectedCount: 2,
		},
		{
			name:          "Object with results array",
			content:       `{"results": [{"name": "item1"}, {"name": "item2"}]}`,
			expectError:   false,
			expectedPath:  "results",
			expectedCount: 2,
		},
		{
			name:          "Object with items array",
			content:       `{"items": [{"a": 1}, {"b": 2}, {"c": 3}]}`,
			expectError:   false,
			expectedPath:  "items",
			expectedCount: 3,
		},
		{
			name:          "Nested structure - choose biggest",
			content:       `{"response": {"data": [{"id": 1, "name": "very long name with lots of content"}, {"id": 2, "name": "another very long name"}], "metadata": {"tags": ["tag1", "tag2"]}}}`,
			expectError:   false,
			expectedPath:  "response.data",
			expectedCount: 2,
		},
		{
			name:          "Spoonacular-style structure",
			content:       `{"results": [{"id": 635675, "image": "https://img.spoonacular.com/recipes/635675-312x231.jpg", "title": "Boozy Bbq Chicken", "readyInMinutes": 45}, {"id": 635676, "title": "Another Recipe"}], "offset": 0, "number": 2}`,
			expectError:   false,
			expectedPath:  "results",
			expectedCount: 2,
		},
		{
			name:          "Multiple arrays - biggest wins",
			content:       `{"users": [{"id": 1}], "data": [{"value": "small"}], "results": [{"big": "content with much more data here to make this array substantially larger"}]}`,
			expectError:   false,
			expectedPath:  "results", // Biggest by size
			expectedCount: 1,
		},
		{
			name:          "Multiple arrays - size wins",
			content:       `{"smallArray": [{"x": 1}], "bigArray": [{"very": "long content with lots of data"}, {"more": "substantial content here"}, {"even": "more data"}]}`,
			expectError:   false,
			expectedPath:  "bigArray", // Bigger size
			expectedCount: 3,
		},
		{
			name:          "Deep nesting level 3",
			content:       `{"level1": {"level2": {"level3": {"data": [{"deep": "item1"}, {"deep": "item2"}]}}}}`,
			expectError:   false,
			expectedPath:  "level1.level2.level3.data",
			expectedCount: 2,
		},
		{
			name:          "Too deep nesting level 4 - not found",
			content:       `{"level1": {"level2": {"level3": {"level4": {"data": [{"deep": "item1"}]}}}}}`,
			expectError:   true,
			expectedPath:  "",
			expectedCount: 0,
		},
		{
			name:          "Invalid JSON",
			content:       `{"invalid": json}`,
			expectError:   true,
			expectedPath:  "",
			expectedCount: 0,
		},
		{
			name:          "No array found",
			content:       `{"message": "no arrays here"}`,
			expectError:   true,
			expectedPath:  "",
			expectedCount: 0,
		},
		{
			name:          "Empty array",
			content:       `{"data": []}`,
			expectError:   false,
			expectedPath:  "data",
			expectedCount: 0,
		},
		{
			name:          "User scenario - totalDataChart with timestamp-value pairs",
			content:       `{"totalDataChart": [[1534550400, 8], [1534636800, 26], [1534723200, 8], [1534809600, 35], [1534896000, 42], [1534982400, 24], [1535068800, 11], [1535155200, 7], [1535241600, 25], [1535328000, 15]]}`,
			expectError:   false,
			expectedPath:  "totalDataChart",
			expectedCount: 10,
		},
		{
			name:          "JSON string within text field",
			content:       `[{"type":"text","text":"{\"data\": [{\"id\": 1}, {\"id\": 2}, {\"id\": 3}]}"}]`,
			expectError:   false,
			expectedPath:  "[0].text(parsed).data",
			expectedCount: 3,
		},
		{
			name:          "Nested JSON with large array inside text",
			content:       `[{"type":"text","text":"{\"totalDataChart\": [[1534550400, 8], [1534636800, 26], [1534723200, 8]]}"}]`,
			expectError:   false,
			expectedPath:  "[0].text(parsed).totalDataChart",
			expectedCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, count, err := truncator.analyzeJSONStructure(tt.content)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if path != tt.expectedPath {
				t.Errorf("Expected path '%s', got '%s'", tt.expectedPath, path)
			}

			if count != tt.expectedCount {
				t.Errorf("Expected count %d, got %d", tt.expectedCount, count)
			}
		})
	}
}

func TestTruncateWithinLimit(t *testing.T) {
	truncator := NewTruncator(1000)
	content := `{"data": [{"id": 1}]}`
	args := map[string]interface{}{"param": "value"}

	result := truncator.Truncate(content, "test_tool", args)

	if result.TruncatedContent != content {
		t.Error("Content within limit should not be modified")
	}

	if result.CacheAvailable {
		t.Error("Cache should not be available for content within limit")
	}

	if result.TotalSize != len(content) {
		t.Errorf("Expected total size %d, got %d", len(content), result.TotalSize)
	}
}

func TestTruncateSimpleMode(t *testing.T) {
	truncator := NewTruncator(50) // Very small limit
	content := `{"message": "this is a simple response that doesn't have structured records"}`
	args := map[string]interface{}{"param": "value"}

	result := truncator.Truncate(content, "test_tool", args)

	if !strings.Contains(result.TruncatedContent, "truncated by mcpproxy, cache not available") {
		t.Error("Simple truncation message not found")
	}

	if result.CacheAvailable {
		t.Error("Cache should not be available for simple truncation")
	}

	if result.TotalSize != len(content) {
		t.Errorf("Expected total size %d, got %d", len(content), result.TotalSize)
	}
}

func TestTruncateWithCache(t *testing.T) {
	truncator := NewTruncator(100) // Small limit to trigger truncation
	content := `{"data": [{"id": 1, "name": "item1"}, {"id": 2, "name": "item2"}, {"id": 3, "name": "item3"}], "total": 3}`
	args := map[string]interface{}{"param": "value"}

	result := truncator.Truncate(content, "test_tool", args)

	if !result.CacheAvailable {
		t.Error("Cache should be available for structured data")
	}

	if result.CacheKey == "" {
		t.Error("Cache key should be generated")
	}

	if result.RecordPath != "data" {
		t.Errorf("Expected record path 'data', got '%s'", result.RecordPath)
	}

	if result.TotalRecords != 3 {
		t.Errorf("Expected total records 3, got %d", result.TotalRecords)
	}

	if !strings.Contains(result.TruncatedContent, "truncated by mcpproxy") {
		t.Error("Truncation message not found")
	}

	if !strings.Contains(result.TruncatedContent, "read_cache") {
		t.Error("read_cache instruction not found")
	}

	if !strings.Contains(result.TruncatedContent, result.CacheKey) {
		t.Error("Cache key not found in instructions")
	}
}

func TestTruncateRootArray(t *testing.T) {
	truncator := NewTruncator(30) // Small limit to trigger truncation
	content := `[{"id": 1}, {"id": 2}, {"id": 3}, {"id": 4}]`
	args := map[string]interface{}{}

	result := truncator.Truncate(content, "test_tool", args)

	if !result.CacheAvailable {
		t.Error("Cache should be available for root array")
	}

	if result.RecordPath != "" {
		t.Errorf("Expected empty record path for root array, got '%s'", result.RecordPath)
	}

	if result.TotalRecords != 4 {
		t.Errorf("Expected total records 4, got %d", result.TotalRecords)
	}
}

func TestSimpleTruncate(t *testing.T) {
	truncator := NewTruncator(100)
	content := strings.Repeat("a", 200) // 200 character string

	result := truncator.simpleTruncate(content)

	if len(result) > 100 {
		t.Errorf("Result should be truncated to limit, got length %d", len(result))
	}

	if !strings.Contains(result, "truncated by mcpproxy, cache not available") {
		t.Error("Simple truncation message not found")
	}
}

func TestCreateTruncatedWithCache(t *testing.T) {
	truncator := NewTruncator(500)
	content := strings.Repeat("x", 1000) // 1000 character string
	cacheKey := "test_cache_key"
	totalRecords := 10
	totalSize := 1000

	result := truncator.createTruncatedWithCache(content, cacheKey, totalRecords, totalSize)

	if len(result) > 500 {
		t.Errorf("Result should be within limit, got length %d", len(result))
	}

	// Check for required elements in instructions
	expectedStrings := []string{
		"truncated by mcpproxy",
		"read_cache",
		cacheKey,
		"records: 10",
		"total_size\": 1000",
	}

	for _, expected := range expectedStrings {
		if !strings.Contains(result, expected) {
			t.Errorf("Expected string '%s' not found in result", expected)
		}
	}
}

func TestTruncatePreservesJSONStructure(t *testing.T) {
	truncator := NewTruncator(200)
	content := `{"data": [{"id": 1, "name": "very long name that might cause truncation issues"}, {"id": 2, "name": "another long name"}], "metadata": {"total": 2, "extra": "some additional data"}}`
	args := map[string]interface{}{}

	result := truncator.Truncate(content, "test_tool", args)

	// The truncated part should try to preserve JSON structure
	truncatedPart := strings.Split(result.TruncatedContent, "... [truncated by mcpproxy]")[0]

	// Should end with } or ] for valid JSON structure
	trimmed := strings.TrimSpace(truncatedPart)
	if !strings.HasSuffix(trimmed, "}") && !strings.HasSuffix(trimmed, "]") {
		t.Error("Truncated content should preserve JSON structure")
	}
}

func TestMultipleArrayFields(t *testing.T) {
	truncator := NewTruncator(1000)
	content := `{
		"users": [{"id": 1}, {"id": 2}],
		"data": [{"value": "a"}, {"value": "b"}, {"value": "c"}],
		"results": [{"name": "x"}]
	}`

	path, count, err := truncator.analyzeJSONStructure(content)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should find the largest array by size
	if path != "data" {
		t.Errorf("Expected 'data' (largest array), got '%s'", path)
	}

	if count != 3 {
		t.Errorf("Expected count 3 for data array, got %d", count)
	}
}

func TestLargeLimitNoTruncation(t *testing.T) {
	truncator := NewTruncator(10000) // Large limit
	content := `{"data": [{"id": 1}, {"id": 2}]}`
	args := map[string]interface{}{}

	result := truncator.Truncate(content, "test_tool", args)

	if result.TruncatedContent != content {
		t.Error("Content should not be truncated with large limit")
	}

	if result.CacheAvailable {
		t.Error("Cache should not be available when no truncation occurs")
	}
}

func TestZeroLimitDisabled(t *testing.T) {
	truncator := NewTruncator(0)          // Disabled
	content := strings.Repeat("x", 10000) // Very large content

	if truncator.ShouldTruncate(content) {
		t.Error("Truncation should be disabled with limit 0")
	}

	result := truncator.Truncate(content, "test_tool", map[string]interface{}{})
	if result.TruncatedContent != content {
		t.Error("Content should not be modified when truncation is disabled")
	}
}

func TestFindArraysRecursive(t *testing.T) {
	truncator := NewTruncator(1000)

	tests := []struct {
		name           string
		data           interface{}
		expectedPaths  []string
		expectedCounts []int
	}{
		{
			name: "Simple object with array",
			data: map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{"id": 1},
					map[string]interface{}{"id": 2},
				},
			},
			expectedPaths:  []string{"data"},
			expectedCounts: []int{2},
		},
		{
			name: "Nested structure",
			data: map[string]interface{}{
				"response": map[string]interface{}{
					"results": []interface{}{
						map[string]interface{}{"name": "item1"},
					},
					"metadata": map[string]interface{}{
						"tags": []interface{}{"tag1", "tag2"},
					},
				},
			},
			expectedPaths:  []string{"response.results", "response.metadata.tags"},
			expectedCounts: []int{1, 2},
		},
		{
			name: "Root array",
			data: []interface{}{
				map[string]interface{}{"id": 1},
				map[string]interface{}{"id": 2},
			},
			expectedPaths:  []string{""},
			expectedCounts: []int{2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arrays := truncator.findArraysRecursive(tt.data, "", 0, 3)

			if len(arrays) != len(tt.expectedPaths) {
				t.Errorf("Expected %d arrays, got %d", len(tt.expectedPaths), len(arrays))
				return
			}

			// Create maps for easier comparison
			foundPaths := make(map[string]int)
			for _, arr := range arrays {
				foundPaths[arr.Path] = arr.Count
			}

			for i, expectedPath := range tt.expectedPaths {
				if count, exists := foundPaths[expectedPath]; !exists {
					t.Errorf("Expected path '%s' not found", expectedPath)
				} else if count != tt.expectedCounts[i] {
					t.Errorf("Expected count %d for path '%s', got %d", tt.expectedCounts[i], expectedPath, count)
				}
			}
		})
	}
}

func TestCalculateArraySize(t *testing.T) {
	truncator := NewTruncator(1000)

	tests := []struct {
		name     string
		array    []interface{}
		expected int
	}{
		{
			name:     "Empty array",
			array:    []interface{}{},
			expected: 2, // "[]"
		},
		{
			name: "Small array",
			array: []interface{}{
				map[string]interface{}{"id": 1},
				map[string]interface{}{"id": 2},
			},
			expected: 19, // [{"id":1},{"id":2}]
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size := truncator.calculateArraySize(tt.array)
			if size != tt.expected {
				t.Errorf("Expected size %d, got %d", tt.expected, size)
			}
		})
	}
}

func TestChooseBestArray(t *testing.T) {
	truncator := NewTruncator(1000)

	tests := []struct {
		name     string
		arrays   []ArrayInfo
		expected string // expected path
	}{
		{
			name: "Size wins",
			arrays: []ArrayInfo{
				{Path: "results", Count: 10, Size: 500},
				{Path: "data", Count: 5, Size: 1000},
			},
			expected: "data",
		},
		{
			name: "Larger size wins",
			arrays: []ArrayInfo{
				{Path: "array1", Count: 5, Size: 500},
				{Path: "array2", Count: 10, Size: 1000},
			},
			expected: "array2",
		},
		{
			name: "First array when equal",
			arrays: []ArrayInfo{
				{Path: "first", Count: 5, Size: 500},
				{Path: "second", Count: 5, Size: 500},
			},
			expected: "first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			best := truncator.chooseBestArray(tt.arrays, "")
			if best.Path != tt.expected {
				t.Errorf("Expected path '%s', got '%s'", tt.expected, best.Path)
			}
		})
	}
}
