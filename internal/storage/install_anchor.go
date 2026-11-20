package storage

import (
	stderrors "errors"
	"fmt"
	"time"

	"go.etcd.io/bbolt"
)

// InstallAnchorBucket / InstallAnchorKey hold the install anchor (Spec 126
// FR-033): the UTC time of the first core start over a data store that held no
// prior data. It lives in its own bucket, never inside OnboardingState: older
// binaries round-trip the whole OnboardingState record and would erase a field
// they do not know on a downgrade. Only EnsureInstallAnchor writes it; it is
// written once, never rewritten, and recorded whatever the telemetry opt-out
// setting (only sending is gated). The value is an RFC 3339 UTC timestamp and
// never leaves the machine: the heartbeat carries only day buckets derived
// from it.
const (
	InstallAnchorBucket = "install_anchor"
	InstallAnchorKey    = "first_run_at"
)

// errStoreHasRecords ends the walk in storeHasRecords at the first record; it
// never escapes the View.
var errStoreHasRecords = stderrors.New("store has records")

// storeHasRecords reports whether the opened database holds at least one
// record (a key) in any bucket, nested buckets included. Buckets that exist but
// are empty — for example created by a standalone CLI command run before the
// first serve — are not data, and neither is the meta schema-version key that
// every open writes into a new store (initBuckets). A read failure counts as
// "has records" (fail closed: an unreadable store is never a new install).
func storeHasRecords(db *bbolt.DB) bool {
	err := db.View(func(tx *bbolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bbolt.Bucket) error {
			return bucketHasRecords(b, string(name) == MetaBucket)
		})
	})
	if err == nil {
		return false
	}
	return true // errStoreHasRecords, or a read failure (fail closed)
}

// bucketHasRecords returns errStoreHasRecords when b, or a bucket nested in
// it, holds a key. isMeta skips the schema-version key of the meta bucket.
func bucketHasRecords(b *bbolt.Bucket, isMeta bool) error {
	return b.ForEach(func(k, v []byte) error {
		if v == nil { // nested bucket
			nested := b.Bucket(k)
			if nested == nil {
				return errStoreHasRecords
			}
			return bucketHasRecords(nested, false)
		}
		if isMeta && string(k) == SchemaVersionKey {
			return nil
		}
		return errStoreHasRecords
	})
}

// WasEmptyAtOpen reports whether config.db held no prior data when this
// process opened it: the file was absent, 0 bytes long, or held no record in
// any bucket (empty buckets and the meta schema-version key do not count).
// It is decided once, in NewBoltDB, before any bucket is created.
func (b *BoltDB) WasEmptyAtOpen() bool {
	return b.wasEmptyAtOpen
}

// EnsureInstallAnchor records now (as UTC) as the install anchor when the
// store was empty at open and holds no anchor yet, and returns the anchor the
// store holds afterwards. An existing store without an anchor stays without
// one (ok=false): an install upgraded from a build without the field is never
// counted as new. An anchor, once written, is never rewritten — a second call,
// or a later start, returns the first one. Concurrent callers are serialized by
// the bbolt write transaction and all see the same anchor.
func (b *BoltDB) EnsureInstallAnchor(now time.Time) (time.Time, bool, error) {
	if !b.wasEmptyAtOpen {
		return b.InstallAnchor()
	}
	var anchor time.Time
	err := b.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(InstallAnchorBucket))
		if err != nil {
			return err
		}
		if raw := bucket.Get([]byte(InstallAnchorKey)); raw != nil {
			anchor, err = decodeInstallAnchor(raw)
			return err
		}
		anchor = now.UTC()
		return bucket.Put([]byte(InstallAnchorKey), []byte(anchor.Format(time.RFC3339Nano)))
	})
	if err != nil {
		return time.Time{}, false, err
	}
	return anchor, true, nil
}

// InstallAnchor returns the recorded install anchor. ok is false when the
// store has none (an existing install). A stored value that does not parse is
// an error, never a missing anchor.
func (b *BoltDB) InstallAnchor() (time.Time, bool, error) {
	var (
		anchor time.Time
		ok     bool
	)
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(InstallAnchorBucket))
		if bucket == nil {
			return nil
		}
		raw := bucket.Get([]byte(InstallAnchorKey))
		if raw == nil {
			return nil
		}
		t, err := decodeInstallAnchor(raw)
		if err != nil {
			return err
		}
		anchor, ok = t, true
		return nil
	})
	if err != nil {
		return time.Time{}, false, err
	}
	return anchor, ok, nil
}

func decodeInstallAnchor(raw []byte) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("install anchor: %w", err)
	}
	return t.UTC(), nil
}

// WasEmptyAtOpen reports whether config.db held no prior data when this
// process opened it (Spec 126 FR-033, FR-036).
func (m *Manager) WasEmptyAtOpen() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return false
	}
	return m.db.WasEmptyAtOpen()
}

// EnsureInstallAnchor records the install anchor when the store was empty at
// open (Spec 126 FR-033). See BoltDB.EnsureInstallAnchor.
func (m *Manager) EnsureInstallAnchor(now time.Time) (time.Time, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return time.Time{}, false, fmt.Errorf("storage not open")
	}
	return m.db.EnsureInstallAnchor(now)
}

// InstallAnchor returns the recorded install anchor, if any.
func (m *Manager) InstallAnchor() (time.Time, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return time.Time{}, false, fmt.Errorf("storage not open")
	}
	return m.db.InstallAnchor()
}
