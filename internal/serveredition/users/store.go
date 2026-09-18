//go:build server

package users

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

// Bucket names for server edition user and session storage.
const (
	BucketUsers        = "users"
	BucketUsersByEmail = "users_by_email"
	// BucketSessions holds USER LOGIN sessions.
	//
	// The name stays "sessions" DELIBERATELY. The core used to share this bucket
	// for MCP session records, and the two swept it believing each owned every
	// key — core's retention evicted user logins, and the expiry sweep below
	// deleted MCP records (they carry no expires_at, so a zero time reads as long
	// expired). The core has moved out to "mcp_sessions"
	// (internal/storage/sessions_migration.go); this bucket is now exclusively
	// ours.
	//
	// Renaming it here as well would orphan every existing login and log the whole
	// estate out on upgrade — the exact harm this fix exists to prevent. So it
	// keeps its name, and the guards below make the sweep safe regardless.
	BucketSessions = "sessions"
)

// UserStore provides CRUD operations for User and Session entities in BBolt.
type UserStore struct {
	db *bbolt.DB
}

// NewUserStore creates a new UserStore backed by the given BBolt database.
func NewUserStore(db *bbolt.DB) *UserStore {
	return &UserStore{db: db}
}

// EnsureBuckets creates all required buckets if they don't exist.
func (s *UserStore) EnsureBuckets() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		buckets := []string{
			BucketUsers,
			BucketUsersByEmail,
			BucketSessions,
		}
		for _, name := range buckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return fmt.Errorf("failed to create bucket %s: %w", name, err)
			}
		}
		return nil
	})
}

// CreateUser stores a new user and creates an email index entry.
// Returns an error if a user with the same email already exists.
func (s *UserStore) CreateUser(user *User) error {
	if err := user.Validate(); err != nil {
		return fmt.Errorf("invalid user: %w", err)
	}

	normalizedEmail := strings.ToLower(strings.TrimSpace(user.Email))

	return s.db.Update(func(tx *bbolt.Tx) error {
		usersBucket := tx.Bucket([]byte(BucketUsers))
		if usersBucket == nil {
			return fmt.Errorf("bucket %s not found", BucketUsers)
		}

		emailBucket := tx.Bucket([]byte(BucketUsersByEmail))
		if emailBucket == nil {
			return fmt.Errorf("bucket %s not found", BucketUsersByEmail)
		}

		// Check for duplicate email
		if existing := emailBucket.Get([]byte(normalizedEmail)); existing != nil {
			return fmt.Errorf("user with email %q already exists", normalizedEmail)
		}

		data, err := json.Marshal(user)
		if err != nil {
			return fmt.Errorf("failed to marshal user: %w", err)
		}

		if err := usersBucket.Put([]byte(user.ID), data); err != nil {
			return fmt.Errorf("failed to store user: %w", err)
		}

		if err := emailBucket.Put([]byte(normalizedEmail), []byte(user.ID)); err != nil {
			return fmt.Errorf("failed to store email index: %w", err)
		}

		return nil
	})
}

// GetUser retrieves a user by ID. Returns nil if not found.
func (s *UserStore) GetUser(id string) (*User, error) {
	var user *User

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketUsers))
		if bucket == nil {
			return nil
		}

		data := bucket.Get([]byte(id))
		if data == nil {
			return nil
		}

		user = &User{}
		if err := json.Unmarshal(data, user); err != nil {
			return fmt.Errorf("failed to unmarshal user: %w", err)
		}
		return nil
	})

	return user, err
}

// GetUserByEmail retrieves a user by email address (case-insensitive).
// Returns nil if not found.
func (s *UserStore) GetUserByEmail(email string) (*User, error) {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))

	var user *User

	err := s.db.View(func(tx *bbolt.Tx) error {
		emailBucket := tx.Bucket([]byte(BucketUsersByEmail))
		if emailBucket == nil {
			return nil
		}

		userID := emailBucket.Get([]byte(normalizedEmail))
		if userID == nil {
			return nil
		}

		usersBucket := tx.Bucket([]byte(BucketUsers))
		if usersBucket == nil {
			return nil
		}

		data := usersBucket.Get(userID)
		if data == nil {
			return nil
		}

		user = &User{}
		if err := json.Unmarshal(data, user); err != nil {
			return fmt.Errorf("failed to unmarshal user: %w", err)
		}
		return nil
	})

	return user, err
}

