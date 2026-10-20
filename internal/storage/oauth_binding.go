package storage

import (
	stderrors "errors"
	"fmt"
	"time"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
)

// Spec 116 PR2b (#1607): OAuth state bound to the server incarnation.
//
// Every OAuth write an upstream instance makes (a token save after a code
// exchange or a refresh, a DCR client registration, a login's paired token
// and client, a completion event) and every clear it makes (the 5xx token
// clear, the config-build DCR hygiene clears, a refresh or login the
// authorization server rejected) is bound to the instance's
// commitview.CredentialBinding: the {Incarnation, OAuthEpoch} of the
// committed view it was built for. Each method below runs ONE bbolt
// transaction that reads the server's upstreams row and acts only while the
// row still carries that binding. The removal purge (which deletes the row)
// and the OAuth-config clear (which raises the row's OAuthEpoch) also run in
// one transaction with the row write, so the two serialize in bbolt: a bound
// write either committed first, and the purge or clear deleted it, or it sees
// the row gone or re-bound and writes nothing. An old instance's write that
// was already past its network round-trip can therefore never re-create
// credentials under a removed name, nor restore credentials an OAuth config
// change cleared, and a same-name re-add never sees the old server's tokens.
//
// Records carry the epoch they were written under (OAuthTokenRecord.
// OAuthEpoch) and a bucket-sequence Revision. A bound read ignores a record of
// another epoch; a bound clear acts only on the exact revision its caller
// inspected (review R7.3), so a parked clear never erases what somebody
// rewrote meanwhile.
//
// The zero binding is "unbound": a caller that has no committed view (the
// standalone CLI, a test fixture). It acts on the current state of the name:
// once any config commit has been recorded a write needs the row to exist
// (writing for a removed server is refused) and is stamped with the row's
// epoch; a read sees every record. Daemon instances are always bound.
//
// No lock beyond bbolt's own is taken, so this adds no lock-order edge
// (plan.md "Credential-binding checks add no edge").

// putOAuthRecord writes rec under key and stamps it with the next bucket
// sequence (review R7.3). EVERY write to the oauth_tokens bucket goes through
// it; the sequence is persisted and never reused, so a deleted and re-created
// record never repeats an older revision.
func putOAuthRecord(bucket *bbolt.Bucket, key string, rec *OAuthTokenRecord) error {
	seq, err := bucket.NextSequence()
	if err != nil {
		return err
	}
	rec.Revision = seq
	data, err := rec.MarshalBinary()
	if err != nil {
		return err
	}
	return bucket.Put([]byte(key), data)
}

// oauthRowBindingTx returns the binding stored on server's upstreams row.
// found is false when no row exists.
func oauthRowBindingTx(tx *bbolt.Tx, server string) (b commitview.CredentialBinding, found bool, err error) {
	ub := tx.Bucket([]byte(UpstreamsBucket))
	if ub == nil {
		return b, false, nil
	}
	data := ub.Get([]byte(server))
	if data == nil {
		return b, false, nil
	}
	rec := &UpstreamRecord{}
	if err := rec.UnmarshalBinary(data); err != nil {
		return b, false, fmt.Errorf("decode upstream %s: %w", server, err)
	}
	return commitview.CredentialBinding{Incarnation: rec.Incarnation, OAuthEpoch: rec.OAuthEpoch}, true, nil
}

// configCommittedTx reports whether a Spec 116 config commit was ever
// recorded in this database.
func configCommittedTx(tx *bbolt.Tx) bool {
	meta := tx.Bucket([]byte(MetaBucket))
	return meta != nil && meta.Get([]byte(ConfigCommitGenerationKey)) != nil
}

// oauthWriteAllowedTx decides whether a write bound to b may change server's
// OAuth state, and returns the epoch the written record carries.
//
//   - bound (b non-zero): the row must exist and carry b. A row written
//     before Spec 116 (incarnation 0) is not authoritative for the
//     incarnation, but its epoch must still match.
//   - unbound: allowed while the row exists (the record takes the row's
//     epoch), or while no commit was ever recorded (a fixture database).
func oauthWriteAllowedTx(tx *bbolt.Tx, server string, b commitview.CredentialBinding) (allowed bool, epoch int64, err error) {
	row, found, err := oauthRowBindingTx(tx, server)
	if err != nil {
		return false, 0, err
	}
	if b.IsZero() {
		if found {
			return true, row.OAuthEpoch, nil
		}
		return !configCommittedTx(tx), 0, nil
	}
	if !found {
		return false, 0, nil
	}
	if row.Incarnation != 0 && row.Incarnation != b.Incarnation {
		return false, 0, nil
	}
	if row.OAuthEpoch != b.OAuthEpoch {
		return false, 0, nil
	}
	return true, b.OAuthEpoch, nil
}

