package storage

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
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
//
// Spec 123: the value is encoded by encodeOAuthRecord under k (callers pass
// BoltDB.oauthKey; nil writes plain JSON). Sealing runs inside the caller's
// transaction, so the transaction shape of every writer is unchanged. When a
// sealed value replaces a plaintext one, the compaction-advised marker is set
// in the same transaction (FR-016): the old plaintext page is now free but
// not zeroed.
func putOAuthRecord(bucket *bbolt.Bucket, k *tokenseal.Key, key string, rec *OAuthTokenRecord) error {
	return putOAuthRecordKeeping(bucket, k, key, rec, nil)
}

// putOAuthRecordKeeping is putOAuthRecord for a record decoded from the
// plaintext JSON object original: top-level fields of original that
// OAuthTokenRecord does not know (a row written by a newer binary) are kept
// in the written value. The boot sweep uses it (Spec 123 review), so sealing
// a row never drops data the binary does not understand. A nil original is
// putOAuthRecord.
func putOAuthRecordKeeping(bucket *bbolt.Bucket, k *tokenseal.Key, key string, rec *OAuthTokenRecord, original []byte) error {
	if hook := putOAuthRecordHook; hook != nil {
		if err := hook(key); err != nil {
			return err
		}
	}
	replacesPlaintext := false
	if k != nil {
		if prev := bucket.Get([]byte(key)); prev != nil {
			shape, _ := tokenseal.Classify(prev)
			replacesPlaintext = shape == tokenseal.ShapePlaintext
		}
	}
	seq, err := bucket.NextSequence()
	if err != nil {
		return err
	}
	rec.Revision = seq
	data, err := encodeOAuthRecord(k, key, rec, original)
	if err != nil {
		return err
	}
	if replacesPlaintext {
		if err := markOAuthCompactAdvisedTx(bucket.Tx()); err != nil {
			return err
		}
	}
	return bucket.Put([]byte(key), data)
}

// putOAuthRecordHook, when set (tests only), runs at the start of every
// putOAuthRecord; an error aborts that write and so its transaction. It is
// the fault-injection seam for the sweep's atomicity test.
var putOAuthRecordHook func(key string) error

// markOAuthCompactAdvisedTx sets meta/OAuthSealCompactAdvisedKey unless it is
// already set (the first sealing write's time is kept).
func markOAuthCompactAdvisedTx(tx *bbolt.Tx) error {
	meta, err := tx.CreateBucketIfNotExists([]byte(MetaBucket))
	if err != nil {
		return err
	}
	if meta.Get([]byte(OAuthSealCompactAdvisedKey)) != nil {
		return nil
	}
	return meta.Put([]byte(OAuthSealCompactAdvisedKey), []byte(time.Now().UTC().Format(time.RFC3339)))
}

// ErrOAuthTokenUndecryptableSentinel matches every *ErrOAuthTokenUndecryptable
// with errors.Is.
var ErrOAuthTokenUndecryptableSentinel = stderrors.New("stored OAuth token cannot be decrypted")

// ErrOAuthTokenUndecryptable is returned for an oauth_tokens row the
// configured key cannot turn into a record (Spec 123 FR-012): sealed under no
// key or another key, an envelope that fails authentication (edited, or moved
// under another bucket key), an unsupported envelope version, or a value
// that is neither an envelope nor a JSON record. It is distinct from "not
// found", and every writer that starts from the stored record refuses such a
// row (FR-013). It carries key ids only, never key material or row bytes.
type ErrOAuthTokenUndecryptable struct {
	Key    string           // bucket key of the row
	Reason tokenseal.Reason // no_key, key_mismatch, auth_failed, unsupported_version, malformed
	RowKID string           // key id in the row's envelope prefix ("" if none)
	KeyKID string           // key id of the configured key ("" if none)
}

func (e *ErrOAuthTokenUndecryptable) Error() string {
	msg := "stored OAuth token " + e.Key + " cannot be decrypted: " + string(e.Reason)
	if e.RowKID != "" {
		msg += " (row key id " + e.RowKID
		if e.KeyKID != "" {
			msg += ", configured key id " + e.KeyKID
		}
		msg += ")"
	} else if e.KeyKID != "" {
		msg += " (configured key id " + e.KeyKID + ")"
	}
	return msg
}