// UpdateUser updates an existing user record. The user must already exist.
func (s *UserStore) UpdateUser(user *User) error {
	if err := user.Validate(); err != nil {
		return fmt.Errorf("invalid user: %w", err)
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		usersBucket := tx.Bucket([]byte(BucketUsers))
		if usersBucket == nil {
			return fmt.Errorf("bucket %s not found", BucketUsers)
		}

		// Verify user exists
		existingData := usersBucket.Get([]byte(user.ID))
		if existingData == nil {
			return fmt.Errorf("user %q not found", user.ID)
		}

		// Check if email changed; if so, update the index
		var existing User
		if err := json.Unmarshal(existingData, &existing); err != nil {
			return fmt.Errorf("failed to unmarshal existing user: %w", err)
		}

		emailBucket := tx.Bucket([]byte(BucketUsersByEmail))
		if emailBucket == nil {
			return fmt.Errorf("bucket %s not found", BucketUsersByEmail)
		}

		oldEmail := strings.ToLower(strings.TrimSpace(existing.Email))
		newEmail := strings.ToLower(strings.TrimSpace(user.Email))

		if oldEmail != newEmail {
			// Check that the new email isn't already taken by another user
			if existingID := emailBucket.Get([]byte(newEmail)); existingID != nil {
				if string(existingID) != user.ID {
					return fmt.Errorf("user with email %q already exists", newEmail)
				}
			}
			// Remove old email index entry
			if err := emailBucket.Delete([]byte(oldEmail)); err != nil {
				return fmt.Errorf("failed to delete old email index: %w", err)
			}
			// Add new email index entry
			if err := emailBucket.Put([]byte(newEmail), []byte(user.ID)); err != nil {
				return fmt.Errorf("failed to store new email index: %w", err)
			}
		}

		data, err := json.Marshal(user)
		if err != nil {
			return fmt.Errorf("failed to marshal user: %w", err)
		}

		return usersBucket.Put([]byte(user.ID), data)
	})
}

// ListUsers returns all users.
func (s *UserStore) ListUsers() ([]*User, error) {
	var users []*User

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketUsers))
		if bucket == nil {
			return nil
		}

		return bucket.ForEach(func(_, v []byte) error {
			var user User
			if err := json.Unmarshal(v, &user); err != nil {
				return fmt.Errorf("failed to unmarshal user: %w", err)
			}
			users = append(users, &user)
			return nil
		})
	})

	return users, err
}

// DeleteUser deletes a user by ID and removes the email index entry.
func (s *UserStore) DeleteUser(id string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		usersBucket := tx.Bucket([]byte(BucketUsers))
		if usersBucket == nil {
			return nil
		}

		// Retrieve user to get email for index cleanup
		data := usersBucket.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("user %q not found", id)
		}

		var user User
		if err := json.Unmarshal(data, &user); err != nil {
			return fmt.Errorf("failed to unmarshal user: %w", err)
		}

		// Delete email index
		emailBucket := tx.Bucket([]byte(BucketUsersByEmail))
		if emailBucket != nil {
			normalizedEmail := strings.ToLower(strings.TrimSpace(user.Email))
			if err := emailBucket.Delete([]byte(normalizedEmail)); err != nil {
				return fmt.Errorf("failed to delete email index: %w", err)
			}
		}

		// Delete user record
		if err := usersBucket.Delete([]byte(id)); err != nil {
			return fmt.Errorf("failed to delete user: %w", err)
		}

		// Delete user's server bucket if it exists
		serverBucketName := userServersBucket(id)
		serverBucket := tx.Bucket(serverBucketName)
		if serverBucket != nil {
			if err := tx.DeleteBucket(serverBucketName); err != nil {
				return fmt.Errorf("failed to delete user servers bucket: %w", err)
			}
		}

		return nil
	})
}

// --- Session operations ---

// CreateSession stores a new session.
func (s *UserStore) CreateSession(session *Session) error {
	if err := session.Validate(); err != nil {
		return fmt.Errorf("invalid session: %w", err)
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSessions))
		if bucket == nil {
			return fmt.Errorf("bucket %s not found", BucketSessions)
		}

		data, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("failed to marshal session: %w", err)
		}

		return bucket.Put([]byte(session.ID), data)
	})
}

