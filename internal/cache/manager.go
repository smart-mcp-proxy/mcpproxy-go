package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

const (
	CacheBucket      = "cache"
	CacheStatsBucket = "cache_stats"
	DefaultTTL       = 2 * time.Hour
	CleanupInterval  = 10 * time.Minute
)

// Manager handles cached tool responses
type Manager struct {
	db     *bbolt.DB
	logger *zap.Logger
	stats  *Stats
	stopCh chan struct{}
}

// NewManager creates a new cache manager
func NewManager(db *bbolt.DB, logger *zap.Logger) (*Manager, error) {
	manager := &Manager{
		db:     db,
		logger: logger,
		stats:  &Stats{},
		stopCh: make(chan struct{}),
	}

	// Initialize buckets
	err := db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(CacheBucket)); err != nil {
			return fmt.Errorf("create cache bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(CacheStatsBucket)); err != nil {
			return fmt.Errorf("create cache stats bucket: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Load existing stats
	if err := manager.loadStats(); err != nil {
		logger.Warn("Failed to load cache stats", zap.Error(err))
	}

	// Start background cleanup
	go manager.startCleanup()

	return manager, nil
}

// GenerateKey generates a cache key from tool name, arguments, and timestamp
func GenerateKey(toolName string, args map[string]interface{}, timestamp time.Time) string {
	// Create a consistent string representation
	argsJSON, _ := json.Marshal(args)
	input := fmt.Sprintf("%s:%s:%d", toolName, string(argsJSON), timestamp.Unix())

	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}

// Store saves a tool response to cache
func (m *Manager) Store(key, toolName string, args map[string]interface{}, content string, recordPath string, totalRecords int) error {
	record := &Record{
		Key:          key,
		ToolName:     toolName,
		Args:         args,
		Timestamp:    time.Now(),
		FullContent:  content,
		RecordPath:   recordPath,
		TotalRecords: totalRecords,
		TotalSize:    len(content),
		ExpiresAt:    time.Now().Add(DefaultTTL),
		AccessCount:  0,
		LastAccessed: time.Now(),
		CreatedAt:    time.Now(),
	}

	return m.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		data, err := record.MarshalBinary()
		if err != nil {
			return fmt.Errorf("marshal cache record: %w", err)
		}

		if err := bucket.Put([]byte(key), data); err != nil {
			return fmt.Errorf("store cache record: %w", err)
		}

		// Update stats
		m.stats.TotalEntries++
		m.stats.TotalSizeBytes += len(content)

		return m.saveStats(tx)
	})
}

// Get retrieves a cached tool response
func (m *Manager) Get(key string) (*Record, error) {
	var record *Record

	err := m.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		data := bucket.Get([]byte(key))
		if data == nil {
			m.stats.MissCount++
			_ = m.saveStats(tx)
			return fmt.Errorf("cache key not found")
		}

		record = &Record{}
		if err := record.UnmarshalBinary(data); err != nil {
			return fmt.Errorf("unmarshal cache record: %w", err)
		}

		// Check if expired
		if record.IsExpired() {
			_ = bucket.Delete([]byte(key))
			m.stats.EvictedCount++
			m.stats.TotalEntries--
			m.stats.TotalSizeBytes -= record.TotalSize
			_ = m.saveStats(tx)
			return fmt.Errorf("cache key expired")
		}

		// Update access stats
		record.AccessCount++
		record.LastAccessed = time.Now()

		data, err := record.MarshalBinary()
		if err != nil {
			return fmt.Errorf("marshal updated record: %w", err)
		}

		if err := bucket.Put([]byte(key), data); err != nil {
			return fmt.Errorf("update access stats: %w", err)
		}

		m.stats.HitCount++
		return m.saveStats(tx)
	})

	return record, err
}

// GetRecords retrieves paginated records from a cached response
func (m *Manager) GetRecords(key string, offset, limit int) (*ReadCacheResponse, error) {
	record, err := m.Get(key)
	if err != nil {
		return nil, err
	}

	// Parse the full content as JSON
	var fullData interface{}
	if err := json.Unmarshal([]byte(record.FullContent), &fullData); err != nil {
		return nil, fmt.Errorf("failed to parse cached content as JSON: %w", err)
	}

	// Extract records array
	records, err := extractRecordsArray(fullData, record.RecordPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract records: %w", err)
	}

	// Apply pagination
	totalRecords := len(records)
	if offset >= totalRecords {
		offset = totalRecords
	}

	end := offset + limit
	if end > totalRecords {
		end = totalRecords
	}

	var paginatedRecords []interface{}
	if offset < totalRecords {
		paginatedRecords = records[offset:end]
	}

	response := &ReadCacheResponse{
		Records: paginatedRecords,
		Meta: Meta{
			Key:          key,
			TotalRecords: totalRecords,
			Limit:        limit,
			Offset:       offset,
			TotalSize:    record.TotalSize,
			RecordPath:   record.RecordPath,
		},
	}

	return response, nil
}

// GetStats returns current cache statistics
func (m *Manager) GetStats() *Stats {
	return m.stats
}

// startCleanup runs periodic cleanup of expired cache entries
func (m *Manager) startCleanup() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := m.cleanup(); err != nil {
				m.logger.Error("Cache cleanup failed", zap.Error(err))
			}
		case <-m.stopCh:
			return
		}
	}
}

