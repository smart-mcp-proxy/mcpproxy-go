package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"go.etcd.io/bbolt"
)

// Bucket names for agent token storage.
const (
	AgentTokensBucket = "agent_tokens" //nolint:gosec // bucket name, not a credential
	// AgentTokenNamesBucket is a LEGACY, owner-blind name->hash index keyed by
	// the bare token name. It is maintained only for tokens with no owner
	// (UserID == ""), i.e. every personal-edition token, so the personal
	// edition is byte-identical on disk and a rollback sees exactly what it
	// wrote. Owner-scoped lookups never consult it — see
	// findAgentTokenHashLocked. Entries left behind by a pre-upgrade
	// server-edition deployment are simply not read; they are deliberately NOT
	// swept, because sweeping them would strand those tokens on a rollback
	// while every by-name path on the old code already re-checks ownership and
	// can therefore only deny, never act on the wrong tenant's token.
	AgentTokenNamesBucket = "agent_token_names" //nolint:gosec // bucket name, not a credential
)

// Sentinel errors returned by CreateAgentToken so callers can classify the
// failure without matching on error strings (and without echoing a storage
// message that would disclose another tenant's token name).
var (
	// ErrAgentTokenNameExists is returned when the OWNER already has a token
	// with that name. Names are scoped per owner, so this never fires because
	// of a different tenant's token.
	ErrAgentTokenNameExists = errors.New("agent token with this name already exists")

	// ErrAgentTokenLimitReached is returned when the deployment-wide token cap
	// is reached.
	ErrAgentTokenLimitReached = errors.New("maximum number of agent tokens reached")

	// ErrAgentTokenNotFound is returned by the owner-scoped mutators when the
	// (owner, name) pair resolves to nothing. Callers MUST NOT distinguish
	// "absent" from "owned by someone else" in their response: the lookup is
	// owner-scoped, so both produce this same error.
	ErrAgentTokenNotFound = errors.New("agent token not found")
)