// GetSession retrieves a session by ID. Returns nil if not found or if the session has expired.
func (s *UserStore) GetSession(id string) (*Session, error) {
	var session *Session

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSessions))
		if bucket == nil {
			return nil
		}

		data := bucket.Get([]byte(id))
		if data == nil {
			return nil
		}

		session = &Session{}
		if err := json.Unmarshal(data, session); err != nil {
			return fmt.Errorf("failed to unmarshal session: %w", err)
		}

		// Return nil for expired sessions
		if session.IsExpired() {
			session = nil
		}

		return nil
	})

	return session, err
}

// DeleteSession deletes a session by ID.
func (s *UserStore) DeleteSession(id string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSessions))
		if bucket == nil {
			return nil
		}

		return bucket.Delete([]byte(id))
	})
}

// DeleteUserSessions deletes all sessions for a given user ID.
func (s *UserStore) DeleteUserSessions(userID string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSessions))
		if bucket == nil {
			return nil
		}

		// Collect session IDs to delete (cannot modify bucket during iteration)
		var toDelete [][]byte
		if err := bucket.ForEach(func(k, v []byte) error {
			var session Session
			if err := json.Unmarshal(v, &session); err != nil {
				return nil // Skip malformed entries
			}
			if session.UserID == userID {
				keyCopy := make([]byte, len(k))
				copy(keyCopy, k)
				toDelete = append(toDelete, keyCopy)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to iterate sessions: %w", err)
		}

		for _, key := range toDelete {
			if err := bucket.Delete(key); err != nil {
				return fmt.Errorf("failed to delete session: %w", err)
			}
		}

		return nil
	})
}

// ListSessions returns all sessions (including expired ones).
func (s *UserStore) ListSessions() ([]*Session, error) {
	var sessions []*Session

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSessions))
		if bucket == nil {
			return nil
		}

		return bucket.ForEach(func(_, v []byte) error {
			var session Session
			if err := json.Unmarshal(v, &session); err != nil {
				return nil // not a login session — skip rather than fail the listing
			}
			// Defence in depth: only list records that actually look like login
			// sessions. JSON unmarshalling happily accepts a foreign shape and
			// leaves the fields zero, which is how MCP records used to appear here
			// as phantom sessions with no user and a zero expiry.
			if !session.isLoginSession() {
				return nil
			}
			sessions = append(sessions, &session)
			return nil
		})
	})

	return sessions, err
}

// CleanupExpiredSessions removes all expired sessions and returns the count of removed sessions.
func (s *UserStore) CleanupExpiredSessions() (int, error) {
	var count int
	now := time.Now().UTC()

	err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSessions))
		if bucket == nil {
			return nil
		}

		// Collect expired session keys
		var toDelete [][]byte
		if err := bucket.ForEach(func(k, v []byte) error {
			var session Session
			if err := json.Unmarshal(v, &session); err != nil {
				return nil // Skip malformed entries
			}
			// NEVER delete a record that is not a login session. A foreign record
			// unmarshals without error and leaves ExpiresAt at the zero time, which
			// reads as "long expired" — that is precisely how this sweep used to
			// destroy every MCP session record sharing the bucket.
			if !session.isLoginSession() {
				return nil
			}
			if now.After(session.ExpiresAt) {
				keyCopy := make([]byte, len(k))
				copy(keyCopy, k)
				toDelete = append(toDelete, keyCopy)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to iterate sessions: %w", err)
		}

		for _, key := range toDelete {
			if err := bucket.Delete(key); err != nil {
				return fmt.Errorf("failed to delete expired session: %w", err)
			}
		}

		count = len(toDelete)
		return nil
	})

	return count, err
}

// Sentinel errors of UpdateUserLogin. The caller maps them to the FR-013
// closed reasons (subject_mismatch, user_disabled).
var (
	// ErrSubjectMismatch: same provider, same email, a different subject and
	// no administrator-armed rebind window (Spec 107 FR-023).
	ErrSubjectMismatch = errors.New("provider subject does not match the stored binding")
	// ErrUserDisabled: the record is disabled; nothing is written.
	ErrUserDisabled = errors.New("user is disabled")
)

