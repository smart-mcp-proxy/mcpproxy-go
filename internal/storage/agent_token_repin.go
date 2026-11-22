package storage

import (
	"encoding/json"
	"fmt"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// repinRestoreMarker marks a record in RepinProfile's `before` slice as a
// browser-login restore entry rather than a stored token record. It is carried
// in AgentToken.MatchedGeneration, a field that is never persisted (json:"-")
// and never set on a stored record, so a real record cannot be mistaken for it
// whatever its TokenHash is.
const repinRestoreMarker = "spec132-repin-binding-restore"

// repinRestoreEntry builds the restore entry of one client RepinProfile moved
// and whose binding lives (also) in an oauth_client_bindings entry. It carries
// the OLD binding in ProfilePin/ProfileMode and what repin WROTE in
// PriorProfilePin/PriorProfileMode, so RestorePins can compare-and-set.
func repinRestoreEntry(clientID, oldPin, oldMode, wrotePin, wroteMode string) auth.AgentToken {
	return auth.AgentToken{
		Name: auth.ClientTokenName(clientID), Kind: auth.KindClient, ClientID: clientID,
		ProfilePin: oldPin, ProfileMode: oldMode,
		PriorProfilePin: wrotePin, PriorProfileMode: wroteMode,
		MatchedGeneration: repinRestoreMarker,
	}
}

func isRepinRestoreEntry(t *auth.AgentToken) bool {
	return t.MatchedGeneration == repinRestoreMarker && t.Kind == auth.KindClient && t.ClientID != ""
}

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
//
// Spec 132: a client's pin is written through writeClientBindingTx, so its
// browser-login binding entry moves with its API-key record, and an entry of a
// client that has NO API-key record moves too. For every client that has an
// entry, `before` ends with a restore entry (see repinRestoreEntry) that
// RestorePins applies compare-and-set. Existing records come first, in bbolt
// key order, exactly as before.
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
		type rewrite struct {
			key    []byte
			data   []byte // direct rewrite of a regular token; nil for a client record
			old    auth.AgentToken
			client bool
		}
		var work []rewrite // ONE list, in bbolt key order
		clientsWithRecord := map[string]bool{}
		// Collect first: bbolt forbids Put while a ForEach is iterating.
		if tokenBucket := tx.Bucket([]byte(AgentTokensBucket)); tokenBucket != nil {
			if err := tokenBucket.ForEach(func(k, v []byte) error {
				var t auth.AgentToken
				if json.Unmarshal(v, &t) != nil {
					return nil
				}
				isClient := t.Kind == auth.KindClient && t.ClientID != ""
				if isClient {
					clientsWithRecord[t.ClientID] = true
				}
				if t.UserID != "" || t.ProfilePin != from {
					return nil
				}
				old := t
				var data []byte
				if !isClient {
					// Regular (non-client) agent token: plain pin rewrite.
					t.ProfilePin = to
					var marshalErr error
					if data, marshalErr = json.Marshal(t); marshalErr != nil {
						return marshalErr
					}
				}
				work = append(work, rewrite{key: append([]byte(nil), k...), data: data, old: old, client: isClient})
				return nil
			}); err != nil {
				return err
			}
		}
		// Browser-login-only clients: entries pinned to `from` whose client has
		// no API-key record. They come after every existing record.
		var oauthOnly []OAuthClientBinding
		var oauthOnlyIDs []string
		if err := oauthForEach(tx, OAuthClientBindingsBucket, func(id string, b OAuthClientBinding) error {
			if b.ProfilePin == from && !clientsWithRecord[id] {
				oauthOnly = append(oauthOnly, b)
				oauthOnlyIDs = append(oauthOnlyIDs, id)
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
		var restores []auth.AgentToken
		for _, w := range work {
			if err := step(); err != nil {
				return err
			}
			if !w.client {
				if err := tx.Bucket([]byte(AgentTokensBucket)).Put(w.key, w.data); err != nil {
					return fmt.Errorf("failed to repin agent token: %w", err)
				}
				before = append(before, w.old)
				continue
			}
			// A client record: through the binding writer so its OAuth entry
			// moves too. When it has an entry, the mint-time rollback snapshot
			// is stale after this move and is cleared in the same write, so a
			// later connect rollback cannot put a pre-reassignment binding back
			// on the existing browser logins.
			entry, err := getOAuthBindingTx(tx, w.old.ClientID)
			if err != nil {
				return err
			}
			if err := writeClientBindingOptsTx(tx, w.old.ClientID, to, w.old.ProfileMode, entry != nil); err != nil {
				return fmt.Errorf("failed to repin agent token: %w", err)
			}
			before = append(before, w.old)
			if entry != nil {
				restores = append(restores, repinRestoreEntry(w.old.ClientID, w.old.ProfilePin, w.old.ProfileMode, to, w.old.ProfileMode))
			}
		}
		for j, id := range oauthOnlyIDs {
			if err := step(); err != nil {
				return err
			}
			if err := writeClientBindingTx(tx, id, to, oauthOnly[j].ProfileMode); err != nil {
				return fmt.Errorf("failed to repin agent token: %w", err)
			}
			restores = append(restores, repinRestoreEntry(id, oauthOnly[j].ProfilePin, oauthOnly[j].ProfileMode, to, oauthOnly[j].ProfileMode))
		}
		before = append(before, restores...)
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
//
// Stored records are restored by hash, as before. The restore entries for
// browser-login bindings are compare-and-set: the entry is restored only if it
// still holds exactly what repin wrote (and a current API-key record of that
// client holds that or the old binding); an entry that is gone is not
// re-created, and one that changed since (re-created client, new mint, an
// administrator's reassignment) is left alone. They do not depend on the
// original API-key record still existing.
func (m *Manager) RestorePins(before []auth.AgentToken) error {
	if len(before) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		for i := range before {
			if isRepinRestoreEntry(&before[i]) {
				continue // second pass, after the records
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
			t.ProfilePin = before[i].ProfilePin
			data, err := json.Marshal(t)
			if err != nil {
				return err
			}
			if err := tokenBucket.Put(key, data); err != nil {
				return fmt.Errorf("failed to restore agent token pin: %w", err)
			}
		}
		for i := range before {
			if isRepinRestoreEntry(&before[i]) {
				if err := restoreRepinnedClientTx(tx, &before[i]); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// restoreRepinnedClientTx is the compare-and-set restore of one client's
// browser-login binding (see RestorePins).
func restoreRepinnedClientTx(tx *bbolt.Tx, p *auth.AgentToken) error {
	oldPin, oldMode := p.ProfilePin, p.ProfileMode
	wrotePin, wroteMode := p.PriorProfilePin, p.PriorProfileMode
	entry, err := getOAuthBindingTx(tx, p.ClientID)
	if err != nil {
		return err
	}
	if entry == nil || entry.ProfilePin != wrotePin || entry.ProfileMode != wroteMode {
		return nil // gone (not re-created) or changed since: leave it alone
	}
	_, tok, err := findClientTokenRecordLocked(tx, p.ClientID)
	if err != nil {
		return err
	}
	if tok != nil && tok.Kind == auth.KindClient {
		wrote := tok.ProfilePin == wrotePin && tok.ProfileMode == wroteMode
		old := tok.ProfilePin == oldPin && tok.ProfileMode == oldMode // already restored by hash
		if !wrote && !old {
			return nil // the API-key record moved on its own
		}
	}
	return writeClientBindingTx(tx, p.ClientID, oldPin, oldMode)
}