// findAgentTokenHashLocked resolves an (owner, name) pair to the hash key of
// the authoritative record inside the given transaction. It scans the
// agent_tokens bucket, which always carries both Name and UserID, rather than
// consulting the legacy owner-blind name index. Returns (nil, nil) when the
// pair does not resolve.
//
// A scan is correct and cheap here: the bucket is capped at auth.MaxTokens
// entries and only low-frequency management operations resolve by name (the
// authentication hot path resolves by hash). It is also constant-time with
// respect to ownership — the whole bucket is walked regardless — so it adds no
// timing oracle for "does another tenant own this name?".
//
// The caller must complete this scan before mutating the bucket: bbolt forbids
// mutating a bucket while iterating it.
func findAgentTokenHashLocked(tx *bbolt.Tx, userID, name string) ([]byte, *auth.AgentToken, error) {
	tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
	if tokenBucket == nil {
		return nil, nil, nil
	}

	var (
		foundHash  []byte
		foundToken *auth.AgentToken
	)

	err := tokenBucket.ForEach(func(k, v []byte) error {
		if foundToken != nil {
			return nil
		}
		var token auth.AgentToken
		if err := json.Unmarshal(v, &token); err != nil {
			return fmt.Errorf("failed to unmarshal agent token: %w", err)
		}
		if token.Name != name || token.UserID != userID {
			return nil
		}
		// bbolt page memory is only valid for the life of the transaction and
		// the key may be reused; copy it before returning.
		foundHash = append([]byte(nil), k...)
		foundToken = &token
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return foundHash, foundToken, nil
}

// CreateAgentToken stores a new agent token. It hashes the raw token using
// the provided HMAC key and stores the AgentToken record keyed by hash in the
// "agent_tokens" bucket.
//
// Token names are unique PER OWNER (token.UserID), not globally: two tenants
// can each hold a token called "ci". Ownerless tokens (UserID == "", every
// personal-edition token) additionally get a bare-name entry in the legacy
// "agent_token_names" index so the personal edition is unchanged on disk.
//
// Returns ErrAgentTokenNameExists if the same owner already has that name, or
// ErrAgentTokenLimitReached if the deployment-wide cap is reached.
func (m *Manager) CreateAgentToken(token auth.AgentToken, rawToken string, hmacKey []byte) error {
	if token.Name == "" {
		return fmt.Errorf("agent token name cannot be empty")
	}

	hash := auth.HashToken(rawToken, hmacKey)
	token.TokenHash = hash
	token.TokenPrefix = auth.TokenPrefix(rawToken)

	if token.CreatedAt.IsZero() {
		token.CreatedAt = time.Now().UTC()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		tokenBucket, err := tx.CreateBucketIfNotExists([]byte(AgentTokensBucket))
		if err != nil {
			return fmt.Errorf("failed to create agent_tokens bucket: %w", err)
		}

		nameBucket, err := tx.CreateBucketIfNotExists([]byte(AgentTokenNamesBucket))
		if err != nil {
			return fmt.Errorf("failed to create agent_token_names bucket: %w", err)
		}

		// Check for a duplicate name WITHIN THIS OWNER's namespace. Scanning
		// must finish before any Put below: bbolt forbids mutating a bucket
		// while it is being iterated.
		existingHash, _, err := findAgentTokenHashLocked(tx, token.UserID, token.Name)
		if err != nil {
			return err
		}
		if existingHash != nil {
			return ErrAgentTokenNameExists
		}

		// Enforce max token limit
		count := tokenBucket.Stats().KeyN
		if count >= auth.MaxTokens {
			return ErrAgentTokenLimitReached
		}

		// Marshal and store
		data, err := json.Marshal(token)
		if err != nil {
			return fmt.Errorf("failed to marshal agent token: %w", err)
		}

		if err := tokenBucket.Put([]byte(hash), data); err != nil {
			return fmt.Errorf("failed to store agent token: %w", err)
		}

		// Maintain the legacy owner-blind index only for ownerless tokens, so
		// the personal edition is byte-identical. A user-owned name must never
		// claim the global slot, or one tenant's name would shadow another's.
		if token.UserID == "" {
			if err := nameBucket.Put([]byte(token.Name), []byte(hash)); err != nil {
				return fmt.Errorf("failed to store agent token name mapping: %w", err)
			}
		}

		return nil
	})
}

// GetAgentTokenByName retrieves an OWNERLESS agent token by its name — that
// is, GetAgentTokenByOwnerAndName("", name). Every personal-edition token is
// ownerless, so this is unchanged for the personal edition; it deliberately
// does NOT resolve a server-edition user's token, because token names are
// unique only within an owner and a bare name is therefore ambiguous.
// Returns nil if not found.
func (m *Manager) GetAgentTokenByName(name string) (*auth.AgentToken, error) {
	return m.GetAgentTokenByOwnerAndName("", name)
}

// GetAgentTokenByOwnerAndName retrieves the token called name owned by userID.
// Use "" as userID for ownerless (personal-edition) tokens.
//
// Returns nil when the pair does not resolve — whether because no such token
// exists or because the name belongs to a different owner. Callers must not
// distinguish those two cases in a response: doing so turns the endpoint into
// an oracle for other tenants' token names.
func (m *Manager) GetAgentTokenByOwnerAndName(userID, name string) (*auth.AgentToken, error) {
	if name == "" {
		return nil, fmt.Errorf("agent token name cannot be empty")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var token *auth.AgentToken

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		_, found, err := findAgentTokenHashLocked(tx, userID, name)
		if err != nil {
			return err
		}
		token = found
		return nil
	})

	return token, err
}

// GetAgentTokenByHash retrieves an agent token by its HMAC hash.
// Returns nil if not found.
func (m *Manager) GetAgentTokenByHash(hash string) (*auth.AgentToken, error) {
	if hash == "" {
		return nil, fmt.Errorf("agent token hash cannot be empty")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var token *auth.AgentToken

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return nil
		}

		data := tokenBucket.Get([]byte(hash))
		if data == nil {
			return nil
		}

		token = &auth.AgentToken{}
		if err := json.Unmarshal(data, token); err != nil {
			return fmt.Errorf("failed to unmarshal agent token: %w", err)
		}

		return nil
	})

	return token, err
}

// ListAgentTokens returns all stored agent tokens.
func (m *Manager) ListAgentTokens() ([]auth.AgentToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var tokens []auth.AgentToken

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return nil
		}

		return tokenBucket.ForEach(func(k, v []byte) error {
			var token auth.AgentToken
			if err := json.Unmarshal(v, &token); err != nil {
				return fmt.Errorf("failed to unmarshal agent token: %w", err)
			}
			tokens = append(tokens, token)
			return nil
		})
	})

	return tokens, err
}

