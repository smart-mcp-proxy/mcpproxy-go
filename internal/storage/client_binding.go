package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 132 research R4: a client's binding (profile pin + mode) lives in the
// API-key record (agent_tokens, Kind=client) and, for a client with browser
// logins, in an oauth_client_bindings entry. writeClientBindingTx is the ONE
// place that writes a client's pin/mode into either store, so the two never
// drift; every writer of a binding calls it inside its own transaction.

func bindingRevision(clientID, pin, mode string) string {
	sum := sha256.Sum256([]byte(clientID + "|" + pin + "|" + mode))
	return hex.EncodeToString(sum[:8])
}

// putOAuthBindingTx is the only writer of an oauth_client_bindings entry: it
// stamps a fresh WriteID on every write.
func putOAuthBindingTx(tx *bbolt.Tx, clientID string, b *OAuthClientBinding) error {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Errorf("failed to generate binding write id: %w", err)
	}
	b.WriteID = hex.EncodeToString(raw[:])
	return oauthPutJSON(tx, OAuthClientBindingsBucket, clientID, b)
}

// validateClientBinding: mode is locked or switchable, and locked needs a pin.
func validateClientBinding(pin, mode string) error {
	if mode != auth.ProfileModeLocked && mode != auth.ProfileModeSwitchable {
		return fmt.Errorf("invalid profile mode %q", mode)
	}
	if mode == auth.ProfileModeLocked && pin == "" {
		return fmt.Errorf("a locked client binding requires a non-empty profile")
	}
	return nil
}

// writeClientBindingTx writes (pin, mode) on the client's API-key record when
// there is one AND on its oauth_client_bindings entry when there is one, and
// fails with ErrClientCredentialNotFound only when NEITHER exists. It never
// creates an oauth_client_bindings entry by itself and never touches the
// record's PriorProfile* snapshot.
func writeClientBindingTx(tx *bbolt.Tx, clientID, pin, mode string) error {
	return writeClientBindingOptsTx(tx, clientID, pin, mode, false)
}

// writeClientBindingOptsTx is writeClientBindingTx with the option to clear
// the API-key record's PriorProfile* snapshot in the same write (a binding
// change makes the mint-time snapshot stale).
func writeClientBindingOptsTx(tx *bbolt.Tx, clientID, pin, mode string, clearPrior bool) error {
	// Validation applies only where a browser-login copy would be written: a
	// client with just an API-key record is written exactly as before Spec 132
	// (one malformed legacy tombstone must not block a profile rename). A client
	// with an OAuth entry is validated before ANY write, so neither copy can be
	// left half-updated.
	b, err := getOAuthBindingTx(tx, clientID)
	if err != nil {
		return err
	}
	if b != nil {
		if err := validateClientBinding(pin, mode); err != nil {
			return err
		}
	}
	found := false
	hash, tok, err := findClientTokenRecordLocked(tx, clientID)
	if err != nil {
		return err
	}
	if tok != nil && tok.Kind == auth.KindClient {
		tok.ProfilePin, tok.ProfileMode = pin, mode
		if clearPrior {
			tok.PriorProfilePin, tok.PriorProfileMode = "", ""
		}
		data, err := json.Marshal(tok)
		if err != nil {
			return fmt.Errorf("failed to marshal client credential: %w", err)
		}
		if err := tx.Bucket([]byte(AgentTokensBucket)).Put(hash, data); err != nil {
			return fmt.Errorf("failed to store client binding: %w", err)
		}
		found = true
	}
	if b != nil {
		b.ProfilePin, b.ProfileMode, b.UpdatedAt = pin, mode, time.Now().UTC()
		if err := putOAuthBindingTx(tx, clientID, b); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return ErrClientCredentialNotFound
	}
	return nil
}