// UpdateUserLogin applies one successful login to the user record keyed by
// the claims' normalised email, atomically (Spec 107 FR-008/FR-023).
//
// It is transaction-owned: the record is re-read by the email index INSIDE
// one db.Update, the subject rule is evaluated there, and groups, subject,
// provider, display name and last-login are written in that same
// transaction. Two concurrent logins therefore serialise on the store: when
// the rebind window is armed and both present different subjects, exactly
// one rebinds (and clears the window) and the other sees the consumed window
// and is refused with ErrSubjectMismatch. A pre-mutated *User handed to
// UpdateUser could not provide this — both callers would observe the armed
// flag.
//
// Subject rule on an existing record:
//   - Disabled → ErrUserDisabled, nothing written.
//   - stored subject empty, or same (provider, subject) → bind.
//   - stored provider differs from the presented one → rebind (Rebound).
//   - same provider, different subject: rebind only while
//     SubjectRebindArmedAt is set (Rebound + RebindConsumed, the flag is
//     cleared in the same write); otherwise ErrSubjectMismatch, nothing
//     written.
//
// A successful login while the window is armed always closes it (Rebound +
// RebindConsumed both set), even when the subject did not change: FR-023
// carries provider_rebound on "the first successful login" while armed
// unconditionally, and RebindConsumed alone is never logged anywhere, so a
// same-subject consumption would otherwise leave no audit trace. A missing
// record is created bound to the presented (provider, subject) (Created).
func (s *UserStore) UpdateUserLogin(ctx context.Context, claims LoginClaims) (LoginOutcome, error) {
	if err := ctx.Err(); err != nil {
		return LoginOutcome{}, err
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		return LoginOutcome{}, fmt.Errorf("login claims: email is required")
	}
	if claims.Subject == "" {
		return LoginOutcome{}, fmt.Errorf("login claims: subject is required")
	}
	now := time.Now().UTC()

	var out LoginOutcome
	err := s.db.Update(func(tx *bbolt.Tx) error {
		usersBucket := tx.Bucket([]byte(BucketUsers))
		if usersBucket == nil {
			return fmt.Errorf("bucket %s not found", BucketUsers)
		}
		emailBucket := tx.Bucket([]byte(BucketUsersByEmail))
		if emailBucket == nil {
			return fmt.Errorf("bucket %s not found", BucketUsersByEmail)
		}

		var user *User
		if id := emailBucket.Get([]byte(email)); id != nil {
			if data := usersBucket.Get(id); data != nil {
				user = &User{}
				if err := json.Unmarshal(data, user); err != nil {
					return fmt.Errorf("failed to unmarshal user: %w", err)
				}
			}
		}

		if user == nil {
			user = NewUser(email, claims.Name, claims.Provider, claims.Subject)
			user.CreatedAt = now
			out.Created = true
		} else {
			if user.Disabled {
				// The record this login refused against, from the SAME read
				// this transaction made — round-2 cross-review finding,
				// PR-D: the caller used to re-look the user up by email
				// AFTER this transaction committed/rolled back, which a
				// concurrent DeleteUser (or a transient read failure) could
				// race, turning a schema-required `user_id` on the
				// auth_event line into a silently anonymous one. Capturing
				// it here is race-free by construction: it is the exact
				// record the refusal decision was made from.
				out.User = user
				return ErrUserDisabled
			}
			armed := user.SubjectRebindArmedAt != nil
			switch {
			case user.ProviderSubjectID == "":
				// An upgraded record bound before the IdP exposed a subject: bind.
			case user.Provider == claims.Provider && user.ProviderSubjectID == claims.Subject:
				// Plain bind — no identity change, but see below: consuming an
				// armed window is itself the reportable event (FR-023).
			case user.Provider != claims.Provider:
				out.Rebound = true
			case armed:
				out.Rebound = true
			default:
				out.User = user // see the ErrUserDisabled comment above.
				return ErrSubjectMismatch
			}
			if armed {
				// FR-023: "the first successful login" while armed "carries
				// provider_rebound in the line's flags" — unconditionally, not
				// only when the presented (provider, sub) actually differs from
				// the stored one. RebindConsumed alone reaches no audit line, so
				// a same-subject login while armed would otherwise close the
				// administrator-opened window with no trace at all.
				user.SubjectRebindArmedAt = nil
				out.RebindConsumed = true
				out.Rebound = true
			}
			user.Provider = claims.Provider
			user.ProviderSubjectID = claims.Subject
			if claims.Name != "" {
				user.DisplayName = claims.Name
			}
		}
		user.LastLoginAt = now
		if claims.GroupsKnown {
			groups := claims.Groups
			if groups == nil {
				groups = []string{}
			}
			user.Groups = groups
			user.GroupsUpdatedAt = now
		}

		if err := user.Validate(); err != nil {
			return fmt.Errorf("invalid user: %w", err)
		}
		data, err := json.Marshal(user)
		if err != nil {
			return fmt.Errorf("failed to marshal user: %w", err)
		}
		if err := usersBucket.Put([]byte(user.ID), data); err != nil {
			return fmt.Errorf("failed to store user: %w", err)
		}
		if out.Created {
			if err := emailBucket.Put([]byte(email), []byte(user.ID)); err != nil {
				return fmt.Errorf("failed to store email index: %w", err)
			}
		}
		out.User = user
		return nil
	})
	if err != nil {
		// out.User is set only on the two branches that captured it
		// (ErrUserDisabled, ErrSubjectMismatch) above; every other error
		// path leaves out at its zero value, so returning out here instead
		// of LoginOutcome{} changes nothing for those callers and gives the
		// two refusal callers race-free access to the record the decision
		// was made from (round-2 cross-review finding, PR-D).
		return out, err
	}
	return out, nil
}