// RevokeAgentToken marks an OWNERLESS agent token as revoked by name.
// Equivalent to RevokeAgentTokenForOwner("", name).
func (m *Manager) RevokeAgentToken(name string) error {
	return m.RevokeAgentTokenForOwner("", name)
}

// RevokeAgentTokenForOwner marks the token called name owned by userID as
// revoked. Returns ErrAgentTokenNotFound when the (owner, name) pair does not
// resolve, whether because it is absent or because it belongs to someone else.
func (m *Manager) RevokeAgentTokenForOwner(userID, name string) error {
	if name == "" {
		return fmt.Errorf("agent token name cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		// Resolve first; the scan must finish before the Put below.
		hash, token, err := findAgentTokenHashLocked(tx, userID, name)
		if err != nil {
			return err
		}
		if token == nil {
			return ErrAgentTokenNotFound
		}

		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return ErrAgentTokenNotFound
		}

		token.Revoked = true

		updatedData, err := json.Marshal(token)
		if err != nil {
			return fmt.Errorf("failed to marshal agent token: %w", err)
		}

		return tokenBucket.Put(hash, updatedData)
	})
}

// DeleteAgentToken permanently removes an agent token, deleting both the record
// in the "agent_tokens" bucket and the name->hash mapping in "agent_token_names".
// Unlike RevokeAgentToken (a soft delete that keeps the record), this frees the
// name so a new token can be created with the same name. Returns an error if the
// token does not exist.
func (m *Manager) DeleteAgentToken(name string) error {
	return m.DeleteAgentTokenForOwner("", name)
}

// DeleteAgentTokenForOwner permanently removes the token called name owned by
// userID. Returns ErrAgentTokenNotFound when the (owner, name) pair does not
// resolve, whether because it is absent or because it belongs to someone else.
func (m *Manager) DeleteAgentTokenForOwner(userID, name string) error {
	if name == "" {
		return fmt.Errorf("agent token name cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		// Resolve first; the scan must finish before any Delete below.
		hash, token, err := findAgentTokenHashLocked(tx, userID, name)
		if err != nil {
			return err
		}
		if token == nil {
			return ErrAgentTokenNotFound
		}

		// Drop the legacy owner-blind index entry only when it points at THIS
		// record, so a delete can never remove another owner's mapping. For an
		// ownerless token that is the entry CreateAgentToken wrote; for a
		// user-owned token it clears a stale pre-upgrade entry, if any.
		if nameBucket := tx.Bucket([]byte(AgentTokenNamesBucket)); nameBucket != nil {
			if indexed := nameBucket.Get([]byte(name)); indexed != nil && string(indexed) == string(hash) {
				if err := nameBucket.Delete([]byte(name)); err != nil {
					return fmt.Errorf("failed to delete agent token name mapping: %w", err)
				}
			}
		}

		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket != nil {
			if err := tokenBucket.Delete(hash); err != nil {
				return fmt.Errorf("failed to delete agent token: %w", err)
			}
		}

		return nil
	})
}

// RegenerateAgentToken creates a new hash for an existing token, preserving
// configuration (name, permissions, allowed servers, expiry). It removes the
// old hash entry and creates a new one with the new raw token's hash.
// Returns the updated token record.
func (m *Manager) RegenerateAgentToken(name string, newRawToken string, hmacKey []byte) (*auth.AgentToken, error) {
	return m.RegenerateAgentTokenForOwner("", name, newRawToken, hmacKey)
}

