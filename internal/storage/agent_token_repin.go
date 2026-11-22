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
//
// Spec 132: a client's pin is written through writeClientBindingTx, so its
// browser-login binding entry moves with its API-key record, and an entry of a
// client that has NO API-key record moves too. Such an entry is reported in
// `before` as a record with an empty TokenHash (Kind=client, ClientID set) that
// RestorePins recognises.
func (m *Manager) repinProfile(from, to string, hook func(written int) error) ([]auth.AgentToken, error) {
	if from == "" || to == "" {
		return nil, fmt.Errorf("repin needs a source and a target profile")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var before []auth.AgentToken
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		before = nil
		type rewrite struct {
			key  []byte
			data []byte
			old  auth.AgentToken
		}
		var work []rewrite
		var clientWork []auth.AgentToken // kind=client records: via the binding writer
		clientsWithRecord := map[string]bool{}
		// Collect first: bbolt forbids Put while a ForEach is iterating.
		if tokenBucket := tx.Bucket([]byte(AgentTokensBucket)); tokenBucket != nil {
			if err := tokenBucket.ForEach(func(k, v []byte) error {
				var t auth.AgentToken
				if json.Unmarshal(v, &t) != nil {
					return nil
				}
				if t.Kind == auth.KindClient && t.ClientID != "" {
					clientsWithRecord[t.ClientID] = true
				}
				if t.UserID != "" || t.ProfilePin != from {
					return nil
				}
				old := t
				if t.Kind == auth.KindClient && t.ClientID != "" {
					clientWork = append(clientWork, old)
					return nil
				}
				// Regular (non-client) agent token: plain pin rewrite.
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
		}
		// Browser-login-only clients: entries pinned to `from` whose client has
		// no API-key record.
		var oauthOnly []auth.AgentToken
		if err := oauthForEach(tx, OAuthClientBindingsBucket, func(id string, b OAuthClientBinding) error {
			if b.ProfilePin == from && !clientsWithRecord[id] {
				oauthOnly = append(oauthOnly, auth.AgentToken{
					Name: auth.ClientTokenName(id), Kind: auth.KindClient, ClientID: id,
					ProfilePin: b.ProfilePin, ProfileMode: b.ProfileMode,
				})
			}
			return nil
		}); err != nil {
			return err
		}

		i := 0
		step := func() error {
			if hook != nil {
				if err := hook(i); err != nil {
					return err
				}
			}
			i++
			return nil
		}
		for _, w := range work {
			if err := step(); err != nil {
				return err
			}
			if err := tx.Bucket([]byte(AgentTokensBucket)).Put(w.key, w.data); err != nil {
				return fmt.Errorf("failed to repin agent token: %w", err)
			}
			before = append(before, w.old)
		}
		for _, old := range append(clientWork, oauthOnly...) {
			if err := step(); err != nil {
				return err
			}
			if err := writeClientBindingTx(tx, old.ClientID, to, old.ProfileMode); err != nil {
				return fmt.Errorf("failed to repin client binding: %w", err)
			}
			before = append(before, old)
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
// record that no longer exists is left alone. Client records, and the entries
// of browser-login-only clients (empty TokenHash), go through the binding writer.
func (m *Manager) RestorePins(before []auth.AgentToken) error {
	if len(before) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		for i := range before {
			if before[i].TokenHash == "" && before[i].Kind == auth.KindClient && before[i].ClientID != "" {
				// Browser-login-only client: its entry, if it still exists.
				b, err := getOAuthBindingTx(tx, before[i].ClientID)
				if err != nil {
					return err
				}
				if b != nil {
					if err := writeClientBindingTx(tx, before[i].ClientID, before[i].ProfilePin, b.ProfileMode); err != nil {
						return err
					}
				}
				continue
			}
			tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
			if tokenBucket == nil {
				continue
			}
			key := []byte(before[i].TokenHash)
			v := tokenBucket.Get(key)
			if v == nil {
				continue
			}
			var t auth.AgentToken
			if json.Unmarshal(v, &t) != nil || t.UserID != "" {
				continue
			}
			if t.Kind == auth.KindClient && t.ClientID != "" {
				if err := writeClientBindingTx(tx, t.ClientID, before[i].ProfilePin, t.ProfileMode); err != nil {
					return fmt.Errorf("failed to restore client binding: %w", err)
				}
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