// cleanup removes expired cache entries
func (m *Manager) cleanup() error {
	now := time.Now()
	cleanupCount := 0
	totalSizeReduced := 0

	err := m.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheBucket))
		cursor := bucket.Cursor()

		var keysToDelete [][]byte

		for key, value := cursor.First(); key != nil; key, value = cursor.Next() {
			var record Record
			if err := record.UnmarshalBinary(value); err != nil {
				m.logger.Warn("Failed to unmarshal cache record during cleanup",
					zap.String("key", string(key)), zap.Error(err))
				keysToDelete = append(keysToDelete, key)
				continue
			}

			if now.After(record.ExpiresAt) {
				keysToDelete = append(keysToDelete, key)
				cleanupCount++
				totalSizeReduced += record.TotalSize
			}
		}

		// Delete expired keys
		for _, key := range keysToDelete {
			if err := bucket.Delete(key); err != nil {
				return fmt.Errorf("delete expired key: %w", err)
			}
		}

		// Update stats
		m.stats.CleanupCount += cleanupCount
		m.stats.TotalEntries -= cleanupCount
		m.stats.TotalSizeBytes -= totalSizeReduced

		return m.saveStats(tx)
	})

	if err != nil {
		return err
	}

	if cleanupCount > 0 {
		m.logger.Info("Cache cleanup completed",
			zap.Int("expired_entries", cleanupCount),
			zap.Int("size_reduced_bytes", totalSizeReduced))
	}

	return nil
}

// loadStats loads cache statistics from database
func (m *Manager) loadStats() error {
	return m.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(CacheStatsBucket))
		data := bucket.Get([]byte("stats"))
		if data == nil {
			return nil // No existing stats
		}

		return m.stats.UnmarshalBinary(data)
	})
}

// saveStats saves cache statistics to database
func (m *Manager) saveStats(tx *bbolt.Tx) error {
	bucket := tx.Bucket([]byte(CacheStatsBucket))
	data, err := m.stats.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}

	return bucket.Put([]byte("stats"), data)
}

// Close stops the cache manager
func (m *Manager) Close() {
	close(m.stopCh)
}

// extractRecordsArray extracts an array from JSON data using a path
func extractRecordsArray(data interface{}, path string) ([]interface{}, error) {
	if path == "" {
		// Try to find records array automatically
		if arr, ok := data.([]interface{}); ok {
			return arr, nil
		}

		if obj, ok := data.(map[string]interface{}); ok {
			// Look for common array field names
			commonNames := []string{"records", "data", "items", "results", "list", "array"}
			for _, name := range commonNames {
				if val, exists := obj[name]; exists {
					if arr, ok := val.([]interface{}); ok {
						return arr, nil
					}
				}
			}
		}

		return nil, fmt.Errorf("no records array found")
	}

	// Parse complex paths like "[0].text(parsed).totalDataChart"
	current := data
	pathSegments := parsePathSegments(path)

	for _, segment := range pathSegments {
		switch segment.Type {
		case "object":
			if obj, ok := current.(map[string]interface{}); ok {
				if val, exists := obj[segment.Key]; exists {
					current = val
				} else {
					return nil, fmt.Errorf("invalid record path: key '%s' not found", segment.Key)
				}
			} else {
				return nil, fmt.Errorf("invalid record path: expected object for key '%s'", segment.Key)
			}
		case "array":
			if arr, ok := current.([]interface{}); ok {
				if segment.Index >= 0 && segment.Index < len(arr) {
					current = arr[segment.Index]
				} else {
					return nil, fmt.Errorf("invalid record path: array index %d out of bounds", segment.Index)
				}
			} else {
				return nil, fmt.Errorf("invalid record path: expected array for index %d", segment.Index)
			}
		case "parsed":
			// Handle (parsed) JSON strings
			if strVal, ok := current.(string); ok {
				var parsedData interface{}
				if err := json.Unmarshal([]byte(strVal), &parsedData); err != nil {
					return nil, fmt.Errorf("invalid record path: failed to parse JSON string: %w", err)
				}
				current = parsedData
			} else {
				return nil, fmt.Errorf("invalid record path: expected string for (parsed) segment")
			}
		}
	}

	// Final result should be an array
	if arr, ok := current.([]interface{}); ok {
		return arr, nil
	}

	return nil, fmt.Errorf("invalid record path: final result is not an array")
}

// PathSegment represents a segment of a JSON path
type PathSegment struct {
	Type  string // "object", "array", or "parsed"
	Key   string // for object access
	Index int    // for array access
}

// parsePathSegments parses a path string into segments
// Handles paths like: "[0].text(parsed).totalDataChart"
func parsePathSegments(path string) []PathSegment {
	var segments []PathSegment
	i := 0

	for i < len(path) {
		if path[i] == '[' {
			// Array index
			j := strings.Index(path[i:], "]")
			if j == -1 {
				break
			}
			indexStr := path[i+1 : i+j]
			if index := parseIndex(indexStr); index >= 0 {
				segments = append(segments, PathSegment{
					Type:  "array",
					Index: index,
				})
			}
			i += j + 1
			// Skip dot after ]
			if i < len(path) && path[i] == '.' {
				i++
			}
		} else if strings.HasPrefix(path[i:], "(parsed)") {
			// Parsed JSON segment
			segments = append(segments, PathSegment{
				Type: "parsed",
			})
			i += 8 // length of "(parsed)"
			// Skip dot after (parsed)
			if i < len(path) && path[i] == '.' {
				i++
			}
		} else {
			// Object key
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' && !strings.HasPrefix(path[i:], "(parsed)") {
				i++
			}
			if i > start {
				key := path[start:i]
				segments = append(segments, PathSegment{
					Type: "object",
					Key:  key,
				})
			}
			// Skip dot
			if i < len(path) && path[i] == '.' {
				i++
			}
		}
	}

	return segments
}

// parseIndex safely parses a string to an integer index
func parseIndex(s string) int {
	if index, err := strconv.Atoi(s); err == nil && index >= 0 {
		return index
	}
	return -1
}