// RegenerateAgentTokenForOwner regenerates the token called name owned by
// userID. Returns ErrAgentTokenNotFound when the (owner, name) pair does not
// resolve, whether because it is absent or because it belongs to someone else.
func (m *Manager) RegenerateAgentTokenForOwner(userID, name string, newRawToken string, hmacKey []byte) (*auth.AgentToken, error) {
	if name == "" {
		return nil, fmt.Errorf("agent token name cannot be empty")
	}

	newHash := auth.HashToken(newRawToken, hmacKey)
	newPrefix := auth.TokenPrefix(newRawToken)

	m.mu.Lock()
	defer m.mu.Unlock()

	var updated *auth.AgentToken

	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		// Resolve first; the scan must finish before the Delete/Put below.
		oldHash, token, err := findAgentTokenHashLocked(tx, userID, name)
		if err != nil {
			return err
		}
		if token == nil {
			return ErrAgentTokenNotFound
		}

		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return ErrAgentTokenNotFound
		}

		// Remove old hash entry
		if err := tokenBucket.Delete(oldHash); err != nil {
			return fmt.Errorf("failed to delete old agent token hash: %w", err)
		}

		// Update token with new hash and prefix, clear revoked status
		token.TokenHash = newHash
		token.TokenPrefix = newPrefix
		token.Revoked = false

		updatedData, err := json.Marshal(token)
		if err != nil {
			return fmt.Errorf("failed to marshal agent token: %w", err)
		}

		// Store with new hash key
		if err := tokenBucket.Put([]byte(newHash), updatedData); err != nil {
			return fmt.Errorf("failed to store regenerated agent token: %w", err)
		}

		// Keep the legacy owner-blind index in step only where it is
		// maintained (ownerless tokens) or where it still points at this very
		// record, so one owner's regenerate can never repoint another's entry.
		if nameBucket := tx.Bucket([]byte(AgentTokenNamesBucket)); nameBucket != nil {
			indexed := nameBucket.Get([]byte(name))
			repoint := userID == "" || (indexed != nil && string(indexed) == string(oldHash))
			if repoint {
				if err := nameBucket.Put([]byte(name), []byte(newHash)); err != nil {
					return fmt.Errorf("failed to update agent token name mapping: %w", err)
				}
			}
		}

		updated = token
		return nil
	})

	return updated, err
}

// UpdateAgentTokenLastUsed updates the LastUsedAt timestamp for an OWNERLESS
// token identified by name.
//
// Deprecated: token names are unique only within an owner, so a bare name is
// ambiguous in the server edition. Authentication paths hold the validated
// record and must call UpdateAgentTokenLastUsedByHash instead.
func (m *Manager) UpdateAgentTokenLastUsed(name string) error {
	if name == "" {
		return fmt.Errorf("agent token name cannot be empty")
	}

	m.mu.RLock()
	token, err := func() (*auth.AgentToken, error) {
		defer m.mu.RUnlock()
		var found *auth.AgentToken
		err := m.db.db.View(func(tx *bbolt.Tx) error {
			_, t, err := findAgentTokenHashLocked(tx, "", name)
			found = t
			return err
		})
		return found, err
	}()
	if err != nil {
		return err
	}
	if token == nil {
		return ErrAgentTokenNotFound
	}

	return m.UpdateAgentTokenLastUsedByHash(token.TokenHash)
}

// UpdateAgentTokenLastUsedByHash updates the LastUsedAt timestamp for the token
// stored under the given HMAC hash. The hash is the authoritative, unambiguous
// key: unlike a name it is unique across owners, so this cannot stamp another
// tenant's token.
func (m *Manager) UpdateAgentTokenLastUsedByHash(hash string) error {
	if hash == "" {
		return fmt.Errorf("agent token hash cannot be empty")
	}

	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return ErrAgentTokenNotFound
		}

		data := tokenBucket.Get([]byte(hash))
		if data == nil {
			return ErrAgentTokenNotFound
		}

		var token auth.AgentToken
		if err := json.Unmarshal(data, &token); err != nil {
			return fmt.Errorf("failed to unmarshal agent token: %w", err)
		}

		token.LastUsedAt = &now

		updatedData, err := json.Marshal(token)
		if err != nil {
			return fmt.Errorf("failed to marshal agent token: %w", err)
		}

		return tokenBucket.Put([]byte(hash), updatedData)
	})
}

// GetAgentTokenCount returns the number of stored agent tokens.
func (m *Manager) GetAgentTokenCount() (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var count int

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return nil
		}
		count = tokenBucket.Stats().KeyN
		return nil
	})

	return count, err
}

// ValidateAgentToken hashes the raw token and looks it up in storage.
// Returns the token if found and valid (not expired, not revoked).
// Returns an error describing why validation failed.
func (m *Manager) ValidateAgentToken(rawToken string, hmacKey []byte) (*auth.AgentToken, error) {
	if !auth.ValidateTokenFormat(rawToken) {
		return nil, fmt.Errorf("invalid token format")
	}

	hash := auth.HashToken(rawToken, hmacKey)

	token, err := m.GetAgentTokenByHash(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to look up token: %w", err)
	}
	if token == nil {
		return nil, fmt.Errorf("token not found")
	}

	if token.IsRevoked() {
		return nil, fmt.Errorf("token has been revoked")
	}

	if token.IsExpired() {
		return nil, fmt.Errorf("token has expired")
	}

	return token, nil
}
