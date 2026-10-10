package storage

import (
	"time"

	"go.etcd.io/bbolt"
)

// ToolBaselineDecisionsBucket records, per server, that an operator approved
// the server's tool snapshot (a security / server approval). Key: server
// name; value: RFC 3339 time of the decision.
//
// Tool-level quarantine otherwise infers "this server already has a tool
// baseline" only from approved or changed approval records, so a server
// approved with an EMPTY reviewed inventory left no evidence of the decision:
// the first tool it later served was auto-approved as a first trusted
// baseline instead of being held for review (UX-02 cross-review r5). The
// marker is that evidence. Discovery records it too, the first time a pass
// leaves an approved or changed record (a trusted auto-baseline included), so
// the baseline outlives the removal of its last approved record (UX-02
// cross-review r6). It is removed with the server's approval records.
const ToolBaselineDecisionsBucket = "tool_baseline_decisions"

// MarkToolBaselineDecided records that serverName's tool baseline was decided
// (an operator approval, or a baseline established by discovery). Idempotent.
func (b *BoltDB) MarkToolBaselineDecided(serverName string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(ToolBaselineDecisionsBucket))
		if err != nil {
			return err
		}
		return bucket.Put([]byte(serverName), []byte(time.Now().UTC().Format(time.RFC3339Nano)))
	})
}

// ToolBaselineDecided reports whether MarkToolBaselineDecided was recorded
// for serverName (and not since removed with its approval records).
func (b *BoltDB) ToolBaselineDecided(serverName string) (bool, error) {
	decided := false
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolBaselineDecisionsBucket))
		decided = bucket != nil && bucket.Get([]byte(serverName)) != nil
		return nil
	})
	return decided, err
}

// deleteToolBaselineDecisions removes the markers of the named servers
// inside tx (no-op when the bucket does not exist yet).
func deleteToolBaselineDecisions(tx *bbolt.Tx, keep func(server string) bool) error {
	bucket := tx.Bucket([]byte(ToolBaselineDecisionsBucket))
	if bucket == nil {
		return nil
	}
	var drop [][]byte
	if err := bucket.ForEach(func(k, _ []byte) error {
		if !keep(string(k)) {
			drop = append(drop, append([]byte(nil), k...))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, k := range drop {
		if err := bucket.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

// MarkToolBaselineDecided records that serverName's tool baseline was decided
// by an operator (server) approval; see ToolBaselineDecisionsBucket.
func (m *Manager) MarkToolBaselineDecided(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.MarkToolBaselineDecided(serverName)
}

// ToolBaselineDecided reports whether serverName's tool baseline was decided
// by an operator (server) approval.
func (m *Manager) ToolBaselineDecided(serverName string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db.ToolBaselineDecided(serverName)
}
