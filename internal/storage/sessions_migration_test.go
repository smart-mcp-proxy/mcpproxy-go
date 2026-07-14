package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// authSession mirrors the server edition's user login session (users.Session).
// Duplicated here on purpose: internal/storage must not import the server
// edition (it is behind a build tag), but it must still be able to prove it
// leaves that shape alone.
type authSession struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	BearerToken string    `json:"bearer_token"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func openTempDB(t *testing.T) *bbolt.DB {
	t.Helper()
	db, err := bbolt.Open(t.TempDir()+"/test.db", 0o600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func putJSON(t *testing.T, db *bbolt.DB, bucket, key string, v interface{}) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), data)
	}))
}

func keysIn(t *testing.T, db *bbolt.DB, bucket string) []string {
	t.Helper()
	var keys []string
	require.NoError(t, db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	}))
	return keys
}

// The core bucket must not be the one the server edition uses for user logins.
// This is the whole bug in one assertion: they were both literally "sessions",
// on the same database, and each side's sweep deleted the other's records.
func TestSessionsBucketDoesNotCollideWithUserLogins(t *testing.T) {
	// The server edition's bucket name, hardcoded rather than imported (that
	// package is behind a build tag). If someone renames it, this test is the
	// tripwire.
	const serverEditionLoginBucket = "sessions"

	assert.NotEqual(t, serverEditionLoginBucket, SessionsBucket,
		"MCP session records must not share a bucket with user login sessions — "+
			"each side sweeps the bucket believing it owns every key")
	assert.Equal(t, serverEditionLoginBucket, LegacySessionsBucket,
		"the legacy bucket is the one the server edition still owns")
}

// The migration must move OUR records out and leave user logins exactly where
// they are. A logged-in user must not be logged out by upgrading.
func TestMigrateLegacySessions_MovesMCPRecordsAndSparesUserLogins(t *testing.T) {
	db := openTempDB(t)

	mcpRecord := SessionRecord{
		ID:           "mcp-session-abc",
		ClientName:   "claude-code",
		Status:       "active",
		StartTime:    time.Now().UTC(),
		LastActivity: time.Now().UTC(),
	}
	login := authSession{
		ID:          "login-1",
		UserID:      "user-123",
		BearerToken: "jwt...",
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour),
	}

	putJSON(t, db, LegacySessionsBucket, "0001_mcp-session-abc", mcpRecord)
	putJSON(t, db, LegacySessionsBucket, "login-1", login)

	require.NoError(t, db.Update(migrateLegacySessions))

	assert.Equal(t, []string{"0001_mcp-session-abc"}, keysIn(t, db, SessionsBucket),
		"the MCP record must move into the namespaced bucket")
	assert.Equal(t, []string{"login-1"}, keysIn(t, db, LegacySessionsBucket),
		"the user login must be left exactly where it is — upgrading must not log anyone out")
}

// A personal-edition database has nothing but MCP records. Once they are moved,
// the legacy bucket is empty and can go.
func TestMigrateLegacySessions_DropsLegacyBucketWhenEmptied(t *testing.T) {
	db := openTempDB(t)

	putJSON(t, db, LegacySessionsBucket, "0001_a", SessionRecord{
		ID: "a", Status: "active", StartTime: time.Now().UTC(),
	})

	require.NoError(t, db.Update(migrateLegacySessions))

	require.NoError(t, db.View(func(tx *bbolt.Tx) error {
		assert.Nil(t, tx.Bucket([]byte(LegacySessionsBucket)),
			"an emptied legacy bucket should be removed")
		return nil
	}))
	assert.Equal(t, []string{"0001_a"}, keysIn(t, db, SessionsBucket))
}

func TestMigrateLegacySessions_IsIdempotent(t *testing.T) {
	db := openTempDB(t)
	putJSON(t, db, LegacySessionsBucket, "0001_a", SessionRecord{
		ID: "a", Status: "active", StartTime: time.Now().UTC(),
	})
	putJSON(t, db, LegacySessionsBucket, "login-1", authSession{
		ID: "login-1", UserID: "u1", ExpiresAt: time.Now().Add(time.Hour),
	})

	require.NoError(t, db.Update(migrateLegacySessions))
	require.NoError(t, db.Update(migrateLegacySessions))
	require.NoError(t, db.Update(migrateLegacySessions))

	assert.Equal(t, []string{"0001_a"}, keysIn(t, db, SessionsBucket))
	assert.Equal(t, []string{"login-1"}, keysIn(t, db, LegacySessionsBucket))
}

func TestMigrateLegacySessions_NoLegacyBucketIsFine(t *testing.T) {
	db := openTempDB(t)
	assert.NoError(t, db.Update(migrateLegacySessions))
}

// The discriminator is the safety property: anything we are not certain is ours
// gets left alone rather than moved (or, in the sweeps, deleted).
func TestIsMCPSessionRecord(t *testing.T) {
	mcp, err := json.Marshal(SessionRecord{
		ID: "x", Status: "active", StartTime: time.Now().UTC(),
	})
	require.NoError(t, err)
	assert.True(t, isMCPSessionRecord(mcp))

	login, err := json.Marshal(authSession{
		ID: "l", UserID: "u", ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	assert.False(t, isMCPSessionRecord(login), "a user login is never ours to move")

	assert.False(t, isMCPSessionRecord([]byte("not json")))
	assert.False(t, isMCPSessionRecord([]byte(`{"something":"else"}`)),
		"an unrecognised record is left alone rather than moved on a guess")

	// The nasty one: a record carrying BOTH shapes. Hands off — we only move what
	// is unambiguously ours.
	both := []byte(`{"start_time":"2026-07-12T10:00:00Z","status":"active","user_id":"u1"}`)
	assert.False(t, isMCPSessionRecord(both))
}

// Retention keeps the newest 100 by raw key order with no type check. That is
// safe now only because the bucket is exclusively ours — this test pins the
// behaviour the namespacing depends on.
func TestEnforceSessionRetention_OnlyTouchesItsOwnBucket(t *testing.T) {
	db := openTempDB(t)

	// A user login lives in the LEGACY bucket, where the server edition keeps it.
	putJSON(t, db, LegacySessionsBucket, "login-1", authSession{
		ID: "login-1", UserID: "u1", ExpiresAt: time.Now().Add(24 * time.Hour),
	})

	// Fill our own bucket past the retention limit.
	for i := 0; i < 130; i++ {
		putJSON(t, db, SessionsBucket, string(rune('a'+i%26))+string(rune('a'+i/26)), SessionRecord{
			ID: "s", Status: "active", StartTime: time.Now().UTC(),
		})
	}

	m := &Manager{logger: zap.NewNop().Sugar()}
	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		return m.enforceSessionRetention(tx.Bucket([]byte(SessionsBucket)), 100)
	}))

	assert.Len(t, keysIn(t, db, SessionsBucket), 100, "retention trims our own bucket")
	assert.Equal(t, []string{"login-1"}, keysIn(t, db, LegacySessionsBucket),
		"and cannot reach a user login, which is what used to log people out")
}
