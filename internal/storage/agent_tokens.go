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
	// findAgentTokenHashLocked.
	//
	// Entries left behind by a pre-upgrade server-edition deployment are not
	// read, and are deliberately NOT swept: sweeping them would strand those
	// tokens on a rollback, while every by-name path on the old code already
	// re-checks ownership and can therefore only deny, never act on the wrong
	// tenant's token.
	//
	// For that rollback promise to be true, all THREE maintenance paths must
	// leave a foreign entry alone, not just the two that already did. Create
	// used to Put unconditionally, overwriting whatever the name pointed at —
	// including a pre-upgrade tenant's entry, the very thing the paragraph
	// above promises to preserve. It now claims the slot only when the slot is
	// free, or when the entry it holds dangles (its hash is no longer in
	// agent_tokens). Delete and regenerate already required the entry to point
	// at the very record being mutated.
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

	// ErrAgentTokenOwnerInactive is returned by ValidateAgentToken when a
	// token's OWNER is no longer allowed to authenticate — disabled, or gone
	// from the user store entirely. The token record itself may be perfectly
	// valid; the identity it speaks for is not.
	ErrAgentTokenOwnerInactive = errors.New("token owner is not active")

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
//
// A record that fails to unmarshal is SKIPPED, not fatal. Because this walks
// the whole bucket rather than reading one indexed record, aborting on the
// first bad row would let a single unparseable entry — a truncated write, a
// hand-edited DB, a record from a future schema — turn create, revoke, delete
// and regenerate into a 500 for EVERY tenant of the deployment, which the
// indexed read it replaced could not do. Skipping degrades the blast radius to
// "that one token is unresolvable": the corrupt row's own (owner, name) stops
// resolving, so its management operations answer not-found, and every other
// token keeps working. The skip is logged at WARN with the bucket key so an
// operator can find the row; that key is an HMAC hash, not a credential.
func (m *Manager) findAgentTokenHashLocked(tx *bbolt.Tx, userID, name string) ([]byte, *auth.AgentToken, error) {
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
			if m.logger != nil {
				m.logger.Warnw("skipping unparseable agent token record",
					"bucket", AgentTokensBucket, "key", string(k), "error", err)
			}
			return nil
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
		existingHash, _, err := m.findAgentTokenHashLocked(tx, token.UserID, token.Name)
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
		//
		// The slot is CLAIMED, not overwritten. An unconditional Put stomps
		// whatever the name already pointed at — including the pre-upgrade
		// server-edition entry the bucket comment above promises to leave
		// intact for a rollback, which on the old code is a live tenant's
		// token. A DANGLING entry (its hash no longer in agent_tokens) is fair
		// game: nothing can resolve through it, on this code or a rollback.
		// Declining the Put costs the new token nothing —
		// findAgentTokenHashLocked resolves by scan and never reads this index.
		if token.UserID == "" {
			if claimAgentTokenNameSlot(tx, nameBucket, token.Name) {
				if err := nameBucket.Put([]byte(token.Name), []byte(hash)); err != nil {
					return fmt.Errorf("failed to store agent token name mapping: %w", err)
				}
			}
		}

		return nil
	})
}

