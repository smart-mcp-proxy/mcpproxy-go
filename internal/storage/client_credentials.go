package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"go.etcd.io/bbolt"
)

// AgentTokenPendingBucket indexes a staged rotation's NEW secret hash to the
// PRIMARY (current) hash key of the client-credential record it belongs to
// (FR-021a, data-model.md §3/§9): both the old and the pending secret must
// authenticate while a rotation is staged, and the primary record moves
// under the AgentTokensBucket key only at finalize.
const AgentTokenPendingBucket = "agent_token_pending" //nolint:gosec // bucket name, not a credential

// Sentinel errors for the client-credential mint/rotation surface (FR-021,
// FR-021a).
var (
	// ErrClientCredentialConflict is returned when the token name
	// "client-<id>" is already held by a GRANDFATHERED kind=agent token
	// (research D8). Minting never touches such a record.
	ErrClientCredentialConflict = errors.New("token name is held by a regular agent token")

	// ErrClientCredentialActive is returned by MintClientCredential when an
	// ACTIVE (non-revoked, non-expired) kind=client record already holds the
	// name: minting over an active credential is a staged rotation
	// (StageClientCredentialRotation), never an immediate replace.
	ErrClientCredentialActive = errors.New("client credential is active; use rotation instead of mint")

	// ErrClientCredentialNotFound is returned by rotation/forget operations
	// when no client-<id> record exists at all.
	ErrClientCredentialNotFound = errors.New("client credential not found")

	// ErrClientCredentialNotRotating is returned by FinalizeClientCredentialRotation
	// when no rotation is staged. Callers treat this as an idempotent no-op
	// success (a second finalize is a 200 no-op), not a hard error.
	ErrClientCredentialNotRotating = errors.New("client credential has no rotation in progress")
)

// findClientTokenRecordLocked resolves the token record currently named
// "client-"+clientID, regardless of its Kind — the caller distinguishes a
// grandfathered kind=agent record (conflict) from a kind=client one. Mirrors
// findAgentTokenHashLocked's ownerless scan; client credentials are always
// ownerless (UserID == ""), matching the connect surface they are minted
// from.
func findClientTokenRecordLocked(tx *bbolt.Tx, clientID string) (hash []byte, token *auth.AgentToken, err error) {
	tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
	if tokenBucket == nil {
		return nil, nil, nil
	}
	name := auth.ClientTokenName(clientID)
	c := tokenBucket.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		var t auth.AgentToken
		if unmarshalErr := json.Unmarshal(v, &t); unmarshalErr != nil {
			continue // skip unparseable rows, matching findAgentTokenHashLocked
		}
		if t.Name == name {
			hashCopy := append([]byte(nil), k...)
			return hashCopy, &t, nil
		}
	}
	return nil, nil, nil
}

// MintClientCredential mints a fresh mcp_cli_ credential for clientID bound
// to (mode, pin) with the given expiry (FR-021). It replaces an existing
// revoked/expired kind=client record for the same clientID IN THE SAME
// TRANSACTION (names stay reserved by soft-revoked records); it never
// touches a kind=agent record (ErrClientCredentialConflict); minting over an
// ACTIVE client record is refused with ErrClientCredentialActive — the
// caller must use StageClientCredentialRotation/FinalizeClientCredentialRotation
// instead.
func (m *Manager) MintClientCredential(clientID, rawToken string, hmacKey []byte, mode, pin string, expiresAt time.Time) (*auth.AgentToken, error) {
	if !auth.ValidClientID(clientID) {
		return nil, fmt.Errorf("invalid client id %q", clientID)
	}
	if mode != auth.ProfileModeLocked && mode != auth.ProfileModeSwitchable {
		return nil, fmt.Errorf("invalid profile mode %q", mode)
	}
	if mode == auth.ProfileModeLocked && pin == "" {
		return nil, fmt.Errorf("a locked client credential requires a non-empty profile")
	}

	hash := auth.HashToken(rawToken, hmacKey)
	now := time.Now().UTC()

	token := auth.AgentToken{
		Name:           auth.ClientTokenName(clientID),
		Kind:           auth.KindClient,
		ClientID:       clientID,
		ProfileMode:    mode,
		ProfilePin:     pin,
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
		ExpiresAt:      expiresAt,
		CreatedAt:      now,
		ConnectedAt:    &now,
		TokenHash:      hash,
		TokenPrefix:    auth.TokenPrefix(rawToken),
	}
	if err := auth.ValidateTokenInvariants(&token, auth.KindClient); err != nil {
		return nil, fmt.Errorf("mint produced an invalid client credential record: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		tokenBucket, err := tx.CreateBucketIfNotExists([]byte(AgentTokensBucket))
		if err != nil {
			return fmt.Errorf("failed to create agent_tokens bucket: %w", err)
		}

		existingHash, existing, err := findClientTokenRecordLocked(tx, clientID)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.Kind != auth.KindClient {
				return ErrClientCredentialConflict
			}
			if !existing.IsRevoked() && !existing.IsExpired() {
				return ErrClientCredentialActive
			}
			// Soft-revoked/expired: replace in the same transaction. The name
			// stays reserved by the record we are about to write.
			if err := tokenBucket.Delete(existingHash); err != nil {
				return fmt.Errorf("failed to delete stale client credential: %w", err)
			}
			pendingBucket := tx.Bucket([]byte(AgentTokenPendingBucket))
			if pendingBucket != nil && existing.PendingHash != "" {
				_ = pendingBucket.Delete([]byte(existing.PendingHash))
			}
		}

		count := tokenBucket.Stats().KeyN
		if count >= auth.MaxTokens {
			return ErrAgentTokenLimitReached
		}

		data, err := json.Marshal(token)
		if err != nil {
			return fmt.Errorf("failed to marshal client credential: %w", err)
		}
		return tokenBucket.Put([]byte(hash), data)
	})
	if err != nil {
		return nil, err
	}
	return &token, nil
}

