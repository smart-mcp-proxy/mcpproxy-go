package storage

import (
	"encoding/json"
	"fmt"
	"time"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 PR-1 (#1567): the durable per-server tool baseline marker.
//
// Whether a trusted server "has a baseline" used to be inferred only from the
// presence of an approved or changed tool-approval record. Those records are
// deleted when the upstream replaces its inventory or returns an
// authoritative empty list, and the next discovery pass then treated the
// server as new and auto-approved whatever it listed. The marker records the
// decision itself, so it survives the loss of the records. It is removed only
// together with the server's approval records (server removal, orphan prune).

// MarkToolBaselineDecided records that serverName's tool baseline was decided.
// It is idempotent: the first DecidedAt is kept, and the source (and by) of an
// existing marker are replaced only when the stored one was inferred and the
// new one is explicit. A zero DecidedAt is stamped with the current time.
func (m *Manager) MarkToolBaselineDecided(serverName string, d toolreview.BaselineDecision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.MarkToolBaselineDecided(serverName, d)
}

// ToolBaselineDecided reports whether serverName has a baseline marker. A
// stored value that cannot be decoded counts as decided (fail closed). A store
// without the bucket (written by an older binary) reads false.
func (m *Manager) ToolBaselineDecided(serverName string) (bool, error) {
	_, ok, err := m.GetToolBaselineDecision(serverName)
	return ok, err
}

// GetToolBaselineDecision returns serverName's marker. ok is true when a value
// is stored, including one that cannot be decoded (then d carries only
// Source "inferred").
func (m *Manager) GetToolBaselineDecision(serverName string) (d toolreview.BaselineDecision, ok bool, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db.GetToolBaselineDecision(serverName)
}

// MarkToolBaselineDecided is the BoltDB form of Manager.MarkToolBaselineDecided.
func (b *BoltDB) MarkToolBaselineDecided(serverName string, d toolreview.BaselineDecision) error {
	if serverName == "" {
		return fmt.Errorf("mark tool baseline: empty server name")
	}
	if d.DecidedAt.IsZero() {
		d.DecidedAt = time.Now().UTC()
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(ToolBaselineDecisionsBucket))
		if err != nil {
			return err
		}
		key := []byte(serverName)
		if raw := bucket.Get(key); raw != nil {
			var stored toolreview.BaselineDecision
			if json.Unmarshal(raw, &stored) == nil {
				if stored.Source != toolreview.BaselineSourceInferred || d.Source == toolreview.BaselineSourceInferred {
					return nil // already decided; nothing to upgrade
				}
				if !stored.DecidedAt.IsZero() {
					d.DecidedAt = stored.DecidedAt
				}
			}
			// An undecodable value is replaced by the new decision.
		}
		data, err := json.Marshal(d)
		if err != nil {
			return err
		}
		return bucket.Put(key, data)
	})
}

// GetToolBaselineDecision is the BoltDB form of Manager.GetToolBaselineDecision.
func (b *BoltDB) GetToolBaselineDecision(serverName string) (d toolreview.BaselineDecision, ok bool, err error) {
	err = b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(ToolBaselineDecisionsBucket))
		if bucket == nil {
			return nil
		}
		raw := bucket.Get([]byte(serverName))
		if raw == nil {
			return nil
		}
		ok = true
		if json.Unmarshal(raw, &d) != nil {
			d = toolreview.BaselineDecision{Source: toolreview.BaselineSourceInferred}
		}
		return nil
	})
	return d, ok, err
}

// deleteToolBaselineDecisionsTx removes the markers of the given servers (or,
// with keep != nil, of every server NOT in keep) inside the caller's
// transaction, so a marker is only ever removed together with the server's
// approval records. A store without the bucket is a no-op.
func deleteToolBaselineDecisionsTx(tx *bbolt.Tx, servers []string, keep map[string]bool) error {
	bucket := tx.Bucket([]byte(ToolBaselineDecisionsBucket))
	if bucket == nil {
		return nil
	}
	var keys [][]byte
	if keep != nil {
		if err := bucket.ForEach(func(k, _ []byte) error {
			if !keep[string(k)] {
				keys = append(keys, append([]byte(nil), k...))
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, s := range servers {
		keys = append(keys, []byte(s))
	}
	for _, k := range keys {
		if err := bucket.Delete(k); err != nil {
			return err
		}
	}
	return nil
}