// claimAgentTokenNameSlot reports whether the legacy owner-blind name index may
// be pointed at a newly created OWNERLESS token.
//
// True when the name is unclaimed, or when the entry it holds DANGLES — its
// hash is absent from agent_tokens, so nothing resolves through it on this code
// or on a rollback. False when a live record already holds the slot, which in
// practice means a pre-upgrade server-edition tenant's token: overwriting that
// is precisely the rollback stranding the bucket comment promises not to cause.
func claimAgentTokenNameSlot(tx *bbolt.Tx, nameBucket *bbolt.Bucket, name string) bool {
	indexed := nameBucket.Get([]byte(name))
	if indexed == nil {
		return true
	}
	tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
	if tokenBucket == nil {
		return true
	}
	return tokenBucket.Get(indexed) == nil
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
		_, found, err := m.findAgentTokenHashLocked(tx, userID, name)
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
//
// A record that fails to unmarshal is SKIPPED, not fatal, for the same reason
// findAgentTokenHashLocked skips one: this walks the whole bucket, so aborting
// on the first bad row lets a single unparseable entry — a truncated write, a
// hand-edited DB, a record from a future schema — turn every listing into a
// 500 for EVERY tenant of the deployment, and for the operator's own
// `mcpproxy token list` with it. Skipping degrades the blast radius to "that
// one token is not listed". The skip is logged at WARN with the bucket key so
// an operator can find the row; that key is an HMAC hash, not a credential.
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
				if m.logger != nil {
					m.logger.Warnw("skipping unparseable agent token record",
						"bucket", AgentTokensBucket, "key", string(k), "error", err)
				}
				return nil
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
		hash, token, err := m.findAgentTokenHashLocked(tx, userID, name)
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

// RevokeAgentTokensForOwner marks every token owned by userID as revoked and
// returns how many it changed. An owner with no tokens is not an error.
//
// It is the credential half of disabling a user. The owner gate
// (SetAgentTokenOwnerGate) already stops a disabled user's tokens the moment
// they are disabled, but the gate is a live check: RE-ENABLING the account
// would hand every previously minted token straight back, including the one
// that caused the disable. Since disabling is the documented remediation for a
// compromised account, the credentials minted under it are burned outright.
// Revoked is a soft delete — the records stay, so the operator can still see
// what existed — and the owner mints new tokens after being re-enabled.
//
// userID must not be empty: "" is the OWNERLESS namespace, i.e. every
// personal-edition token, and a bulk revoke of it would be an outage rather
// than a remediation.
func (m *Manager) RevokeAgentTokensForOwner(userID string) (int, error) {
	if userID == "" {
		return 0, fmt.Errorf("agent token owner cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	revoked := 0

	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		tokenBucket := tx.Bucket([]byte(AgentTokensBucket))
		if tokenBucket == nil {
			return nil
		}

		// Collect first: bbolt forbids mutating a bucket while iterating it.
		type pending struct {
			hash  []byte
			token auth.AgentToken
		}
		var todo []pending

		err := tokenBucket.ForEach(func(k, v []byte) error {
			var token auth.AgentToken
			if err := json.Unmarshal(v, &token); err != nil {
				// Same tolerance as every other full-bucket walk here: one
				// corrupt row must not abort a security remediation for the
				// rows that ARE parseable.
				if m.logger != nil {
					m.logger.Warnw("skipping unparseable agent token record",
						"bucket", AgentTokensBucket, "key", string(k), "error", err)
				}
				return nil
			}
			if token.UserID != userID || token.Revoked {
				return nil
			}
			todo = append(todo, pending{hash: append([]byte(nil), k...), token: token})
			return nil
		})
		if err != nil {
			return err
		}

		for i := range todo {
			todo[i].token.Revoked = true
			data, err := json.Marshal(todo[i].token)
			if err != nil {
				return fmt.Errorf("failed to marshal agent token: %w", err)
			}
			if err := tokenBucket.Put(todo[i].hash, data); err != nil {
				return fmt.Errorf("failed to revoke agent token: %w", err)
			}
			revoked++
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return revoked, nil
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
		hash, token, err := m.findAgentTokenHashLocked(tx, userID, name)
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
	return m.RegenerateAgentTokenForOwner("", name, newRawToken, hmacKey, nil)
}

// RegenerateAgentTokenForOwner regenerates the token called name owned by
// userID. Returns ErrAgentTokenNotFound when the (owner, name) pair does not
// resolve, whether because it is absent or because it belongs to someone else.
//
// narrowScope, when non-nil, is applied to the stored AllowedServers inside the
// same transaction that rotates the hash, and its result is persisted. It exists
// because a token's server scope is decided once, at mint time, and nothing
// re-checks it afterwards: un-sharing a server does not revoke the grants
// already written into live tokens. Rotation is the one moment the owner's
// current entitlement is known, so the server edition passes a filter that drops
// anything they may no longer reach (see resolveTokenServerScope).
//
// The hook can only ever NARROW, and that is ENFORCED here rather than merely
// documented: whatever it returns is intersected with the token's stored
// AllowedServers by intersectAllowedServers before anything is persisted. A
// future caller that returned a wider list — or a constant, or the entitled set
// itself — therefore cannot turn rotation, the one operation that is supposed to
// be scope-neutral, into a privilege escalation. Hooks are still expected to
// behave; the intersection is what makes misbehaving harmless instead of
// load-bearing on a comment.
//
// The hook MUST NOT do I/O: it runs inside a write transaction while m.mu is
// held, so the caller computes the entitled set before the call and passes a
// pure filter.
func (m *Manager) RegenerateAgentTokenForOwner(userID, name string, newRawToken string, hmacKey []byte, narrowScope func([]string) []string) (*auth.AgentToken, error) {
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
		oldHash, token, err := m.findAgentTokenHashLocked(tx, userID, name)
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

		// Re-apply the caller's current server entitlement, if they supplied
		// one. Narrowing only, enforced by the intersection below rather than
		// trusted from the hook; see the doc comment.
		if narrowScope != nil {
			token.AllowedServers = intersectAllowedServers(token.AllowedServers, narrowScope(token.AllowedServers))
		}

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

// intersectAllowedServers returns the entries of proposed that the token's
// current scope already granted. It is the storage-side enforcement of the
// narrow-only contract on RegenerateAgentTokenForOwner's hook: the result can
// never grant a server the token could not already reach, whatever the hook
// returns.
//
// A stored "*" is treated as granting everything, so replacing it with a
// concrete list is a narrowing and survives the intersection intact. That case
// is not hypothetical: it is exactly how a token minted before per-owner
// scoping existed — carrying a bare "*" — gets its unbounded grant converted
// into a bounded one on its first rotation. Without the wildcard rule the
// intersection would empty such a token instead, quietly bricking it.
//
// Order and duplicates follow proposed, so the hook still decides the shape of
// the list it is allowed to produce.
func intersectAllowedServers(current, proposed []string) []string {
	if len(proposed) == 0 {
		return nil
	}

	granted := make(map[string]struct{}, len(current))
	wildcard := false
	for _, name := range current {
		if name == "*" {
			wildcard = true
		}
		granted[name] = struct{}{}
	}

	out := make([]string, 0, len(proposed))
	seen := make(map[string]struct{}, len(proposed))
	for _, name := range proposed {
		if !wildcard {
			if _, ok := granted[name]; !ok {
				continue
			}
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}

	if len(out) == 0 {
		return nil
	}
	return out
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
			_, t, err := m.findAgentTokenHashLocked(tx, "", name)
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

// agentTokenOwnerGate reports whether the user a token belongs to may still
// authenticate. It is consulted on the authentication hot path, so it must be
// cheap and must not block; a store lookup keyed by user id is what the server
// edition installs.
//
// It is called ONLY for owned tokens (UserID != ""), never for the personal
// edition's ownerless ones.
type agentTokenOwnerGate func(userID string) (active bool, err error)

// SetAgentTokenOwnerGate installs the predicate ValidateAgentToken consults for
// every OWNED agent token. Passing nil removes it. Safe to call at any time.
//
// It exists because a token's authorisation is decided once, when the token is
// minted, and nothing re-checks the identity behind it afterwards. Disabling a
// user is the documented remediation for a compromised account — it revokes
// their sessions and stops their JWTs — but the agent tokens they minted are
// separate records that kept authenticating, still carrying that user's UserID
// into every downstream authorisation and activity decision. The gate closes
// that: the token is only as live as its owner.
//
// It FAILS CLOSED. A gate that errors denies the token rather than falling back
// to "valid", because the failure mode of the alternative is precisely the hole
// this closes. The personal edition installs no gate and is unaffected — its
// tokens are all ownerless.
func (m *Manager) SetAgentTokenOwnerGate(gate agentTokenOwnerGate) {
	m.ownerGate.Store(gate)
}

// agentTokenOwnerActive applies the installed gate, if any. Reports true when
// no gate is installed or the token is ownerless.
func (m *Manager) agentTokenOwnerActive(userID string) (bool, error) {
	if userID == "" {
		return true, nil
	}
	v := m.ownerGate.Load()
	if v == nil {
		return true, nil
	}
	gate, _ := v.(agentTokenOwnerGate)
	if gate == nil {
		return true, nil
	}
	return gate(userID)
}

// ValidateAgentToken hashes the raw token and looks it up in storage.
// Returns the token if found and valid (not expired, not revoked) and its owner
// is still allowed to authenticate.
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

	// The identity behind the token must still be live. Checked LAST so a
	// revoked or expired token keeps its own, more specific answer, and so the
	// gate is not consulted for credentials that were going to be refused
	// anyway.
	active, err := m.agentTokenOwnerActive(token.UserID)
	if err != nil {
		// Fail closed: a user store that cannot answer must not mean "yes".
		if m.logger != nil {
			m.logger.Warnw("denying agent token: owner status could not be determined",
				"user_id", token.UserID, "token_prefix", token.TokenPrefix, "error", err)
		}
		return nil, ErrAgentTokenOwnerInactive
	}
	if !active {
		return nil, ErrAgentTokenOwnerInactive
	}

	return token, nil
}
