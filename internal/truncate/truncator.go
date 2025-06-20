package truncate

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mcpproxy-go/internal/cache"
)

// TruncationResult represents the result of truncating a tool response
type TruncationResult struct {
	TruncatedContent string `json:"truncated_content"`
	CacheKey         string `json:"cache_key,omitempty"`
	RecordPath       string `json:"record_path,omitempty"`
	TotalRecords     int    `json:"total_records,omitempty"`
	TotalSize        int    `json:"total_size"`
	CacheAvailable   bool   `json:"cache_available"`
}

// Truncator handles truncating large tool responses
type Truncator struct {
	limit int
}

// NewTruncator creates a new truncator with the specified character limit
func NewTruncator(limit int) *Truncator {
	return &Truncator{limit: limit}
}

// Truncate analyzes and truncates a tool response if it exceeds the limit
func (t *Truncator) Truncate(content, toolName string, args map[string]interface{}) *TruncationResult {
	result := &TruncationResult{
		TotalSize: len(content),
	}

	// If truncation is disabled (limit 0) or content is within limit, return as-is
	if t.limit == 0 || len(content) <= t.limit {
		result.TruncatedContent = content
		return result
	}

	// Try to analyze JSON structure for record splitting
	recordPath, totalRecords, err := t.analyzeJSONStructure(content)
	if err != nil {
		// JSON analysis failed, do simple truncation
		result.TruncatedContent = t.simpleTruncate(content)
		result.CacheAvailable = false
		return result
	}

	// Generate cache key
	timestamp := time.Now()
	cacheKey := cache.GenerateKey(toolName, args, timestamp)

	// Create truncated content with cache instructions
	result.TruncatedContent = t.createTruncatedWithCache(content, cacheKey, totalRecords, len(content))
	result.CacheKey = cacheKey
	result.RecordPath = recordPath
	result.TotalRecords = totalRecords
	result.CacheAvailable = true

	return result
}

// ArrayInfo holds information about found arrays
type ArrayInfo struct {
	Path  string
	Count int
	Size  int // Character size of the array in JSON
}

// findArraysRecursive recursively finds all arrays in JSON structure up to maxDepth
func (t *Truncator) findArraysRecursive(data interface{}, currentPath string, depth int, maxDepth int) []ArrayInfo {
	var arrays []ArrayInfo

	if depth > maxDepth {
		return arrays
	}

	switch v := data.(type) {
	case []interface{}:
		// Found an array - calculate its size
		arraySize := t.calculateArraySize(v)

		arrays = append(arrays, ArrayInfo{
			Path:  currentPath,
			Count: len(v),
			Size:  arraySize,
		})

		// If this array has only 1-2 elements, search deeper within its elements
		// This handles cases where the outer structure has few objects but each contains many records
		if len(v) <= 2 {
			for i, element := range v {
				elementPath := fmt.Sprintf("%s[%d]", currentPath, i)
				if currentPath == "" {
					elementPath = fmt.Sprintf("[%d]", i)
				}
				childArrays := t.findArraysRecursive(element, elementPath, depth+1, maxDepth)
				arrays = append(arrays, childArrays...)
			}
		}

	case map[string]interface{}:
		// Traverse object fields
		for key, val := range v {
			newPath := key
			if currentPath != "" {
				newPath = currentPath + "." + key
			}

			// Check if this value is a JSON string that should be parsed
			if strVal, ok := val.(string); ok && t.looksLikeJSON(strVal) {
				// Try to parse the JSON string and find arrays within it
				if parsedArrays := t.parseJSONStringForArrays(strVal, newPath, depth, maxDepth); len(parsedArrays) > 0 {
					arrays = append(arrays, parsedArrays...)
				}
			}

			childArrays := t.findArraysRecursive(val, newPath, depth+1, maxDepth)
			arrays = append(arrays, childArrays...)
		}
	}

	return arrays
}

// looksLikeJSON checks if a string looks like it contains JSON
func (t *Truncator) looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	return (strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) ||
		(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"))
}

// parseJSONStringForArrays attempts to parse a JSON string and find arrays within it
func (t *Truncator) parseJSONStringForArrays(jsonStr, basePath string, depth int, maxDepth int) []ArrayInfo {
	// Don't exceed max depth when parsing JSON strings
	if depth >= maxDepth {
		return nil
	}

	var parsedData interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsedData); err != nil {
		return nil
	}

	// Recursively find arrays in the parsed JSON, starting from current depth
	arrays := t.findArraysRecursive(parsedData, "", depth, maxDepth)

	// Adjust the paths to indicate they're from parsed JSON and include the base path
	for i := range arrays {
		if arrays[i].Path == "" {
			arrays[i].Path = basePath + "(parsed)"
		} else {
			arrays[i].Path = basePath + "(parsed)." + arrays[i].Path
		}
	}

	return arrays
}

