package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Spec 101 US1 (FR-009, FR-010, FR-011a, SC-010): durable anti-downgrade state
// of the signed TPA bundle.
//
// Layout of TPABundleStateBucket:
//
//	"state"            -> JSON scanner.TPABundleState
//	"history" (bucket) -> big-endian uint64 NextSequence -> JSON scanner.TPABundleActivation
//
// The bucket is created lazily by the first write and NEVER by a read, so an
// install that never activates an external bundle keeps a config.db that an
// older binary reads unchanged. The state and the history row of the activation
// that produced it are written in ONE transaction: a crash can never pair a new
// active bundle with an old watermark.

const (
	tpaBundleStateKey      = "state"
	tpaBundleHistoryBucket = "history"
)

var _ scanner.TPABundleStateStore = (*Manager)(nil)

// LoadTPABundleState returns the persisted state, or (nil, nil) when none has
// ever been written.
func (m *Manager) LoadTPABundleState() (*scanner.TPABundleState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db.LoadTPABundleState()
}

// CommitTPABundleState writes state and, when entry is non-nil, appends it to
// the activation history in one transaction.
func (m *Manager) CommitTPABundleState(state *scanner.TPABundleState, entry *scanner.TPABundleActivation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.CommitTPABundleState(state, entry)
}

// ListTPABundleHistory returns every activation row, oldest first.
func (m *Manager) ListTPABundleHistory() ([]scanner.TPABundleActivation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db.ListTPABundleHistory()
}

// LoadTPABundleState is the BoltDB form of Manager.LoadTPABundleState. A
// missing bucket or key reads (nil, nil); a value that cannot be decoded is an
// error so callers fail safe instead of treating corruption as "no state".
func (b *BoltDB) LoadTPABundleState() (*scanner.TPABundleState, error) {
	var state *scanner.TPABundleState
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(TPABundleStateBucket))
		if bucket == nil {
			return nil
		}
		raw := bucket.Get([]byte(tpaBundleStateKey))
		if raw == nil {
			return nil
		}
		var s scanner.TPABundleState
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("decode tpa bundle state: %w", err)
		}
		state = &s
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

// CommitTPABundleState is the BoltDB form of Manager.CommitTPABundleState. Both
// values are marshalled BEFORE the bucket is touched, and any error aborts the
// whole transaction, so neither the state nor the history row is written on
// failure.
func (b *BoltDB) CommitTPABundleState(state *scanner.TPABundleState, entry *scanner.TPABundleActivation) error {
	if state == nil {
		return fmt.Errorf("commit tpa bundle state: nil state")
	}
	// Stamp on a copy so a failed commit does not mutate the caller's value.
	stamped := *state
	if stamped.SchemaVersion == 0 {
		stamped.SchemaVersion = scanner.TPABundleStateSchemaVersion
	}
	stateRaw, err := json.Marshal(&stamped)
	if err != nil {
		return fmt.Errorf("encode tpa bundle state: %w", err)
	}
	var entryRaw []byte
	if entry != nil {
		if entryRaw, err = json.Marshal(entry); err != nil {
			return fmt.Errorf("encode tpa bundle activation: %w", err)
		}
	}

	if err := b.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(TPABundleStateBucket))
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte(tpaBundleStateKey), stateRaw); err != nil {
			return err
		}
		if entryRaw == nil {
			return nil
		}
		history, err := bucket.CreateBucketIfNotExists([]byte(tpaBundleHistoryBucket))
		if err != nil {
			return err
		}
		seq, err := history.NextSequence()
		if err != nil {
			return err
		}
		var key [8]byte
		binary.BigEndian.PutUint64(key[:], seq)
		return history.Put(key[:], entryRaw)
	}); err != nil {
		return err
	}
	// Reflect the stamped version back only after the commit succeeded.
	state.SchemaVersion = stamped.SchemaVersion
	return nil
}

// ListTPABundleHistory is the BoltDB form of Manager.ListTPABundleHistory.
// Big-endian sequence keys make cursor order the append order.
func (b *BoltDB) ListTPABundleHistory() ([]scanner.TPABundleActivation, error) {
	var out []scanner.TPABundleActivation
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(TPABundleStateBucket))
		if bucket == nil {
			return nil
		}
		history := bucket.Bucket([]byte(tpaBundleHistoryBucket))
		if history == nil {
			return nil
		}
		return history.ForEach(func(_, v []byte) error {
			var a scanner.TPABundleActivation
			if err := json.Unmarshal(v, &a); err != nil {
				return fmt.Errorf("decode tpa bundle activation: %w", err)
			}
			out = append(out, a)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
