package storage

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"go.etcd.io/bbolt"
)

// DefaultMaxResponseSize is the default maximum size for response truncation (64KB)
const DefaultMaxResponseSize = 64 * 1024

// activityKey generates a BBolt key for an activity record.
// Key format: {timestamp_ns}_{ulid} for natural reverse-chronological ordering.
// Using 20-digit nanosecond timestamp ensures consistent ordering.
func activityKey(timestamp time.Time, id string) []byte {
	return []byte(fmt.Sprintf("%020d_%s", timestamp.UnixNano(), id))
}

// parseActivityKey extracts the ULID from an activity key.
// Returns empty string if key format is invalid.
func parseActivityKey(key []byte) string {
	keyStr := string(key)
	// Key format: {20-digit timestamp}_{ulid}
	if len(keyStr) < 22 { // 20 digits + underscore + at least 1 char for id
		return ""
	}
	return keyStr[21:]
}

// truncateResponse truncates a response string if it exceeds maxSize.
// Returns the (potentially truncated) string and whether truncation occurred.
func truncateResponse(response string, maxSize int) (string, bool) {
	if maxSize <= 0 {
		maxSize = DefaultMaxResponseSize
	}
	if len(response) <= maxSize {
		return response, false
	}
	return response[:maxSize] + "...[truncated]", true
}

// SaveActivity stores an activity record in BBolt.
// The record is stored with a composite key for efficient time-based queries.
func (m *Manager) SaveActivity(record *ActivityRecord) error {
	if record == nil {
		return fmt.Errorf("activity record cannot be nil")
	}

	// Generate ID if not set
	if record.ID == "" {
		record.ID = ulid.Make().String()
	}

	// Set timestamp if not set
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(ActivityRecordsBucket))
		if err != nil {
			return fmt.Errorf("failed to create activity bucket: %w", err)
		}

		data, err := record.MarshalBinary()
		if err != nil {
			return fmt.Errorf("failed to marshal activity record: %w", err)
		}

		key := activityKey(record.Timestamp, record.ID)
		if err := bucket.Put(key, data); err != nil {
			return fmt.Errorf("failed to store activity record: %w", err)
		}

		return nil
	})
}

// GetActivity retrieves an activity record by ID.
// Returns nil if the record is not found.
func (m *Manager) GetActivity(id string) (*ActivityRecord, error) {
	if id == "" {
		return nil, fmt.Errorf("activity ID cannot be empty")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var record *ActivityRecord

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ActivityRecordsBucket))
		if bucket == nil {
			return nil // No activities yet
		}

		// Scan to find the record by ID (ID is in the key suffix)
		cursor := bucket.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			if parseActivityKey(k) == id {
				record = &ActivityRecord{}
				if err := record.UnmarshalBinary(v); err != nil {
					return fmt.Errorf("failed to unmarshal activity record: %w", err)
				}
				return nil
			}
		}

		return nil // Not found
	})

	if err != nil {
		return nil, err
	}

	return record, nil
}

// ListActivities returns paginated activity records matching the filter.
// Records are returned in reverse chronological order (newest first).
// Returns the records, total matching count, and any error.
func (m *Manager) ListActivities(filter ActivityFilter) ([]*ActivityRecord, int, error) {
	filter.Validate()

	m.mu.RLock()
	defer m.mu.RUnlock()

	var records []*ActivityRecord
	var total int

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ActivityRecordsBucket))
		if bucket == nil {
			return nil // No activities yet
		}

		// Iterate in reverse order (newest first)
		cursor := bucket.Cursor()
		skipped := 0

		for k, v := cursor.Last(); k != nil; k, v = cursor.Prev() {
			var record ActivityRecord
			if err := record.UnmarshalBinary(v); err != nil {
				m.logger.Warnw("Failed to unmarshal activity record",
					"key", string(k),
					"error", err)
				continue
			}

			// Check if record matches filter
			if !filter.Matches(&record) {
				continue
			}

			total++

			// Handle pagination
			if skipped < filter.Offset {
				skipped++
				continue
			}

			if len(records) < filter.Limit {
				records = append(records, &ActivityRecord{
					ID:                record.ID,
					Type:              record.Type,
					ServerName:        record.ServerName,
					ToolName:          record.ToolName,
					Arguments:         record.Arguments,
					Response:          record.Response,
					ResponseTruncated: record.ResponseTruncated,
					Status:            record.Status,
					ErrorMessage:      record.ErrorMessage,
					DurationMs:        record.DurationMs,
					Timestamp:         record.Timestamp,
					SessionID:         record.SessionID,
					RequestID:         record.RequestID,
					Metadata:          record.Metadata,
				})
			}
		}

		return nil
	})

	return records, total, err
}