// RepairOAuthClientBindings makes every client that has both an API-key record
// and an oauth_client_bindings entry agree, taking the API-key record as the
// truth. It returns the ids it repaired (the caller logs them).
//
// An API-key binding that is itself invalid (unknown mode, locked without a
// pin) is NOT copied: that client is skipped and returned in `skipped`.
func (m *Manager) RepairOAuthClientBindings() (repaired, skipped []string, err error) {
	type fix struct{ id, pin, mode string }
	collect := func(tx *bbolt.Tx) (todo []fix, skip []string, err error) {
		err = oauthForEach(tx, OAuthClientBindingsBucket, func(id string, b OAuthClientBinding) error {
			_, tok, err := findClientTokenRecordLocked(tx, id)
			if err != nil {
				return err
			}
			if tok == nil || tok.Kind != auth.KindClient {
				return nil
			}
			if tok.ProfilePin == b.ProfilePin && tok.ProfileMode == b.ProfileMode {
				return nil
			}
			if validateClientBinding(tok.ProfilePin, tok.ProfileMode) != nil {
				skip = append(skip, id)
				return nil
			}
			todo = append(todo, fix{id, tok.ProfilePin, tok.ProfileMode})
			return nil
		})
		return todo, skip, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// View first: with nothing to repair no write transaction is opened.
	var skip []string
	needWrite := false
	if err := m.db.db.View(func(tx *bbolt.Tx) error {
		todo, s, err := collect(tx)
		needWrite, skip = len(todo) > 0, s
		return err
	}); err != nil {
		return nil, nil, err
	}
	if !needWrite {
		return nil, skip, nil
	}
	err = m.db.db.Update(func(tx *bbolt.Tx) error {
		repaired = nil
		todo, s, err := collect(tx)
		if err != nil {
			return err
		}
		skip = s
		for _, r := range todo {
			if err := writeClientBindingTx(tx, r.id, r.pin, r.mode); err != nil {
				return err
			}
			repaired = append(repaired, r.id)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return repaired, skip, nil
}

// forgetClientLoginsTx deletes every grant record of the client with its
// access index entries, the client's oauth_client_bindings entry, and the
// registrations left with no grant record. It reports how many grants it deleted.
func forgetClientLoginsTx(tx *bbolt.Tx, clientID string) (int, error) {
	gb := tx.Bucket([]byte(OAuthGrantsBucket))
	n := 0
	if gb != nil {
		var mine []OAuthGrant
		regStays := map[string]bool{}
		if err := oauthForEach(tx, OAuthGrantsBucket, func(_ string, g OAuthGrant) error {
			if g.Client == clientID {
				mine = append(mine, g)
			} else {
				regStays[g.RegistrationID] = true
			}
			return nil
		}); err != nil {
			return 0, err
		}
		rb := tx.Bucket([]byte(OAuthRegistrationsBucket))
		for _, g := range mine {
			if err := gb.Delete([]byte(g.GrantID)); err != nil {
				return 0, err
			}
			if err := deleteAccessIndexTx(tx, g.AccessHash); err != nil {
				return 0, err
			}
			if rb != nil && !regStays[g.RegistrationID] {
				if err := rb.Delete([]byte(g.RegistrationID)); err != nil {
					return 0, err
				}
			}
		}
		n = len(mine)
	}
	if bb := tx.Bucket([]byte(OAuthClientBindingsBucket)); bb != nil {
		if err := bb.Delete([]byte(clientID)); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// ForgetClientLogins deletes the client's browser logins and binding entry in
// one transaction (see forgetClientLoginsTx). It never touches the API-key
// record. It reports how many grants it deleted.
func (m *Manager) ForgetClientLogins(clientID string) (int, error) {
	if !auth.ValidClientID(clientID) {
		return 0, fmt.Errorf("invalid client id %q", clientID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		var err error
		n, err = forgetClientLoginsTx(tx, clientID)
		return err
	})
	return n, err
}

// clientHasLoginRecordsTx reports whether the client has a binding entry or a
// grant record.
func clientHasLoginRecordsTx(tx *bbolt.Tx, clientID string) (bool, error) {
	b, err := getOAuthBindingTx(tx, clientID)
	if err != nil || b != nil {
		return b != nil, err
	}
	found := false
	err = oauthForEach(tx, OAuthGrantsBucket, func(_ string, g OAuthGrant) error {
		if g.Client == clientID {
			found = true
		}
		return nil
	})
	return found, err
}

// revokeClientAPIKeyTx soft-revokes the client's kind=client record (the
// exact effect of ForgetClientCredential) and returns it; nil when there is none.
func revokeClientAPIKeyTx(tx *bbolt.Tx, clientID string) (*auth.AgentToken, error) {
	primaryHash, tok, err := findClientTokenRecordLocked(tx, clientID)
	if err != nil {
		return nil, err
	}
	if tok == nil || tok.Kind != auth.KindClient {
		return nil, nil
	}
	markRevoked(tok, time.Now())
	tok.PriorProfilePin, tok.PriorProfileMode = "", ""
	data, err := json.Marshal(tok)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal client credential: %w", err)
	}
	if err := tx.Bucket([]byte(AgentTokensBucket)).Put(primaryHash, data); err != nil {
		return nil, fmt.Errorf("failed to store revoked client credential: %w", err)
	}
	return tok, nil
}

// ForgetClient is the client FORGET: it revokes the API-key record the way
// ForgetClientCredential does (the record stays as a revoked tombstone that
// reserves the name) and, in the same transaction, deletes every browser login,
// the OAuth binding entry and the registrations left without a grant. The
// returned record is nil for a client that had only browser logins.
// ErrClientCredentialNotFound means the client had nothing at all.
func (m *Manager) ForgetClient(clientID string) (*auth.AgentToken, error) {
	if !auth.ValidClientID(clientID) {
		return nil, fmt.Errorf("invalid client id %q", clientID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var revoked *auth.AgentToken
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		hadLogins, err := clientHasLoginRecordsTx(tx, clientID)
		if err != nil {
			return err
		}
		revoked, err = revokeClientAPIKeyTx(tx, clientID)
		if err != nil {
			return err
		}
		if revoked == nil && !hadLogins {
			return ErrClientCredentialNotFound
		}
		_, err = forgetClientLoginsTx(tx, clientID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return revoked, nil
}

// RevokeClient is the client REVOKE: the API-key record (when there is one) is
// revoked as ForgetClientCredential does, and every grant of the client is
// marked revoked with reason client_revoked. Everything is kept for display.
// The returned record is nil for a client with only browser logins.
// ErrClientCredentialNotFound means the client had nothing at all.
func (m *Manager) RevokeClient(clientID string, now time.Time) (*auth.AgentToken, error) {
	if !auth.ValidClientID(clientID) {
		return nil, fmt.Errorf("invalid client id %q", clientID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var revoked *auth.AgentToken
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		hadLogins, err := clientHasLoginRecordsTx(tx, clientID)
		if err != nil {
			return err
		}
		revoked, err = revokeClientAPIKeyTx(tx, clientID)
		if err != nil {
			return err
		}
		if revoked == nil && !hadLogins {
			return ErrClientCredentialNotFound
		}
		var mine []OAuthGrant
		if err := oauthForEach(tx, OAuthGrantsBucket, func(_ string, g OAuthGrant) error {
			if g.Client == clientID {
				mine = append(mine, g)
			}
			return nil
		}); err != nil {
			return err
		}
		for i := range mine {
			if _, err := revokeGrantTx(tx, &mine[i], OAuthRevokeClientRevoked, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return revoked, nil
}

// RevokeClientAPIKey revokes the API-key record only; grants and the OAuth
// binding entry are untouched. It is ForgetClientCredential under a name that
// says what it does now that a client can hold other credentials.
func (m *Manager) RevokeClientAPIKey(clientID string) (*auth.AgentToken, error) {
	return m.forgetClientCredential(clientID, false)
}