// Is makes errors.Is(err, ErrOAuthTokenUndecryptableSentinel) hold.
func (e *ErrOAuthTokenUndecryptable) Is(target error) bool {
	return target == ErrOAuthTokenUndecryptableSentinel
}

// encodeOAuthRecord is the only encoder of oauth_tokens values (FR-010,
// FR-011): plain JSON without a key, an enc:v1 envelope bound to key with one.
//
// A non-nil original (the plaintext JSON object rec was decoded from) also
// carries over the top-level fields OAuthTokenRecord does not know (see
// putOAuthRecordKeeping).
func encodeOAuthRecord(k *tokenseal.Key, key string, rec *OAuthTokenRecord, original []byte) ([]byte, error) {
	data, err := rec.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if original != nil {
		if data, err = keepUnknownOAuthFields(data, original); err != nil {
			return nil, err
		}
	}
	if k == nil {
		return data, nil
	}
	return k.Seal(OAuthTokenBucket, key, data)
}

// oauthRecordJSONNames are the JSON object keys OAuthTokenRecord decodes.
var oauthRecordJSONNames = func() []string {
	t := reflect.TypeOf(OAuthTokenRecord{})
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		names = append(names, name)
	}
	return names
}()

// keepUnknownOAuthFields adds to encoded (the marshalled record) every
// top-level key of original that no OAuthTokenRecord field decodes.
// encoding/json matches keys case-insensitively, so a key that folds to a
// known name is a known field and is not carried over (the record's value
// wins).
func keepUnknownOAuthFields(encoded, original []byte) ([]byte, error) {
	var orig map[string]json.RawMessage
	if err := json.Unmarshal(original, &orig); err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	added := false
	for name, v := range orig {
		known := false
		for _, n := range oauthRecordJSONNames {
			if strings.EqualFold(name, n) {
				known = true
				break
			}
		}
		if !known {
			out[name] = v
			added = true
		}
	}
	if !added {
		return encoded, nil
	}
	return json.Marshal(out)
}

// decodeOAuthRecord is the only decoder of oauth_tokens values (FR-010): a
// plaintext row is read as today, a sealed row is opened with k. Any failure
// is an *ErrOAuthTokenUndecryptable.
func decodeOAuthRecord(k *tokenseal.Key, key string, data []byte) (*OAuthTokenRecord, error) {
	shape, rowKID := tokenseal.Classify(data)
	plain := data
	switch shape {
	case tokenseal.ShapePlaintext:
	case tokenseal.ShapeSealedV1, tokenseal.ShapeUnsupported:
		opened, err := k.Open(OAuthTokenBucket, key, data)
		if err != nil {
			var oe *tokenseal.OpenError
			if stderrors.As(err, &oe) {
				return nil, &ErrOAuthTokenUndecryptable{Key: key, Reason: oe.Reason, RowKID: oe.RowKID, KeyKID: oe.KeyKID}
			}
			return nil, &ErrOAuthTokenUndecryptable{Key: key, Reason: tokenseal.ReasonMalformed, RowKID: rowKID, KeyKID: k.ID()}
		}
		plain = opened
	default:
		return nil, &ErrOAuthTokenUndecryptable{Key: key, Reason: tokenseal.ReasonMalformed, KeyKID: k.ID()}
	}
	rec := &OAuthTokenRecord{}
	if err := rec.UnmarshalBinary(plain); err != nil {
		return nil, &ErrOAuthTokenUndecryptable{Key: key, Reason: tokenseal.ReasonMalformed, RowKID: rowKID, KeyKID: k.ID()}
	}
	return rec, nil
}

// oauthRowIsEnvelopeTx reports whether the row stored under key has the
// "enc:" prefix of an encrypted envelope, well-formed or not. It reads the
// prefix only and decodes nothing.
func oauthRowIsEnvelopeTx(bucket *bbolt.Bucket, key string) bool {
	return bytes.HasPrefix(bucket.Get([]byte(key)), []byte("enc:"))
}

