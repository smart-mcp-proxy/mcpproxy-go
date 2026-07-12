package storage

import (
	"encoding/json"
	"fmt"

	"go.etcd.io/bbolt"
)

// migrateLegacySessions moves MCP session records out of the shared "sessions"
// bucket and into the namespaced one.
//
// Both editions used a bucket literally named "sessions" on the same database:
// the core for MCP transport/work sessions, the server edition for USER LOGIN
// sessions. Each side swept the bucket believing it owned every key, so they
// deleted each other's data — core's retention evicted user logins, and the
// server edition's expiry sweep deleted MCP records (they carry no expires_at,
// so a zero time read as long expired).
//
// This moves only the records that are unmistakably ours and leaves everything
// else exactly where it is. Auth sessions are never touched, so nobody is logged
// out by the upgrade. Idempotent, and safe to run on a personal-edition database
// where the legacy bucket contains nothing but MCP records.
func migrateLegacySessions(tx *bbolt.Tx) error {
	legacy := tx.Bucket([]byte(LegacySessionsBucket))
	if legacy == nil {
		return nil // fresh database, or already migrated
	}

	target, err := tx.CreateBucketIfNotExists([]byte(SessionsBucket))
	if err != nil {
		return fmt.Errorf("failed to create %s bucket: %w", SessionsBucket, err)
	}

	type movedEntry struct {
		key   []byte
		value []byte
	}
	var moved []movedEntry

	if err := legacy.ForEach(func(k, v []byte) error {
		if !isMCPSessionRecord(v) {
			return nil // not ours — a user login session, or something unknown
		}
		// Copy: the key and value are only valid for the life of the iteration.
		key := make([]byte, len(k))
		copy(key, k)
		val := make([]byte, len(v))
		copy(val, v)
		moved = append(moved, movedEntry{key: key, value: val})
		return nil
	}); err != nil {
		return fmt.Errorf("failed to scan legacy sessions bucket: %w", err)
	}

	for _, e := range moved {
		if err := target.Put(e.key, e.value); err != nil {
			return fmt.Errorf("failed to move session record: %w", err)
		}
		if err := legacy.Delete(e.key); err != nil {
			return fmt.Errorf("failed to remove legacy session record: %w", err)
		}
	}

	// Only drop the legacy bucket once it is genuinely empty. In the server
	// edition it still holds user login sessions and MUST survive.
	//
	// Emptiness is checked with a cursor, not Stats(): Stats() reports page-level
	// counts that do not reflect deletions made inside this same transaction, so
	// it would still report the records we just moved out.
	if k, _ := legacy.Cursor().First(); k == nil {
		if err := tx.DeleteBucket([]byte(LegacySessionsBucket)); err != nil {
			return fmt.Errorf("failed to delete empty legacy sessions bucket: %w", err)
		}
	}

	return nil
}

// isMCPSessionRecord reports whether a stored value is one of OUR session
// records, as opposed to a server-edition user login session sharing the bucket.
//
// The two are told apart by the fields only one of them has. A user login session
// carries user_id / expires_at / bearer_token; an MCP session record carries
// start_time and status and none of those. We require the positive signal AND the
// absence of the negative one, so an unrecognised value is left alone rather than
// moved or deleted on a guess.
func isMCPSessionRecord(value []byte) bool {
	var probe struct {
		// Ours.
		StartTime *string `json:"start_time"`
		Status    *string `json:"status"`
		// Theirs — the presence of any of these means hands off.
		UserID      *string `json:"user_id"`
		ExpiresAt   *string `json:"expires_at"`
		BearerToken *string `json:"bearer_token"`
	}
	if err := json.Unmarshal(value, &probe); err != nil {
		return false // unparseable: not ours to move
	}

	if probe.UserID != nil || probe.ExpiresAt != nil || probe.BearerToken != nil {
		return false // a user login session
	}
	return probe.StartTime != nil && probe.Status != nil
}