// SetUserDisabled atomically toggles the Disabled flag (and, for an enable,
// the FR-023 rebind window) inside ONE db.Update transaction: the record is
// re-read by id INSIDE the transaction, not handed in pre-mutated.
//
// A blind GetUser (View) + mutate + UpdateUser (Put), which is what the admin
// handlers used before, has a lost-update window: a concurrent successful
// login can run its own UpdateUserLogin transaction — writing Groups,
// GroupsUpdatedAt, Provider, ProviderSubjectID, LastLoginAt and clearing
// SubjectRebindArmedAt — entirely between the admin's read and its write, and
// the admin's blind Put then overwrites the record back to the stale
// snapshot, reopening a just-consumed rebind window or discarding a
// concurrent subject rebind (cross-review round 1, chunk 2 P1).
//
// Returns (nil, false, nil) when the user does not exist (GetUser's own
// not-found convention), and armed reports whether a real disabled→enabled
// transition opened the FR-023 rebind window (mirrors the enableUser
// semantics: enabling an already-enabled record is not a transition).
func (s *UserStore) SetUserDisabled(id string, disabled bool) (user *User, armed bool, err error) {
	now := time.Now().UTC()
	txErr := s.db.Update(func(tx *bbolt.Tx) error {
		usersBucket := tx.Bucket([]byte(BucketUsers))
		if usersBucket == nil {
			return fmt.Errorf("bucket %s not found", BucketUsers)
		}
		data := usersBucket.Get([]byte(id))
		if data == nil {
			return nil // not found; user stays nil
		}
		u := &User{}
		if err := json.Unmarshal(data, u); err != nil {
			return fmt.Errorf("failed to unmarshal user: %w", err)
		}

		if disabled {
			u.Disabled = true
			// Disabling closes any open rebind window (FR-023); the binding
			// itself (ProviderSubjectID) is kept.
			u.SubjectRebindArmedAt = nil
		} else {
			if u.Disabled {
				u.SubjectRebindArmedAt = &now
				armed = true
			}
			u.Disabled = false
		}

		if err := u.Validate(); err != nil {
			return fmt.Errorf("invalid user: %w", err)
		}
		out, err := json.Marshal(u)
		if err != nil {
			return fmt.Errorf("failed to marshal user: %w", err)
		}
		if err := usersBucket.Put([]byte(id), out); err != nil {
			return fmt.Errorf("failed to store user: %w", err)
		}
		user = u
		return nil
	})
	if txErr != nil {
		return nil, false, txErr
	}
	return user, armed, nil
}
