package storage

import (
	"encoding/json"
	"fmt"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// RepinProfile rewrites ProfilePin from -> to on EVERY stored ownerless token
// naming `from` — regular agent tokens and client credentials alike, revoked
// and expired ones included — in one transaction (Spec 108 D19). It is the
// token half of a profile rename or delete-with-reassign: doing it in one tx
// means a failure part-way leaves every record unchanged. Owned
// (server-edition) tokens are never touched: a profile is not tenant state.
//
// It returns the records as they were BEFORE the rewrite, so the caller can
// restore exactly those pins with RestorePins. An empty source or target is
// refused before anything is written (a locked client credential cannot hold
// an empty pin).
func (m *Manager) RepinProfile(from, to string) (before []auth.AgentToken, err error) {
	return m.repinProfile(from, to, nil)
}

// repinProfile is RepinProfile with a per-record hook (called before each
// record is written) so a test can inject a failure part-way through the tx.
func (m *Manager) repinProfile(from, to string, hook func(written int) error) ([]auth.AgentToken, error) {
	if from == "" || to == "" {
		return nil, fmt.Errorf("repin needs a source and a target profile")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var before []auth.AgentToken
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		before = nil
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return nil
		}
		type rewrite struct {
			key  []byte
			data []byte
			old  auth.AgentToken
		}
		var work []rewrite
		// Collect first: bbolt forbids Put while a ForEach is iterating.
		if err := tokenBucket.ForEach(func(k, v []byte) error {
			var t auth.AgentToken
			if json.Unmarshal(v, &t) != nil {
				return nil
			}
			if t.UserID != "" || t.ProfilePin != from {
				return nil
			}
			old := t
			t.ProfilePin = to
			data, marshalErr := json.Marshal(t)
			if marshalErr != nil {
				return marshalErr
			}
			work = append(work, rewrite{key: append([]byte(nil), k...), data: data, old: old})
			return nil
		}); err != nil {
			return err
		}
		for i, w := range work {
			if hook != nil {
				if err := hook(i); err != nil {
					return err
				}
			}
			if err := tokenBucket.Put(w.key, w.data); err != nil {
				return fmt.Errorf("failed to repin agent token: %w", err)
			}
			before = append(before, w.old)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return before, nil
}

// RestorePins puts back the ProfilePin of each record RepinProfile returned,
// in one transaction (the rollback of a repin whose config write failed). A
// record that no longer exists is left alone.
func (m *Manager) RestorePins(before []auth.AgentToken) error {
	if len(before) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return nil
		}
		for i := range before {
			key := []byte(before[i].TokenHash)
			v := tokenBucket.Get(key)
			if v == nil {
				continue
			}
			var t auth.AgentToken
			if json.Unmarshal(v, &t) != nil || t.UserID != "" {
				continue
			}
			t.ProfilePin = before[i].ProfilePin
			data, err := json.Marshal(t)
			if err != nil {
				return err
			}
			if err := tokenBucket.Put(key, data); err != nil {
				return fmt.Errorf("failed to restore agent token pin: %w", err)
			}
		}
		return nil
	})
}