// DeleteActivity deletes an activity record by ID.
// Returns nil if the record doesn't exist.
func (m *Manager) DeleteActivity(id string) error {
	if id == "" {
		return fmt.Errorf("activity ID cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ActivityRecordsBucket))
		if bucket == nil {
			return nil // No activities yet
		}

		// Find and delete the record by ID
		cursor := bucket.Cursor()
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			if parseActivityKey(k) == id {
				return bucket.Delete(k)
			}
		}

		return nil // Not found, not an error
	})
}

// CountActivities returns the total number of activity records.
func (m *Manager) CountActivities() (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var count int

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ActivityRecordsBucket))
		if bucket == nil {
			return nil
		}
		count = bucket.Stats().KeyN
		return nil
	})

	return count, err
}

// StreamActivities returns a channel that yields activity records matching the filter.
// The channel is closed when all matching records have been sent.
// This is useful for streaming large exports without loading all records into memory.
func (m *Manager) StreamActivities(filter ActivityFilter) <-chan *ActivityRecord {
	filter.Validate()
	ch := make(chan *ActivityRecord, 100)

	go func() {
		defer close(ch)

		m.mu.RLock()
		defer m.mu.RUnlock()

		err := m.db.db.View(func(tx *bbolt.Tx) error {
			bucket := tx.Bucket([]byte(ActivityRecordsBucket))
			if bucket == nil {
				return nil
			}

			cursor := bucket.Cursor()
			for k, v := cursor.Last(); k != nil; k, v = cursor.Prev() {
				var record ActivityRecord
				if err := record.UnmarshalBinary(v); err != nil {
					continue
				}

				if !filter.Matches(&record) {
					continue
				}

				ch <- &record
			}

			return nil
		})

		if err != nil {
			m.logger.Errorw("Error streaming activities", "error", err)
		}
	}()

	return ch
}

// PruneOldActivities deletes activity records older than the specified duration.
// Returns the number of records deleted.
func (m *Manager) PruneOldActivities(maxAge time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	cutoffKey := activityKey(cutoff, "")

	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted int

	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ActivityRecordsBucket))
		if bucket == nil {
			return nil
		}

		var keysToDelete [][]byte
		cursor := bucket.Cursor()

		// Keys before cutoff (older records have smaller keys)
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			// Compare keys lexicographically
			if string(k) < string(cutoffKey) {
				keysToDelete = append(keysToDelete, append([]byte{}, k...))
			} else {
				break // Keys are sorted, no more old records
			}
		}

		for _, key := range keysToDelete {
			if err := bucket.Delete(key); err != nil {
				return fmt.Errorf("failed to delete old activity: %w", err)
			}
			deleted++
		}

		return nil
	})

	if err != nil {
		return deleted, err
	}

	if deleted > 0 {
		m.logger.Infow("Pruned old activity records",
			"deleted", deleted,
			"max_age", maxAge.String())
	}

	return deleted, nil
}