// oauthRawRow is one oauth_tokens key/value pair copied out of the bucket.
type oauthRawRow struct {
	key   string
	value []byte
}

// collectOAuthRows copies every key/value pair of the oauth_tokens bucket.
// It is the one listing walk: callers decode or rewrite rows only after it
// returns, so no caller mutates the bucket while a cursor is on it (bbolt
// cursors skip keys when the bucket changes during iteration; see
// clearOAuthStateTx).
func collectOAuthRows(bucket *bbolt.Bucket) ([]oauthRawRow, error) {
	var rows []oauthRawRow
	err := bucket.ForEach(func(k, v []byte) error {
		rows = append(rows, oauthRawRow{key: string(k), value: append([]byte(nil), v...)})
		return nil
	})
	return rows, err
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

// getOAuthRecordTx reads the record at key; nil when absent. A row k cannot
// decode is an *ErrOAuthTokenUndecryptable, never "absent".
func getOAuthRecordTx(bucket *bbolt.Bucket, k *tokenseal.Key, key string) (*OAuthTokenRecord, error) {
	data := bucket.Get([]byte(key))
	if data == nil {
		return nil, nil
	}
	return decodeOAuthRecord(k, key, data)
}

// GetOAuthTokenBound is GetOAuthToken for a reader bound to binding: a
// record written under another binding reads as absent ("oauth token not
// found").
func (b *BoltDB) GetOAuthTokenBound(serverKey string, binding commitview.CredentialBinding) (*OAuthTokenRecord, error) {
	var record *OAuthTokenRecord
	err := b.db.View(func(tx *bbolt.Tx) error {
		rec, err := getOAuthRecordTx(tx.Bucket([]byte(OAuthTokenBucket)), b.oauthKey, serverKey)
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
		rec, err := getOAuthRecordTx(tx.Bucket([]byte(OAuthTokenBucket)), b.oauthKey, serverKey)
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
		record, err := getOAuthRecordTx(bucket, b.oauthKey, serverKey)
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
		return putOAuthRecord(bucket, b.oauthKey, serverKey, record)
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
		record, err := getOAuthRecordTx(bucket, b.oauthKey, serverKey)
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
		return putOAuthRecord(bucket, b.oauthKey, serverKey, record)
	})
	return saved && err == nil, err
}

// boundClearTx runs the checks every instance-originated destructive OAuth
// operation shares (review R6.2, R7.3): server's row carries binding, the
// record exists, was written under the binding's epoch, and still has the
// revision observedRev the caller based its decision on. It returns the
// record when all hold, nil otherwise.
func boundClearTx(tx *bbolt.Tx, k *tokenseal.Key, serverKey, server string, binding commitview.CredentialBinding, observedRev uint64) (*OAuthTokenRecord, error) {
	allowed, _, err := oauthWriteAllowedTx(tx, server, binding)
	if err != nil || !allowed {
		return nil, err
	}
	rec, err := getOAuthRecordTx(tx.Bucket([]byte(OAuthTokenBucket)), k, serverKey)
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
		rec, err := boundClearTx(tx, b.oauthKey, serverKey, server, binding, observedRev)
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
		rec, err := boundClearTx(tx, b.oauthKey, serverKey, server, binding, observedRev)
		if err != nil || rec == nil {
			return err
		}
		rec.ClientID = ""
		rec.ClientSecret = ""
		rec.CallbackPort = 0
		rec.RedirectURI = ""
		rec.Updated = time.Now()
		cleared = true
		return putOAuthRecord(tx.Bucket([]byte(OAuthTokenBucket)), b.oauthKey, serverKey, rec)
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
		rec, err := getOAuthRecordTx(bucket, b.oauthKey, serverKey)
		if err != nil || rec == nil || !OAuthRecordVisible(rec, binding) || rec.ClientID != expectedClientID {
			return err
		}
		rec.ClientID = ""
		rec.ClientSecret = ""
		rec.CallbackPort = 0
		rec.RedirectURI = ""
		rec.Updated = time.Now()
		cleared = true
		return putOAuthRecord(bucket, b.oauthKey, serverKey, rec)
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
