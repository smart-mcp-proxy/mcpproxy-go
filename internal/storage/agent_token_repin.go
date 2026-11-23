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

// repinRestoreEntry is the restore entry of one client whose binding also lives
// in an oauth_client_bindings entry. It carries the client id, the OLD pin and
// mode (ProfilePin/ProfileMode) and, in MatchedPrefix (never persisted), the
// WriteID that repin's own write produced on that entry: RestorePins restores
// only while the entry still has exactly that incarnation.
func repinRestoreEntry(clientID, oldPin, oldMode, writeID string) auth.AgentToken {
	return auth.AgentToken{
		Name: auth.ClientTokenName(clientID), Kind: auth.KindClient, ClientID: clientID,
		ProfilePin: oldPin, ProfileMode: oldMode,
		MatchedGeneration: repinRestoreMarker, MatchedPrefix: writeID,
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
// entry, `before` ends with a restore entry (see repinRestoreEntry). Existing
// records come first, in bbolt key order, exactly as before.
//
// A repin never touches PriorProfilePin/PriorProfileMode: that snapshot is the
// binding the client had before its connect, and undoing the connect returns the
// client to exactly that, whatever profile renames happened since — the same as
// for an API-key tombstone before Spec 132.
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
		// captureWriteID reads the incarnation repin's own write just produced.
		captureWriteID := func(id string) (string, bool, error) {
			e, err := getOAuthBindingTx(tx, id)
			if err != nil || e == nil {
				return "", false, err
			}
			return e.WriteID, true, nil
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
			// (if any) moves too.
			if err := writeClientBindingTx(tx, w.old.ClientID, to, w.old.ProfileMode); err != nil {
				return fmt.Errorf("failed to repin agent token: %w", err)
			}
			before = append(before, w.old)
			wid, has, err := captureWriteID(w.old.ClientID)
			if err != nil {
				return err
			}
			if has {
				restores = append(restores, repinRestoreEntry(w.old.ClientID, w.old.ProfilePin, w.old.ProfileMode, wid))
			}
		}
		for j, id := range oauthOnlyIDs {
			if err := step(); err != nil {
				return err
			}
			if err := writeClientBindingTx(tx, id, to, oauthOnly[j].ProfileMode); err != nil {
				return fmt.Errorf("failed to repin agent token: %w", err)
			}
			wid, _, err := captureWriteID(id)
			if err != nil {
				return err
			}
			restores = append(restores, repinRestoreEntry(id, oauthOnly[j].ProfilePin, oauthOnly[j].ProfileMode, wid))
		}
		before = append(before, restores...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return before, nil
}

// restoreRecordPinTx puts back the stored pin of one record by hash, exactly as
// before Spec 132 (the mode is never changed by a repin). A record that no
// longer exists, or is owned, is left alone.
func restoreRecordPinTx(tx *bbolt.Tx, rec *auth.AgentToken) error {
	tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
	if tokenBucket == nil {
		return nil
	}
	key := []byte(rec.TokenHash)
	v := tokenBucket.Get(key)
	if v == nil {
		return nil
	}
	var t auth.AgentToken
	if json.Unmarshal(v, &t) != nil || t.UserID != "" {
		return nil
	}
	t.ProfilePin = rec.ProfilePin
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := tokenBucket.Put(key, data); err != nil {
		return fmt.Errorf("failed to restore agent token pin: %w", err)
	}
	return nil
}

// RestorePins puts back the ProfilePin of each record RepinProfile returned,
// in one transaction (the rollback of a repin whose config write failed). A
// record that no longer exists is left alone. Regular tokens are restored by
// hash.
//
// For a CLIENT, ONE decision is made before either copy is touched, from the
// client's oauth_client_bindings entry as it is now:
//
//   - no entry: restore the API-key record by hash, unconditionally, as before
//     Spec 132; an entry is never created;
//   - entry and a restore entry in `before`: restore the old pin and mode on
//     both copies (through the binding writer, so it also works when the
//     API-key record is gone) only if the entry's WriteID is still the one
//     repin's own write produced; anything that wrote the binding since
//     (reassignment, new mint, forget and re-create, even with the same pin and
//     mode) restores neither copy, nor the record by hash;
//   - entry but no restore entry (the client got its first browser login after
//     the repin): restore both copies only if the record from `before` still
//     exists and the entry equals that record's CURRENT pin and mode; else
//     leave both alone.
func (m *Manager) RestorePins(before []auth.AgentToken) error {
	if len(before) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		restoreEntries := map[string]*auth.AgentToken{}
		clientRecords := map[string]*auth.AgentToken{}
		var clientOrder []string
		seen := map[string]bool{}
		note := func(id string) {
			if !seen[id] {
				seen[id] = true
				clientOrder = append(clientOrder, id)
			}
		}
		for i := range before {
			b := &before[i]
			switch {
			case isRepinRestoreEntry(b):
				restoreEntries[b.ClientID] = b
				note(b.ClientID)
			case b.Kind == auth.KindClient && b.ClientID != "" && b.TokenHash != "":
				clientRecords[b.ClientID] = b
				note(b.ClientID)
			default:
				if b.TokenHash == "" {
					continue
				}
				if err := restoreRecordPinTx(tx, b); err != nil {
					return err
				}
			}
		}
		for _, id := range clientOrder {
			entry, err := getOAuthBindingTx(tx, id)
			if err != nil {
				return err
			}
			rec, e := clientRecords[id], restoreEntries[id]
			switch {
			case entry == nil:
				if rec != nil {
					if err := restoreRecordPinTx(tx, rec); err != nil {
						return err
					}
				}
			case e != nil:
				if entry.WriteID != "" && entry.WriteID == e.MatchedPrefix {
					if err := writeClientBindingTx(tx, id, e.ProfilePin, e.ProfileMode); err != nil {
						return err
					}
				}
			default: // entry, no restore entry; rec is non-nil here
				cur := tx.Bucket([]byte(AgentTokensBucket))
				if cur == nil {
					continue
				}
				v := cur.Get([]byte(rec.TokenHash))
				if v == nil {
					continue
				}
				var t auth.AgentToken
				if json.Unmarshal(v, &t) != nil || t.UserID != "" {
					continue
				}
				if t.ProfilePin == entry.ProfilePin && t.ProfileMode == entry.ProfileMode {
					if err := writeClientBindingTx(tx, id, rec.ProfilePin, rec.ProfileMode); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}
