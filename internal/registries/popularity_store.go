package registries

import (
	"encoding/json"
	"fmt"

	"go.etcd.io/bbolt"
)

// popularityBucketName is the dedicated bbolt bucket for the popularity
// cache (Spec 110 FR-008, plan.md R4: cache.Manager does not fit — fixed 2h
// TTL, deletes expired records, and Spec 105 authorization frames that are
// irrelevant here).
const popularityBucketName = "catalog_popularity"

// popularityStore persists starsEntry records to popularityBucketName. A nil
// *popularityStore (never constructed) means memory-only, which is what the
// CLI in-process fallback uses (plan.md wiring table).
type popularityStore struct {
	db *bbolt.DB
}

// newPopularityStore opens (creating if needed) the popularity bucket on an
// already-open bbolt database.
func newPopularityStore(db *bbolt.DB) (*popularityStore, error) {
	if db == nil {
		return nil, fmt.Errorf("catalog popularity store: nil db")
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(popularityBucketName))
		return err
	}); err != nil {
		return nil, fmt.Errorf("catalog popularity store: create bucket: %w", err)
	}
	return &popularityStore{db: db}, nil
}

// all returns every decodable persisted entry. The provider loads the whole
// bucket once at construction so the FR-008 key cap applies to what is on
// disk, not just to what a lazy lookup happened to pull into memory — with
// lazy loading, a restart reset the in-memory count to zero and the bucket
// could grow past the cap forever.
func (s *popularityStore) all() map[string]*starsEntry {
	out := make(map[string]*starsEntry)
	_ = s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(popularityBucketName))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var entry starsEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				return nil //nolint:nilerr // a corrupt record is skipped, never fatal
			}
			out[string(k)] = &entry
			return nil
		})
	})
	return out
}

// get returns the persisted entry for key, or ok=false if there is none (or
// it fails to decode — treated the same as absent rather than as an error,
// since a corrupt single record must never break the whole cache).
func (s *popularityStore) get(key string) (*starsEntry, bool) {
	var entry starsEntry
	found := false
	_ = s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(popularityBucketName))
		if b == nil {
			return nil
		}
		v := b.Get([]byte(key))
		if v == nil {
			return nil
		}
		if err := json.Unmarshal(v, &entry); err != nil {
			return nil //nolint:nilerr // a corrupt record is treated as absent, not fatal
		}
		found = true
		return nil
	})
	if !found {
		return nil, false
	}
	return &entry, true
}

// put persists entry under key, creating the bucket if a prior open somehow
// missed it (defensive; newPopularityStore already creates it).
func (s *popularityStore) put(key string, entry *starsEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(popularityBucketName))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), data)
	})
}

// delete removes key (used by cap eviction — FR-008: "evicting the oldest
// fetched_at first").
func (s *popularityStore) delete(key string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(popularityBucketName))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
}