// calculateArraySize estimates the character size of an array in JSON
func (t *Truncator) calculateArraySize(arr []interface{}) int {
	if len(arr) == 0 {
		return 2 // "[]"
	}

	// Marshal just the array to get its size
	if jsonBytes, err := json.Marshal(arr); err == nil {
		return len(jsonBytes)
	}

	// Fallback: rough estimation
	return len(arr) * 50 // Assume ~50 chars per item on average
}

// chooseBestArray selects the best array using enhanced criteria
func (t *Truncator) chooseBestArray(arrays []ArrayInfo, originalContent string) ArrayInfo {
	if len(arrays) == 0 {
		return ArrayInfo{}
	}

	// Enhanced selection criteria:
	// 1. Prefer arrays with more elements (better for pagination)
	// 2. Among arrays with similar element counts, prefer larger size
	// 3. Avoid arrays with very few elements unless they're much larger

	bestArray := arrays[0]

	for _, arr := range arrays[1:] {
		// If this array has significantly more elements, prefer it
		if arr.Count > bestArray.Count*2 {
			bestArray = arr
			continue
		}

		// If counts are similar (within 2x), prefer by size
		if arr.Count >= bestArray.Count/2 && arr.Size > bestArray.Size {
			bestArray = arr
			continue
		}

		// Special case: if best array has very few elements (≤2) but current has many more
		if bestArray.Count <= 2 && arr.Count > 10 {
			bestArray = arr
			continue
		}
	}

	return bestArray
}

// analyzeJSONStructure analyzes JSON content to find record arrays
func (t *Truncator) analyzeJSONStructure(content string) (recordPath string, totalRecords int, err error) {
	var data interface{}
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return "", 0, fmt.Errorf("invalid JSON: %w", err)
	}

	// Find all arrays recursively up to level 4 (to handle deep nesting)
	arrays := t.findArraysRecursive(data, "", 0, 4)
	if len(arrays) == 0 {
		return "", 0, fmt.Errorf("no record array found")
	}

	// Choose the largest array by size
	bestArray := t.chooseBestArray(arrays, content)
	return bestArray.Path, bestArray.Count, nil
}

// simpleTruncate performs basic truncation without caching
func (t *Truncator) simpleTruncate(content string) string {
	if len(content) <= t.limit {
		return content
	}

	messageSpace := 200
	if t.limit < messageSpace {
		messageSpace = t.limit / 2 // Use half the limit for message
	}

	truncatePoint := t.limit - messageSpace
	if truncatePoint < 0 {
		truncatePoint = 0
	}

	truncated := content[:truncatePoint]
	return truncated + "\n\n... [truncated by mcpproxy, cache not available]"
}

// createTruncatedWithCache creates a truncated response with cache instructions
func (t *Truncator) createTruncatedWithCache(content, cacheKey string, totalRecords, totalSize int) string {
	instructions := fmt.Sprintf(`

... [truncated by mcpproxy]

Response truncated (limit: %d chars, actual: %d chars, records: %d)
Use read_cache tool: key="%s", offset=0, limit=50
Returns: {"records": [...], "meta": {"total_records": %d, "total_size": %d}}`,
		t.limit, totalSize, totalRecords, cacheKey, totalRecords, totalSize)

	// Calculate how much content we can show (ensure result fits within limit)
	instructionsSize := len(instructions)
	availableSize := t.limit - instructionsSize

	if availableSize < 0 {
		availableSize = 0
	}

	if availableSize > len(content) {
		availableSize = len(content)
	}

	truncated := content[:availableSize]

	// Try to find a good breaking point (end of JSON object/array)
	if availableSize > 0 && availableSize < len(content) {
		if lastBrace := strings.LastIndex(truncated, "}"); lastBrace > availableSize/2 {
			truncated = truncated[:lastBrace+1]
		} else if lastBracket := strings.LastIndex(truncated, "]"); lastBracket > availableSize/2 {
			truncated = truncated[:lastBracket+1]
		}
	}

	return truncated + instructions
}

// ShouldTruncate returns true if content should be truncated
func (t *Truncator) ShouldTruncate(content string) bool {
	return t.limit > 0 && len(content) > t.limit
}
