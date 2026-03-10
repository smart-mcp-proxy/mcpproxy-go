//go:build teams

package users

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.etcd.io/bbolt"
)

// Bucket names for teams user and session storage.
const (
	BucketUsers        = "users"
	BucketUsersByEmail = "users_by_email"
	BucketSessions     = "sessions"
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
				return fmt.Errorf("failed to unmarshal session: %w", err)
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
