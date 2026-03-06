package storage

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"go.etcd.io/bbolt"
)

// Bucket names for agent token storage.
const (
	AgentTokensBucket     = "agent_tokens"      //nolint:gosec // bucket name, not a credential
	AgentTokenNamesBucket = "agent_token_names"  //nolint:gosec // bucket name, not a credential
)

// CreateAgentToken stores a new agent token. It hashes the raw token using
// the provided HMAC key, stores the AgentToken record keyed by hash in the
// "agent_tokens" bucket, and creates a name->hash mapping in "agent_token_names".
// Returns an error if the name already exists or the max token limit is reached.
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

		// Check for duplicate name
		if existing := nameBucket.Get([]byte(token.Name)); existing != nil {
			return fmt.Errorf("agent token with name %q already exists", token.Name)
		}

		// Enforce max token limit
		count := tokenBucket.Stats().KeyN
		if count >= auth.MaxTokens {
			return fmt.Errorf("maximum number of agent tokens (%d) reached", auth.MaxTokens)
		}

		// Marshal and store
		data, err := json.Marshal(token)
		if err != nil {
			return fmt.Errorf("failed to marshal agent token: %w", err)
		}

		if err := tokenBucket.Put([]byte(hash), data); err != nil {
			return fmt.Errorf("failed to store agent token: %w", err)
		}

		if err := nameBucket.Put([]byte(token.Name), []byte(hash)); err != nil {
			return fmt.Errorf("failed to store agent token name mapping: %w", err)
		}

		return nil
	})
}

// GetAgentTokenByName retrieves an agent token by its name.
// Returns nil if not found.
func (m *Manager) GetAgentTokenByName(name string) (*auth.AgentToken, error) {
	if name == "" {
		return nil, fmt.Errorf("agent token name cannot be empty")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var token *auth.AgentToken

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		nameBucket := tx.Bucket([]byte(AgentTokenNamesBucket))
		if nameBucket == nil {
			return nil
		}

		hash := nameBucket.Get([]byte(name))
		if hash == nil {
			return nil
		}

		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return nil
		}

		data := tokenBucket.Get(hash)
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

// RevokeAgentToken marks an agent token as revoked by name.
func (m *Manager) RevokeAgentToken(name string) error {
	if name == "" {
		return fmt.Errorf("agent token name cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		nameBucket := tx.Bucket([]byte(AgentTokenNamesBucket))
		if nameBucket == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		hash := nameBucket.Get([]byte(name))
		if hash == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		data := tokenBucket.Get(hash)
		if data == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		var token auth.AgentToken
		if err := json.Unmarshal(data, &token); err != nil {
			return fmt.Errorf("failed to unmarshal agent token: %w", err)
		}

		token.Revoked = true

		updatedData, err := json.Marshal(token)
		if err != nil {
			return fmt.Errorf("failed to marshal agent token: %w", err)
		}

		return tokenBucket.Put(hash, updatedData)
	})
}

// RegenerateAgentToken creates a new hash for an existing token, preserving
// configuration (name, permissions, allowed servers, expiry). It removes the
// old hash entry and creates a new one with the new raw token's hash.
// Returns the updated token record.
func (m *Manager) RegenerateAgentToken(name string, newRawToken string, hmacKey []byte) (*auth.AgentToken, error) {
	if name == "" {
		return nil, fmt.Errorf("agent token name cannot be empty")
	}

	newHash := auth.HashToken(newRawToken, hmacKey)
	newPrefix := auth.TokenPrefix(newRawToken)

	m.mu.Lock()
	defer m.mu.Unlock()

	var updated *auth.AgentToken

	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		nameBucket := tx.Bucket([]byte(AgentTokenNamesBucket))
		if nameBucket == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		oldHash := nameBucket.Get([]byte(name))
		if oldHash == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		data := tokenBucket.Get(oldHash)
		if data == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		var token auth.AgentToken
		if err := json.Unmarshal(data, &token); err != nil {
			return fmt.Errorf("failed to unmarshal agent token: %w", err)
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

		// Update name mapping to point to new hash
		if err := nameBucket.Put([]byte(name), []byte(newHash)); err != nil {
			return fmt.Errorf("failed to update agent token name mapping: %w", err)
		}

		updated = &token
		return nil
	})

	return updated, err
}

// UpdateAgentTokenLastUsed updates the LastUsedAt timestamp for a token identified by name.
func (m *Manager) UpdateAgentTokenLastUsed(name string) error {
	if name == "" {
		return fmt.Errorf("agent token name cannot be empty")
	}

	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		nameBucket := tx.Bucket([]byte(AgentTokenNamesBucket))
		if nameBucket == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		hash := nameBucket.Get([]byte(name))
		if hash == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return fmt.Errorf("agent token %q not found", name)
		}

		data := tokenBucket.Get(hash)
		if data == nil {
			return fmt.Errorf("agent token %q not found", name)
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

		return tokenBucket.Put(hash, updatedData)
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
