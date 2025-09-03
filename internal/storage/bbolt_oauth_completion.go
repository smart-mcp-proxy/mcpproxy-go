package storage

import (
	"fmt"
	"time"

	"go.etcd.io/bbolt"
)

// SaveOAuthCompletionEvent saves an OAuth completion event to the database
func (s *BoltDB) SaveOAuthCompletionEvent(event *OAuthCompletionEvent) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(OAuthCompletionBucket))
		if err != nil {
			return fmt.Errorf("failed to create oauth completion bucket: %w", err)
		}

		key := fmt.Sprintf("%s_%d", event.ServerName, event.CompletedAt.Unix())
		data, err := event.MarshalBinary()
		if err != nil {
			return fmt.Errorf("failed to marshal oauth completion event: %w", err)
		}

		return bucket.Put([]byte(key), data)
	})
}

// GetUnprocessedOAuthCompletionEvents returns all unprocessed OAuth completion events
func (s *BoltDB) GetUnprocessedOAuthCompletionEvents() ([]*OAuthCompletionEvent, error) {
	var events []*OAuthCompletionEvent

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthCompletionBucket))
		if bucket == nil {
			return nil // No events yet
		}

		return bucket.ForEach(func(k, v []byte) error {
			event := &OAuthCompletionEvent{}
			if err := event.UnmarshalBinary(v); err != nil {
				return err
			}
			
			// Only return unprocessed events
			if event.ProcessedAt == nil {
				events = append(events, event)
			}
			return nil
		})
	})

	return events, err
}

// MarkOAuthCompletionEventProcessed marks an OAuth completion event as processed
func (s *BoltDB) MarkOAuthCompletionEventProcessed(serverName string, completedAt time.Time) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthCompletionBucket))
		if bucket == nil {
			return fmt.Errorf("oauth completion bucket not found")
		}

		key := fmt.Sprintf("%s_%d", serverName, completedAt.Unix())
		data := bucket.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("oauth completion event not found")
		}

		event := &OAuthCompletionEvent{}
		if err := event.UnmarshalBinary(data); err != nil {
			return err
		}

		now := time.Now()
		event.ProcessedAt = &now

		updatedData, err := event.MarshalBinary()
		if err != nil {
			return err
		}

		return bucket.Put([]byte(key), updatedData)
	})
}

// CleanupOldOAuthCompletionEvents removes OAuth completion events older than 24 hours
func (s *BoltDB) CleanupOldOAuthCompletionEvents() error {
	cutoff := time.Now().Add(-24 * time.Hour)
	
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthCompletionBucket))
		if bucket == nil {
			return nil
		}

		var keysToDelete [][]byte

		// Collect keys to delete
		err := bucket.ForEach(func(k, v []byte) error {
			event := &OAuthCompletionEvent{}
			if err := event.UnmarshalBinary(v); err != nil {
				s.logger.Errorf("Failed to unmarshal event during cleanup: %v", err)
				return nil // Continue cleanup
			}

			if event.CompletedAt.Before(cutoff) {
				keysToDelete = append(keysToDelete, k)
			}
			return nil
		})

		if err != nil {
			return err
		}

		// Delete old events
		for _, key := range keysToDelete {
			if err := bucket.Delete(key); err != nil {
				s.logger.Errorf("Failed to delete old OAuth completion event: %v", err)
			}
		}

		return nil
	})
}