// OAuthRecordVisible reports whether a reader bound to b may use rec: a
// bound reader sees only records written under its epoch (a record written
// before PR2b carries epoch 0 and is read only by a binding with epoch 0);
// an unbound reader sees every record.
func OAuthRecordVisible(rec *OAuthTokenRecord, b commitview.CredentialBinding) bool {
	if rec == nil {
		return false
	}
	return b.IsZero() || rec.OAuthEpoch == b.OAuthEpoch
}

// getOAuthRecordTx reads the record at key; nil when absent.
func getOAuthRecordTx(bucket *bbolt.Bucket, key string) (*OAuthTokenRecord, error) {
	data := bucket.Get([]byte(key))
	if data == nil {
		return nil, nil
	}
	rec := &OAuthTokenRecord{}
	if err := rec.UnmarshalBinary(data); err != nil {
		return nil, err
	}
	return rec, nil
}

// GetOAuthTokenBound is GetOAuthToken for a reader bound to binding: a
// record written under another binding reads as absent ("oauth token not
// found").
func (b *BoltDB) GetOAuthTokenBound(serverKey string, binding commitview.CredentialBinding) (*OAuthTokenRecord, error) {
	var record *OAuthTokenRecord
	err := b.db.View(func(tx *bbolt.Tx) error {
		rec, err := getOAuthRecordTx(tx.Bucket([]byte(OAuthTokenBucket)), serverKey)
		if err != nil {
			return err
		}
		if !OAuthRecordVisible(rec, binding) {
			return fmt.Errorf("oauth token not found")
		}
		record = rec
		return nil
	})
	return record, err
}

// GetOAuthClientCredentialsBound is GetOAuthClientCredentials for a reader
// bound to binding; it also returns the revision of the record it read (0
// when none), which a later ClearOAuthClientIfBound passes back.
func (b *BoltDB) GetOAuthClientCredentialsBound(serverKey string, binding commitview.CredentialBinding) (clientID, clientSecret string, callbackPort int, redirectURI string, revision uint64, err error) {
	err = b.db.View(func(tx *bbolt.Tx) error {
		rec, err := getOAuthRecordTx(tx.Bucket([]byte(OAuthTokenBucket)), serverKey)
		if err != nil || !OAuthRecordVisible(rec, binding) {
			return err
		}
		clientID, clientSecret, callbackPort, redirectURI, revision = rec.ClientID, rec.ClientSecret, rec.CallbackPort, rec.RedirectURI, rec.Revision
		return nil
	})
	return
}

// UpdateOAuthTokenIfBound is UpdateOAuthToken bound to the credential
// binding of server (Spec 116 PR2b): mutate runs and the record is written
// only while server's row carries binding, in the same transaction. A record
// of another epoch under the key is a leftover the bound writer does not
// build on: mutate starts from an empty record. The written record carries
// the binding's epoch. It reports whether it wrote; mutate returning
// ErrSkipOAuthTokenUpdate leaves the record alone and reports false. On a
// write, the record passed to mutate carries the written Revision afterwards
// (a refresh flight reports it as the revision it serves).
func (b *BoltDB) UpdateOAuthTokenIfBound(serverKey, server string, binding commitview.CredentialBinding, mutate func(rec *OAuthTokenRecord) error) (bool, error) {
	written := false
	err := b.db.Update(func(tx *bbolt.Tx) error {
		allowed, epoch, err := oauthWriteAllowedTx(tx, server, binding)
		if err != nil || !allowed {
			return err
		}
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		now := time.Now()
		record, err := getOAuthRecordTx(bucket, serverKey)
		if err != nil {
			return err
		}
		if record == nil || !OAuthRecordVisible(record, binding) {
			record = &OAuthTokenRecord{ServerName: serverKey, Created: now}
		}
		if err := mutate(record); err != nil {
			return err
		}
		record.ServerName = serverKey
		if record.Created.IsZero() {
			record.Created = now
		}
		record.Updated = now
		record.OAuthEpoch = epoch
		written = true
		return putOAuthRecord(bucket, serverKey, record)
	})
	if stderrors.Is(err, ErrSkipOAuthTokenUpdate) {
		return false, nil
	}
	return written && err == nil, err
}

// OAuthClientSaveMode selects the precondition of SaveOAuthClientIfBound.
type OAuthClientSaveMode int