// StageClientCredentialRotation begins a staged rotation (FR-021a) over the
// ACTIVE client credential named "client-"+clientID: it adds a pending
// secret while leaving the current one valid, so an in-flight config write
// on the client side is never invalidated mid-flight. Both the old and the
// new secret authenticate until FinalizeClientCredentialRotation (or a
// caller-driven rollback) resolves it.
func (m *Manager) StageClientCredentialRotation(clientID, newRawToken string, hmacKey []byte) (*auth.AgentToken, error) {
	if !auth.ValidClientID(clientID) {
		return nil, fmt.Errorf("invalid client id %q", clientID)
	}
	pendingHash := auth.HashToken(newRawToken, hmacKey)
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	var updated *auth.AgentToken
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return ErrClientCredentialNotFound
		}
		primaryHash, existing, err := findClientTokenRecordLocked(tx, clientID)
		if err != nil {
			return err
		}
		if existing == nil {
			return ErrClientCredentialNotFound
		}
		if existing.Kind != auth.KindClient {
			return ErrClientCredentialConflict
		}
		if existing.IsRevoked() || existing.IsExpired() {
			return ErrClientCredentialActive // wrong direction: nothing active to rotate
		}

		pendingBucket, err := tx.CreateBucketIfNotExists([]byte(AgentTokenPendingBucket))
		if err != nil {
			return fmt.Errorf("failed to create agent_token_pending bucket: %w", err)
		}
		// Drop any previously staged pending hash before staging a new one,
		// so a second reconnect before finalize never leaves a dangling
		// pending-index entry.
		if existing.PendingHash != "" {
			_ = pendingBucket.Delete([]byte(existing.PendingHash))
		}

		existing.PendingHash = pendingHash
		existing.PendingPrefix = auth.TokenPrefix(newRawToken)
		existing.RotationStartedAt = &now

		data, err := json.Marshal(existing)
		if err != nil {
			return fmt.Errorf("failed to marshal client credential: %w", err)
		}
		if err := tokenBucket.Put(primaryHash, data); err != nil {
			return fmt.Errorf("failed to store staged rotation: %w", err)
		}
		if err := pendingBucket.Put([]byte(pendingHash), primaryHash); err != nil {
			return fmt.Errorf("failed to index pending hash: %w", err)
		}
		updated = existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// FinalizeClientCredentialRotation promotes the staged pending secret to
