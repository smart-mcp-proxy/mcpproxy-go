//go:build server

package users

import (
	"encoding/json"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"go.etcd.io/bbolt"
)

// userServersBucket returns the bucket name for a user's server configurations.
// Each user gets an isolated bucket: "user_servers:<userID>".
func userServersBucket(userID string) []byte {
	return []byte("user_servers:" + userID)
}

// CreateUserServer stores a server configuration for a specific user.
// Creates the user's server bucket if it doesn't exist.
func (s *UserStore) CreateUserServer(userID string, server *config.ServerConfig) error {
	if userID == "" {
		return fmt.Errorf("user ID is required")
	}
	if server.Name == "" {
		return fmt.Errorf("server name is required")
	}

	bucketName := userServersBucket(userID)

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(bucketName)
		if err != nil {
			return fmt.Errorf("failed to create user servers bucket: %w", err)
		}

		// Check for duplicate server name
		if existing := bucket.Get([]byte(server.Name)); existing != nil {
			return fmt.Errorf("server %q already exists for user %s", server.Name, userID)
		}

		data, err := json.Marshal(server)
		if err != nil {
			return fmt.Errorf("failed to marshal server config: %w", err)
		}

		return bucket.Put([]byte(server.Name), data)
	})
}

// GetUserServer retrieves a specific server configuration for a user.
// Returns nil if not found.
func (s *UserStore) GetUserServer(userID, serverName string) (*config.ServerConfig, error) {
	var server *config.ServerConfig

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(userServersBucket(userID))
		if bucket == nil {
			return nil
		}

		data := bucket.Get([]byte(serverName))
		if data == nil {
			return nil
		}

		server = &config.ServerConfig{}
		if err := json.Unmarshal(data, server); err != nil {
			return fmt.Errorf("failed to unmarshal server config: %w", err)
		}
		return nil
	})

	return server, err
}

// ListUserServers returns all server configurations for a user.
func (s *UserStore) ListUserServers(userID string) ([]*config.ServerConfig, error) {
	var servers []*config.ServerConfig

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(userServersBucket(userID))
		if bucket == nil {
			return nil
		}

		return bucket.ForEach(func(_, v []byte) error {
			var server config.ServerConfig
			if err := json.Unmarshal(v, &server); err != nil {
				return fmt.Errorf("failed to unmarshal server config: %w", err)
			}
			servers = append(servers, &server)
			return nil
		})
	})

	return servers, err
}

// UpdateUserServer updates an existing server configuration for a user.
// Returns an error if the server doesn't exist.
func (s *UserStore) UpdateUserServer(userID string, server *config.ServerConfig) error {
	if userID == "" {
		return fmt.Errorf("user ID is required")
	}
	if server.Name == "" {
		return fmt.Errorf("server name is required")
	}

	bucketName := userServersBucket(userID)

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketName)
		if bucket == nil {
			return fmt.Errorf("no servers found for user %s", userID)
		}

		// Verify server exists
		if existing := bucket.Get([]byte(server.Name)); existing == nil {
			return fmt.Errorf("server %q not found for user %s", server.Name, userID)
		}

		data, err := json.Marshal(server)
		if err != nil {
			return fmt.Errorf("failed to marshal server config: %w", err)
		}

		return bucket.Put([]byte(server.Name), data)
	})
}

// DeleteUserServer deletes a server configuration for a user.
func (s *UserStore) DeleteUserServer(userID, serverName string) error {
	if userID == "" {
		return fmt.Errorf("user ID is required")
	}

	bucketName := userServersBucket(userID)

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketName)
		if bucket == nil {
			return nil
		}

		return bucket.Delete([]byte(serverName))
	})
}

// UserServerExists checks if a server configuration exists for a user.
func (s *UserStore) UserServerExists(userID, serverName string) (bool, error) {
	var exists bool

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(userServersBucket(userID))
		if bucket == nil {
			return nil
		}

		if bucket.Get([]byte(serverName)) != nil {
			exists = true
		}
		return nil
	})

	return exists, err
}

// --- Shared server preferences ---

// sharedPrefsBucket returns the bucket name for a user's shared server preferences.
// Each user gets an isolated bucket: "shared_prefs:<userID>".
func sharedPrefsBucket(userID string) []byte {
	return []byte("shared_prefs:" + userID)
}

// SharedServerPref represents a user's preference for a shared server.
type SharedServerPref struct {
	ServerName string `json:"server_name"`
	Enabled    bool   `json:"enabled"`
}

// SetSharedServerPref stores a user's enable/disable preference for a shared server.
func (s *UserStore) SetSharedServerPref(userID, serverName string, enabled bool) error {
	if userID == "" {
		return fmt.Errorf("user ID is required")
	}
	if serverName == "" {
		return fmt.Errorf("server name is required")
	}

	bucketName := sharedPrefsBucket(userID)

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(bucketName)
		if err != nil {
			return fmt.Errorf("failed to create shared prefs bucket: %w", err)
		}

		pref := SharedServerPref{
			ServerName: serverName,
			Enabled:    enabled,
		}

		data, err := json.Marshal(pref)
		if err != nil {
			return fmt.Errorf("failed to marshal shared pref: %w", err)
		}

		return bucket.Put([]byte(serverName), data)
	})
}

// GetSharedServerPref retrieves a user's preference for a specific shared server.
// Returns nil if no preference is set (defaults to enabled).
func (s *UserStore) GetSharedServerPref(userID, serverName string) (*SharedServerPref, error) {
	var pref *SharedServerPref

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(sharedPrefsBucket(userID))
		if bucket == nil {
			return nil
		}

		data := bucket.Get([]byte(serverName))
		if data == nil {
			return nil
		}

		pref = &SharedServerPref{}
		if err := json.Unmarshal(data, pref); err != nil {
			return fmt.Errorf("failed to unmarshal shared pref: %w", err)
		}
		return nil
	})

	return pref, err
}

// GetSharedServerPrefs retrieves all shared server preferences for a user.
// Returns a map of server name -> preference.
func (s *UserStore) GetSharedServerPrefs(userID string) (map[string]*SharedServerPref, error) {
	prefs := make(map[string]*SharedServerPref)

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(sharedPrefsBucket(userID))
		if bucket == nil {
			return nil
		}

		return bucket.ForEach(func(_, v []byte) error {
			var pref SharedServerPref
			if err := json.Unmarshal(v, &pref); err != nil {
				return fmt.Errorf("failed to unmarshal shared pref: %w", err)
			}
			prefs[pref.ServerName] = &pref
			return nil
		})
	})

	return prefs, err
}