const (
	// OAuthClientSaveAlways writes the registration whatever the record
	// holds (the operator's static client; UpdateOAuthClientCredentials).
	OAuthClientSaveAlways OAuthClientSaveMode = iota
	// OAuthClientSaveIfUnsetOrSame writes only while the record holds no
	// client id or this same one (SaveOAuthClientCredentialsIfUnsetOrSame).
	OAuthClientSaveIfUnsetOrSame
	// OAuthClientSaveIfSameOrEmpty writes only while the record holds this
	// same client id, or neither a client id nor a refresh token
	// (SaveOAuthClientCredentialsIfSameOrEmpty).
	OAuthClientSaveIfSameOrEmpty
)

// SaveOAuthClientIfBound persists a DCR client registration (client id and
// secret, callback port, redirect URI) bound to the credential binding of
// server: it writes only while server's row carries binding and mode's
// precondition holds on the record, in one transaction. It reports whether
// it wrote.
func (b *BoltDB) SaveOAuthClientIfBound(serverKey, server string, binding commitview.CredentialBinding, mode OAuthClientSaveMode, clientID, clientSecret string, callbackPort int, redirectURI string) (bool, error) {
	saved := false
	err := b.db.Update(func(tx *bbolt.Tx) error {
		allowed, epoch, err := oauthWriteAllowedTx(tx, server, binding)
		if err != nil || !allowed {
			return err
		}
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		now := time.Now()
		record, err := getOAuthRecordTx(bucket, serverKey)
		if err != nil {
			return err
		}
		if record == nil || !OAuthRecordVisible(record, binding) {
			record = &OAuthTokenRecord{ServerName: serverKey, Created: now}
		} else {
			switch mode {
			case OAuthClientSaveIfUnsetOrSame:
				if record.ClientID != "" && record.ClientID != clientID {
					return nil
				}
			case OAuthClientSaveIfSameOrEmpty:
				if record.ClientID != clientID && (record.ClientID != "" || record.RefreshToken != "") {
					return nil
				}
			}
		}
		record.ClientID = clientID
		record.ClientSecret = clientSecret
		record.CallbackPort = callbackPort
		record.RedirectURI = redirectURI
		record.Updated = now
		record.OAuthEpoch = epoch
		saved = true
		return putOAuthRecord(bucket, serverKey, record)
	})
	return saved && err == nil, err
}

// boundClearTx runs the checks every instance-originated destructive OAuth
// operation shares (review R6.2, R7.3): server's row carries binding, the
// record exists, was written under the binding's epoch, and still has the
// revision observedRev the caller based its decision on. It returns the
// record when all hold, nil otherwise.
func boundClearTx(tx *bbolt.Tx, serverKey, server string, binding commitview.CredentialBinding, observedRev uint64) (*OAuthTokenRecord, error) {
	allowed, _, err := oauthWriteAllowedTx(tx, server, binding)
	if err != nil || !allowed {
		return nil, err
	}
	rec, err := getOAuthRecordTx(tx.Bucket([]byte(OAuthTokenBucket)), serverKey)
	if err != nil || rec == nil {
		return nil, err
	}
	if !OAuthRecordVisible(rec, binding) || rec.Revision != observedRev {
		return nil, nil
	}
	return rec, nil
}

// DeleteOAuthTokenIfBound deletes the record at serverKey (tokens and DCR
// client) only while server's row carries binding and the record is still
// at revision observedRev, the one the caller inspected (the 5xx token clear,
// review R6.2). It reports whether it deleted.
func (b *BoltDB) DeleteOAuthTokenIfBound(serverKey, server string, binding commitview.CredentialBinding, observedRev uint64) (bool, error) {
	deleted := false
	err := b.db.Update(func(tx *bbolt.Tx) error {
		rec, err := boundClearTx(tx, serverKey, server, binding, observedRev)
		if err != nil || rec == nil {
			return err
		}
		deleted = true
		return tx.Bucket([]byte(OAuthTokenBucket)).Delete([]byte(serverKey))
	})
	return deleted && err == nil, err
}

// ClearOAuthClientIfBound clears only the DCR registration fields (client
// id and secret, callback port, redirect URI) of the record at serverKey,
// keeping its tokens, expiry and epoch, under the same checks as
// DeleteOAuthTokenIfBound (review R9.4): a DCR clear forces a fresh
// registration on the next login; it does not sign the server out. The
// rewrite takes a new revision, so a second parked clear that observed the
// old one is dropped. It reports whether it cleared.
func (b *BoltDB) ClearOAuthClientIfBound(serverKey, server string, binding commitview.CredentialBinding, observedRev uint64) (bool, error) {
	cleared := false
	err := b.db.Update(func(tx *bbolt.Tx) error {
		rec, err := boundClearTx(tx, serverKey, server, binding, observedRev)
		if err != nil || rec == nil {
			return err
		}
		rec.ClientID = ""
		rec.ClientSecret = ""
		rec.CallbackPort = 0
		rec.RedirectURI = ""
		rec.Updated = time.Now()
		cleared = true
		return putOAuthRecord(tx.Bucket([]byte(OAuthTokenBucket)), serverKey, rec)
	})
	return cleared && err == nil, err
}

