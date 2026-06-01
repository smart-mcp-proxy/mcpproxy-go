package storage

import (
	"fmt"

	"go.etcd.io/bbolt"
)

// Spec 069 A2: persistence for the actor-owned usage aggregate.
//
// The aggregate itself lives in the runtime layer (it folds in domain events);
// storage only provides byte-blob persistence under a schema-versioned key plus
// a single full-scan helper used to rebuild the aggregate on a cold start with
// no persisted snapshot. Keeping these byte-oriented avoids a storage->runtime
// import cycle: the runtime owns encoding/decoding of the snapshot shape.

const (
	// ActivityStatsBucket holds derived activity statistics (usage aggregate
	// snapshot). Separate from the raw ActivityRecordsBucket.
	ActivityStatsBucket = "activity_stats"

	// UsageSnapshotKey is the schema-versioned key for the persisted usage
	// aggregate. Bumping the version suffix forces a clean rebuild rather than
	// mis-decoding an incompatible older shape.
	UsageSnapshotKey = "usage_aggregate_v1"
)

// SaveUsageSnapshot persists the encoded usage aggregate under the versioned
// key, overwriting any previous snapshot.
func (m *Manager) SaveUsageSnapshot(data []byte) error {
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(ActivityStatsBucket))
		if err != nil {
			return fmt.Errorf("create activity_stats bucket: %w", err)
		}
		// Copy: bbolt requires the value to remain valid for the put only;
		// callers may reuse their buffer afterwards.
		buf := make([]byte, len(data))
		copy(buf, data)
		return bucket.Put([]byte(UsageSnapshotKey), buf)
	})
}

// LoadUsageSnapshot returns the persisted usage aggregate bytes, or (nil, nil)
// when no snapshot has been written yet (cold start). The returned slice is a
// copy safe to use outside the transaction.
func (m *Manager) LoadUsageSnapshot() ([]byte, error) {
	var out []byte
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ActivityStatsBucket))
		if bucket == nil {
			return nil
		}
		v := bucket.Get([]byte(UsageSnapshotKey))
		if v == nil {
			return nil
		}
		out = make([]byte, len(v))
		copy(out, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ScanAllActivities performs a single pass over every activity record and calls
// visit for each. Used to rebuild the usage aggregate on a cold start (reuses
// the same cursor pattern as AggregateToolUsage). An empty/absent bucket yields
// zero visits and no error. Filtering by record type is the caller's job.
func (m *Manager) ScanAllActivities(visit func(*ActivityRecord)) error {
	return m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ActivityRecordsBucket))
		if bucket == nil {
			return nil
		}
		cursor := bucket.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			var record ActivityRecord
			if err := record.UnmarshalBinary(v); err != nil {
				m.logger.Warnw("Failed to unmarshal activity record during usage rebuild",
					"key", string(k), "error", err)
				continue
			}
			visit(&record)
		}
		return nil
	})
}