// primary and drops the old one. Idempotent: a second finalize (no rotation
// in progress) returns the current record and ErrClientCredentialNotRotating,
// which callers MUST treat as a 200 no-op, never a hard failure.
func (m *Manager) FinalizeClientCredentialRotation(clientID string) (*auth.AgentToken, error) {
	if !auth.ValidClientID(clientID) {
		return nil, fmt.Errorf("invalid client id %q", clientID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var result *auth.AgentToken
	var notRotating bool
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return ErrClientCredentialNotFound
		}
		primaryHash, existing, err := findClientTokenRecordLocked(tx, clientID)
		if err != nil {
			return err
		}
		if existing == nil {
			return ErrClientCredentialNotFound
		}
		if existing.PendingHash == "" {
			result = existing
			notRotating = true
			return nil
		}

		pendingBucket := tx.Bucket([]byte(AgentTokenPendingBucket))
		newHash := existing.PendingHash
		existing.TokenHash = newHash
		existing.TokenPrefix = existing.PendingPrefix
		existing.PendingHash = ""
		existing.PendingPrefix = ""
		existing.RotationStartedAt = nil

		data, err := json.Marshal(existing)
		if err != nil {
			return fmt.Errorf("failed to marshal client credential: %w", err)
		}
		if err := tokenBucket.Delete(primaryHash); err != nil {
			return fmt.Errorf("failed to delete old primary hash: %w", err)
		}
		if err := tokenBucket.Put([]byte(newHash), data); err != nil {
			return fmt.Errorf("failed to store finalized client credential: %w", err)
		}
		if pendingBucket != nil {
			_ = pendingBucket.Delete([]byte(newHash))
		}
		result = existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	if notRotating {
		return result, ErrClientCredentialNotRotating
	}
	return result, nil
}

// RollbackClientCredentialRotation discards a staged pending secret,
// keeping the old one valid — the reconciler path for "config write failed,
// old secret still in the client's config" (FR-021a). Idempotent: a no-op
// when no rotation is in progress.
func (m *Manager) RollbackClientCredentialRotation(clientID string) (*auth.AgentToken, error) {
	if !auth.ValidClientID(clientID) {
		return nil, fmt.Errorf("invalid client id %q", clientID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var result *auth.AgentToken
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return ErrClientCredentialNotFound
		}
		primaryHash, existing, err := findClientTokenRecordLocked(tx, clientID)
		if err != nil {
			return err
		}
		if existing == nil {
			return ErrClientCredentialNotFound
		}
		if existing.PendingHash == "" {
			result = existing
			return nil
		}
		pendingBucket := tx.Bucket([]byte(AgentTokenPendingBucket))
		if pendingBucket != nil {
			_ = pendingBucket.Delete([]byte(existing.PendingHash))
		}
		existing.PendingHash = ""
		existing.PendingPrefix = ""
		existing.RotationStartedAt = nil

		data, err := json.Marshal(existing)
		if err != nil {
			return fmt.Errorf("failed to marshal client credential: %w", err)
		}
		if err := tokenBucket.Put(primaryHash, data); err != nil {
			return fmt.Errorf("failed to store rolled-back client credential: %w", err)
		}
		result = existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getAgentTokenByPendingHashLocked resolves a raw secret's hash through the
// pending-hash index to the record it is staged against, verifying the
// record's own PendingHash still agrees (defends against a stale index entry
// left by a crash between the two writes in StageClientCredentialRotation —
// they are in the same transaction, so this should not happen, but the
// caller (ValidateAgentToken) must fail closed rather than trust the index
// alone).
func (m *Manager) getAgentTokenByPendingHashLocked(pendingHash string) (*auth.AgentToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var token *auth.AgentToken
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		pendingBucket := tx.Bucket([]byte(AgentTokenPendingBucket))
		if pendingBucket == nil {
			return nil
		}
		primaryHash := pendingBucket.Get([]byte(pendingHash))
		if primaryHash == nil {
			return nil
		}
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return nil
		}
		data := tokenBucket.Get(primaryHash)
		if data == nil {
			return nil
		}
		var t auth.AgentToken
		if err := json.Unmarshal(data, &t); err != nil {
			return fmt.Errorf("failed to unmarshal agent token: %w", err)
		}
		if t.PendingHash != pendingHash {
			return nil // stale index entry; do not authenticate against it
		}
		token = &t
		return nil
	})
	return token, err
}

// ForgetClientCredential revokes the client credential named
// "client-"+clientID (disconnect, FR-030 change=forget). Returns
// ErrClientCredentialNotFound when no such record exists.
func (m *Manager) ForgetClientCredential(clientID string) (*auth.AgentToken, error) {
	if !auth.ValidClientID(clientID) {
		return nil, fmt.Errorf("invalid client id %q", clientID)
	}
	name := auth.ClientTokenName(clientID)
	tok, err := m.GetAgentTokenByName(name)
	if err != nil {
		return nil, err
	}
	if tok == nil || tok.Kind != auth.KindClient {
		return nil, ErrClientCredentialNotFound
	}
	if err := m.RevokeAgentToken(name); err != nil {
		return nil, err
	}
	tok.Revoked = true
	return tok, nil
}