// ClearOAuthClientIfBoundAndClientID clears the DCR registration fields of
// the record at serverKey like ClearOAuthClientIfBound, but decides on the
// client id instead of a revision: it acts only while server's row carries
// binding, the record was written under it, and the record still holds
// expectedClientID, all in one transaction. It is the login path's clear of
// a registration the authorization server rejected: that login holds the
// client id it sent, not the record it was read from, and DCR issues a new id
// per registration, so a concurrent login that stored a fresh registration is
// never wiped. It reports whether it cleared.
func (b *BoltDB) ClearOAuthClientIfBoundAndClientID(serverKey, server string, binding commitview.CredentialBinding, expectedClientID string) (bool, error) {
	if expectedClientID == "" {
		return false, nil
	}
	cleared := false
	err := b.db.Update(func(tx *bbolt.Tx) error {
		allowed, _, err := oauthWriteAllowedTx(tx, server, binding)
		if err != nil || !allowed {
			return err
		}
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		rec, err := getOAuthRecordTx(bucket, serverKey)
		if err != nil || rec == nil || !OAuthRecordVisible(rec, binding) || rec.ClientID != expectedClientID {
			return err
		}
		rec.ClientID = ""
		rec.ClientSecret = ""
		rec.CallbackPort = 0
		rec.RedirectURI = ""
		rec.Updated = time.Now()
		cleared = true
		return putOAuthRecord(bucket, serverKey, rec)
	})
	return cleared && err == nil, err
}

// ClearOAuthTokenForStandaloneLogout deletes the token record at exactly
// serverKey (oauth.GenerateServerKey(name, url)) for `mcpproxy auth logout`
// without a daemon (review R8.3). It is an operator action on the current
// state, not stale-instance cleanup, so it is not binding-conditional: the
// standalone CLI opens config.db itself, and bbolt's exclusive file lock
// means no daemon (and so no live instance) shares the database meanwhile.
// scripts/check-upstream-write-sites.sh allows it in exactly one file,
// internal/upstream/cli/client.go.
func (b *BoltDB) ClearOAuthTokenForStandaloneLogout(serverKey string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(OAuthTokenBucket)).Delete([]byte(serverKey))
	})
}

// SaveOAuthCompletionEventIfBound records an OAuth completion event bound to
// binding (Spec 116 PR2b, T055c): it writes only while the server's row
// carries the binding (an unbound caller: while the row exists, or no commit
// was ever recorded) and stamps the event with the binding, so the consumer
// can drop an event that a superseded instance wrote. It reports whether it
// wrote.
func (b *BoltDB) SaveOAuthCompletionEventIfBound(event *OAuthCompletionEvent, binding commitview.CredentialBinding) (bool, error) {
	written := false
	err := b.db.Update(func(tx *bbolt.Tx) error {
		allowed, _, err := oauthWriteAllowedTx(tx, event.ServerName, binding)
		if err != nil || !allowed {
			return err
		}
		event.Incarnation, event.OAuthEpoch = binding.Incarnation, binding.OAuthEpoch
		if err := putOAuthCompletionEventTx(tx, event); err != nil {
			return err
		}
		written = true
		return nil
	})
	return written && err == nil, err
}

// CheckCommittedView is the construction-time check of an upstream instance
// (Spec 116 PR2b, review R6.1): in one bbolt View, server's row must exist
// and carry exactly sg (generation, incarnation and OAuth epoch). Otherwise
// it returns commitview.ErrStaleGeneration and the caller builds nothing: a
// newer commit already wrote the row, and that commit's own worker builds the
// instance for it. A failed check is not an error to report.
func (m *Manager) CheckCommittedView(server string, sg commitview.ServerGen) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return fmt.Errorf("storage not initialized")
	}
	return m.db.db.View(func(tx *bbolt.Tx) error {
		ub := tx.Bucket([]byte(UpstreamsBucket))
		if ub == nil {
			return commitview.ErrStaleGeneration
		}
		data := ub.Get([]byte(server))
		if data == nil {
			return commitview.ErrStaleGeneration
		}
		rec := &UpstreamRecord{}
		if err := rec.UnmarshalBinary(data); err != nil {
			return fmt.Errorf("decode upstream %s: %w", server, err)
		}
		if rec.Generation != sg.Gen || rec.Incarnation != sg.Incarnation || rec.OAuthEpoch != sg.OAuthEpoch {
			return commitview.ErrStaleGeneration
		}
		return nil
	})
}
