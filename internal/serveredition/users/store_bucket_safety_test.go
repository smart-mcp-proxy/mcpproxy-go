//go:build server

package users

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

func openStore(t *testing.T) (*UserStore, *bbolt.DB) {
	t.Helper()
	db, err := bbolt.Open(t.TempDir()+"/test.db", 0o600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(BucketSessions))
		return err
	}))
	return &UserStore{db: db}, db
}

// putForeign writes a record that is NOT a login session into the sessions
// bucket — exactly what the core used to do with its MCP session records.
func putForeign(t *testing.T, db *bbolt.DB, key string) {
	t.Helper()
	// An MCP session record: valid JSON, no user_id, no expires_at.
	foreign := map[string]interface{}{
		"id":          "mcp-session-abc",
		"client_name": "claude-code",
		"status":      "active",
		"start_time":  time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(foreign)
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(BucketSessions)).Put([]byte(key), data)
	}))
}

func keyCount(t *testing.T, db *bbolt.DB) int {
	t.Helper()
	n := 0
	require.NoError(t, db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(BucketSessions)).ForEach(func(_, _ []byte) error {
			n++
			return nil
		})
	}))
	return n
}

// The expiry sweep must never delete a record that is not a login session.
//
// A foreign record unmarshals into Session without error — JSON simply ignores
// the fields it does not know — leaving ExpiresAt at the zero time, which reads
// as "expired long ago". That is precisely how this sweep used to destroy every
// MCP session record that shared the bucket.
func TestCleanupExpiredSessions_NeverDeletesForeignRecords(t *testing.T) {
	store, db := openStore(t)

	putForeign(t, db, "0001_mcp-session-abc")

	live := NewSession("user-1", time.Hour)
	require.NoError(t, store.CreateSession(live))

	expired := NewSession("user-2", time.Hour)
	expired.ExpiresAt = time.Now().UTC().Add(-time.Hour)
	require.NoError(t, store.CreateSession(expired))

	removed, err := store.CleanupExpiredSessions()
	require.NoError(t, err)

	assert.Equal(t, 1, removed, "only the genuinely expired login is removed")
	assert.Equal(t, 2, keyCount(t, db),
		"the foreign record and the live login both survive — a zero expiry is not consent to delete")
}

// A foreign record must not be listed as a phantom login with no user.
func TestListSessions_SkipsForeignRecords(t *testing.T) {
	store, db := openStore(t)

	putForeign(t, db, "0001_mcp-session-abc")
	require.NoError(t, store.CreateSession(NewSession("user-1", time.Hour)))

	sessions, err := store.ListSessions()
	require.NoError(t, err)

	require.Len(t, sessions, 1, "only real login sessions are listed")
	assert.Equal(t, "user-1", sessions[0].UserID)
}