// PruneExcessActivities deletes oldest records when count exceeds maxRecords.
// Deletes records until count is at targetPercent of maxRecords (default 90%).
// Returns the number of records deleted.
func (m *Manager) PruneExcessActivities(maxRecords int, targetPercent float64) (int, error) {
	if targetPercent <= 0 || targetPercent > 1 {
		targetPercent = 0.9 // Default to 90%
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted int

	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ActivityRecordsBucket))
		if bucket == nil {
			return nil
		}

		count := bucket.Stats().KeyN
		if count <= maxRecords {
			return nil
		}

		targetCount := int(float64(maxRecords) * targetPercent)
		toDelete := count - targetCount

		var keysToDelete [][]byte
		cursor := bucket.Cursor()

		// Delete oldest records (smallest keys)
		for k, _ := cursor.First(); k != nil && len(keysToDelete) < toDelete; k, _ = cursor.Next() {
			keysToDelete = append(keysToDelete, append([]byte{}, k...))
		}

		for _, key := range keysToDelete {
			if err := bucket.Delete(key); err != nil {
				return fmt.Errorf("failed to delete excess activity: %w", err)
			}
			deleted++
		}

		return nil
	})

	if err != nil {
		return deleted, err
	}

	if deleted > 0 {
		m.logger.Infow("Pruned excess activity records",
			"deleted", deleted,
			"max_records", maxRecords)
	}

	return deleted, nil
}

// SaveActivityAsync saves an activity record asynchronously.
// This is non-blocking and suitable for recording tool calls without impacting latency.
func (m *Manager) SaveActivityAsync(record *ActivityRecord) {
	go func() {
		if err := m.SaveActivity(record); err != nil {
			m.logger.Errorw("Failed to save activity record async",
				"id", record.ID,
				"type", record.Type,
				"error", err)
		}
	}()
}

// GetActivityByIDScan performs a full scan to find activity by ID.
// This is less efficient than GetActivity but works when the timestamp is unknown.
func (m *Manager) GetActivityByIDScan(id string) (*ActivityRecord, error) {
	return m.GetActivity(id) // Our GetActivity already does a scan
}

// TruncateActivityResponse is a helper to truncate responses for storage.
func TruncateActivityResponse(response string, maxSize int) (string, bool) {
	return truncateResponse(response, maxSize)
}

// ActivityRecordFromJSON parses an activity record from JSON bytes.
func ActivityRecordFromJSON(data []byte) (*ActivityRecord, error) {
	var record ActivityRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("failed to parse activity record: %w", err)
	}
	return &record, nil
}

// UpdateActivityMetadata updates the metadata of an existing activity record.
// This is used for async updates like sensitive data detection results.
// The updates map is merged into the existing metadata (existing keys are preserved unless overwritten).
func (m *Manager) UpdateActivityMetadata(id string, updates map[string]interface{}) error {
	if id == "" {
		return fmt.Errorf("activity ID cannot be empty")
	}
	if len(updates) == 0 {
		return nil // Nothing to update
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ActivityRecordsBucket))
		if bucket == nil {
			return fmt.Errorf("activity bucket not found")
		}

		// Find the record by ID
		cursor := bucket.Cursor()
		var key []byte
		var record *ActivityRecord

		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			if parseActivityKey(k) == id {
				key = k
				record = &ActivityRecord{}
				if err := record.UnmarshalBinary(v); err != nil {
					return fmt.Errorf("failed to unmarshal activity record: %w", err)
				}
				break
			}
		}

		if record == nil {
			return fmt.Errorf("activity record not found: %s", id)
		}

		// Merge updates into existing metadata
		if record.Metadata == nil {
			record.Metadata = make(map[string]interface{})
		}
		for k, v := range updates {
			record.Metadata[k] = v
		}

		// Save updated record
		data, err := record.MarshalBinary()
		if err != nil {
			return fmt.Errorf("failed to marshal updated activity record: %w", err)
		}

		return bucket.Put(key, data)
	})
}
