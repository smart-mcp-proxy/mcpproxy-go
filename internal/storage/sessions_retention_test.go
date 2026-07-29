package storage

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

func newRetentionTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// makeSession builds a session record. startOffset is relative to base, so tests
// can control the retention key ({StartTime.UnixNano()}_{ID}) precisely.
func makeSession(id string, base time.Time, startOffset, activityOffset time.Duration, status string) *SessionRecord {
	start := base.Add(startOffset)
	rec := &SessionRecord{
		ID:           id,
		ClientName:   "test-client",
		StartTime:    start,
		LastActivity: base.Add(activityOffset),
		Status:       status,
	}
	if status == "closed" {
		end := start.Add(time.Second)
		rec.EndTime = &end
	}
	return rec
}

func sessionIDs(sessions []*SessionRecord) []string {
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

func containsSessionID(sessions []*SessionRecord, id string) bool {
	for _, s := range sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}

// putRawSession writes a session value verbatim, so tests can plant records
// CreateSession would never produce (corrupt bytes, zero-valued records).
func putRawSession(m *Manager, key string, value []byte) error {
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(SessionsBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), value)
	})
}

func countSessions(t *testing.T, m *Manager) int {
	t.Helper()
	var n int
	require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(SessionsBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, _ []byte) error { n++; return nil })
	}))
	return n
}

func rawSessionExists(t *testing.T, m *Manager, key string) bool {
	t.Helper()
	var found bool
	require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(SessionsBucket))
		if b == nil {
			return nil
		}
		found = b.Get([]byte(key)) != nil
		return nil
	}))
	return found
}

// The cap is only an invariant if EVERY write path enforces it. CreateSession is
// not the only one: the legacy-bucket migration moves an arbitrary number of
// records into the sessions bucket at open time. A database that migrates 500
// records and then only ever updates or closes existing sessions — never
// creating a new ID — would sit above the cap forever.
func TestMigrateLegacySessions_LeavesBucketWithinRetentionLimit(t *testing.T) {
	dir := t.TempDir()

	// Build a pre-migration database by hand: MCP records in the shared bucket,
	// plus a user login that must survive untouched.
	db, err := bbolt.Open(filepath.Join(dir, "config.db"), 0o600, nil)
	require.NoError(t, err)

	base := time.Now().Add(-96 * time.Hour)
	const legacyCount = 500
	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(LegacySessionsBucket))
		if err != nil {
			return err
		}
		for i := 0; i < legacyCount; i++ {
			rec := makeSession(fmt.Sprintf("legacy-%04d", i), base,
				time.Duration(i)*time.Minute, time.Duration(i)*time.Minute, "closed")
			data, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(fmt.Sprintf("%d_%s", rec.StartTime.UnixNano(), rec.ID)), data); err != nil {
				return err
			}
		}
		login, err := json.Marshal(authSession{
			ID: "login-1", UserID: "u1", BearerToken: "jwt", ExpiresAt: time.Now().Add(24 * time.Hour),
		})
		if err != nil {
			return err
		}
		return b.Put([]byte("login-1"), login)
	}))
	require.NoError(t, db.Close())

	// Opening the database runs the migration.
	m, err := NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	assert.Equal(t, sessionRetentionLimit, countSessions(t, m),
		"migration must leave the sessions bucket within the cap — it is a write path like any other")

	// The newest records are the ones kept, and the user login is untouched.
	_, err = m.GetSessionByID(fmt.Sprintf("legacy-%04d", legacyCount-1))
	assert.NoError(t, err, "the newest migrated record must survive")
	_, err = m.GetSessionByID("legacy-0000")
	assert.Error(t, err, "the oldest migrated record must be trimmed")

	require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(LegacySessionsBucket))
		require.NotNil(t, b, "the legacy bucket must survive — it still holds the user login")
		assert.NotNil(t, b.Get([]byte("login-1")), "a user login must never be trimmed by session retention")
		return nil
	}))
}

// The bug: retention deletes the oldest KEYS, and keys are ordered by StartTime.
// A long-lived ACTIVE session has the oldest start time by definition, so it is
// the first thing evicted once enough newer sessions are created — even though
// the client is still connected and every one of those newer sessions is closed.
//
// Retention must evict finished work before it evicts live work.
func TestEnforceSessionRetention_KeepsLiveSessionWhenClosedOnesCouldGo(t *testing.T) {
	m := newRetentionTestManager(t)
	base := time.Now().Add(-24 * time.Hour)

	// A long-running client connects first and is still working.
	live := makeSession("live-session", base, 0, 24*time.Hour, "active")
	require.NoError(t, m.CreateSession(live))

	// Then a chatty client reconnects sessionRetentionLimit+20 times. Every one of
	// those sessions is finished, so every one of them is a better eviction
	// candidate than the live session.
	for i := 0; i < sessionRetentionLimit+20; i++ {
		s := makeSession(fmt.Sprintf("closed-%04d", i), base,
			time.Duration(i+1)*time.Minute, time.Duration(i+1)*time.Minute, "closed")
		require.NoError(t, m.CreateSession(s))
	}

	got, err := m.GetSessionByID("live-session")
	require.NoError(t, err, "the live session must still exist in storage — "+
		"a connected client's record must not be evicted while closed records remain")
	assert.Equal(t, "active", got.Status)

	sessions, total, err := m.GetRecentSessions(sessionRetentionLimit)
	require.NoError(t, err)
	assert.LessOrEqual(t, total, sessionRetentionLimit, "the hard cap must still hold")
	assert.True(t, containsSessionID(sessions, "live-session"),
		"the live session must be listed; got %v", sessionIDs(sessions))
}

// The same bug, with the staleness ordering deliberately pointing the wrong way:
// a connected-but-idle client (an editor left open in the background) has OLDER
// last-activity than every one of the closed sessions that churned past it. Only
// the status tier can save it here — ranking purely by last activity would evict
// the live session first.
//
// Status is authoritative for liveness; activity only ranks within a tier. A
// session that really is dead gets its status flipped by CloseInactiveSessions,
// and only then becomes evictable.
func TestEnforceSessionRetention_KeepsIdleLiveSessionOverFresherClosedOnes(t *testing.T) {
	m := newRetentionTestManager(t)
	base := time.Now().Add(-24 * time.Hour)

	// Connected, but last did anything a minute after base.
	idleLive := makeSession("idle-live-session", base, 0, time.Minute, "active")
	require.NoError(t, m.CreateSession(idleLive))

	// Closed sessions, every one of them more recently active than the live one.
	for i := 0; i < sessionRetentionLimit+20; i++ {
		s := makeSession(fmt.Sprintf("closed-%04d", i), base,
			time.Duration(i+2)*time.Minute, time.Duration(i+2)*time.Minute, "closed")
		require.NoError(t, m.CreateSession(s))
	}

	got, err := m.GetSessionByID("idle-live-session")
	require.NoError(t, err, "a connected client must outrank every closed session, "+
		"however recently those closed sessions were active")
	assert.Equal(t, "active", got.Status)

	sessions, total, err := m.GetRecentSessions(sessionRetentionLimit)
	require.NoError(t, err)
	assert.Equal(t, sessionRetentionLimit, total)
	assert.True(t, containsSessionID(sessions, "idle-live-session"),
		"the idle live session must be listed; got %v", sessionIDs(sessions))
}

// The abandoned-client case: if every retained session is "active" (clients that
// died without closing), preferring active sessions must NOT turn the bucket
// into an unbounded bucket. The cap still holds, and among active sessions the
// one with the freshest activity is the one that survives.
func TestEnforceSessionRetention_CapHoldsWhenEverySessionIsActive(t *testing.T) {
	m := newRetentionTestManager(t)
	base := time.Now().Add(-48 * time.Hour)

	// The genuinely live one: oldest start time, but active seconds ago.
	live := makeSession("live-session", base, 0, 48*time.Hour, "active")
	require.NoError(t, m.CreateSession(live))

	// Clients that died without closing: newer start times, stale activity.
	for i := 0; i < sessionRetentionLimit+30; i++ {
		s := makeSession(fmt.Sprintf("abandoned-%04d", i), base,
			time.Duration(i+1)*time.Minute, time.Duration(i+1)*time.Minute, "active")
		require.NoError(t, m.CreateSession(s))
	}

	sessions, total, err := m.GetRecentSessions(sessionRetentionLimit + 100)
	require.NoError(t, err)
	assert.Equal(t, sessionRetentionLimit, total,
		"an all-active bucket must still be capped — otherwise abandoned sessions grow without bound")
	assert.Len(t, sessions, sessionRetentionLimit)

	got, err := m.GetSessionByID("live-session")
	require.NoError(t, err, "among active sessions, the freshest activity must be the last to go")
	assert.Equal(t, "active", got.Status)
}

// Closed sessions are still evicted oldest-first among themselves.
func TestEnforceSessionRetention_EvictsOldestClosedFirst(t *testing.T) {
	m := newRetentionTestManager(t)
	base := time.Now().Add(-24 * time.Hour)

	for i := 0; i < sessionRetentionLimit+5; i++ {
		s := makeSession(fmt.Sprintf("closed-%04d", i), base,
			time.Duration(i)*time.Minute, time.Duration(i)*time.Minute, "closed")
		require.NoError(t, m.CreateSession(s))
	}

	sessions, total, err := m.GetRecentSessions(sessionRetentionLimit + 50)
	require.NoError(t, err)
	assert.Equal(t, sessionRetentionLimit, total)

	// The 5 oldest are gone, the newest are kept.
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("closed-%04d", i)
		assert.False(t, containsSessionID(sessions, id), "%s should have been evicted", id)
	}
	assert.True(t, containsSessionID(sessions, fmt.Sprintf("closed-%04d", sessionRetentionLimit+4)),
		"the newest closed session must be kept")
}

// A session with no LastActivity (older records predate the field) must be
// ranked by StartTime — its actual evidence — rather than sorting as "epoch
// zero" or as "right now".
//
// Both directions are asserted on purpose. Only proving that a zero-activity
// record SURVIVES is a test that passes for the wrong reason: substituting
// time.Now() for the fallback, which would make every legacy record eternally
// the freshest thing in the bucket, passes that assertion too. So a
// zero-activity record with an OLD StartTime must be evicted, and one with a
// NEW StartTime must survive, in the same bucket.
func TestEnforceSessionRetention_ZeroLastActivityIsRankedByStartTime(t *testing.T) {
	m := newRetentionTestManager(t)
	base := time.Now().Add(-24 * time.Hour)

	// Legacy record with no LastActivity and a start time older than everything
	// else in the bucket. StartTime is the only evidence, and it says: evict.
	stale := &SessionRecord{
		ID:        "legacy-stale",
		StartTime: base.Add(-10 * time.Hour),
		Status:    "active",
	}
	require.NoError(t, m.CreateSession(stale))

	// Legacy record with no LastActivity and a start time newer than everything
	// else. Same missing field, opposite verdict: keep.
	fresh := &SessionRecord{
		ID:        "legacy-fresh",
		StartTime: base.Add(10 * time.Hour),
		Status:    "active",
	}
	require.NoError(t, m.CreateSession(fresh))

	// Enough middle-aged active records to force both legacy records to be judged.
	for i := 0; i < sessionRetentionLimit+10; i++ {
		s := makeSession(fmt.Sprintf("old-active-%04d", i), base,
			time.Duration(i)*time.Minute, time.Duration(i)*time.Minute, "active")
		require.NoError(t, m.CreateSession(s))
	}

	_, err := m.GetSessionByID("legacy-stale")
	require.Error(t, err,
		"a zero LastActivity must be ranked by StartTime — an ancient legacy record "+
			"must not be treated as freshly active and outrank everything")

	got, err := m.GetSessionByID("legacy-fresh")
	require.NoError(t, err, "a zero LastActivity must fall back to StartTime, not sort as the year 1")
	assert.Equal(t, "active", got.Status)
}

// An unreadable record cannot be shown, closed, or updated by anything — it is
// the one record with no value at all, so it must be evicted before any usable
// record.
//
// Deriving that from field values alone does not work: an unmarshal failure
// leaves the zero value, which is indistinguishable from a VALID record whose
// status is not "active" and whose timestamps are zero. Both then land in the
// same tier with the same timestamp and the key-order tiebreak decides, which
// can delete the usable record and keep the corrupt one. Unreadability is
// therefore its own explicit tier.
func TestEnforceSessionRetention_EvictsUnreadableRecordsBeforeUsableOnes(t *testing.T) {
	m := newRetentionTestManager(t)

	// A valid record that looks exactly like the zero value: closed, no
	// timestamps at all. Its key sorts BEFORE the corrupt one, so a key-order
	// tiebreak would choose this one as the victim.
	require.NoError(t, putRawSession(m, "00000000000000000001_valid-zero", []byte(
		`{"id":"valid-zero","status":"closed"}`)))

	// A record that cannot be parsed. Higher key, so only an explicit tier can
	// make it lose.
	require.NoError(t, putRawSession(m, "00000000000000000002_corrupt", []byte(`{"id":`)))

	// Exactly one record over the limit, so exactly one must go.
	base := time.Now().Add(-time.Hour)
	for i := 0; i < sessionRetentionLimit-1; i++ {
		s := makeSession(fmt.Sprintf("closed-%04d", i), base,
			time.Duration(i)*time.Minute, time.Duration(i)*time.Minute, "closed")
		require.NoError(t, m.CreateSession(s))
	}

	require.Equal(t, sessionRetentionLimit, countSessions(t, m))
	assert.False(t, rawSessionExists(t, m, "00000000000000000002_corrupt"),
		"the unreadable record must be the one evicted")
	assert.True(t, rawSessionExists(t, m, "00000000000000000001_valid-zero"),
		"a usable record must not be deleted while an unreadable one survives")
}

// The bounded selection must pick the same victims a full sort would, including
// when the bucket starts far above the limit (the migration case).
func TestEnforceSessionRetention_TrimsFromFarAboveTheLimitInOneGo(t *testing.T) {
	m := newRetentionTestManager(t)
	base := time.Now().Add(-72 * time.Hour)

	const total = 500
	// Seed far above the cap without going through CreateSession, so retention
	// has to do the whole trim in a single call.
	for i := 0; i < total; i++ {
		rec := makeSession(fmt.Sprintf("bulk-%04d", i), base,
			time.Duration(i)*time.Minute, time.Duration(i)*time.Minute, "closed")
		data, err := json.Marshal(rec)
		require.NoError(t, err)
		require.NoError(t, putRawSession(m,
			fmt.Sprintf("%d_%s", rec.StartTime.UnixNano(), rec.ID), data))
	}
	require.Equal(t, total, countSessions(t, m))

	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		return m.enforceSessionRetention(tx.Bucket([]byte(SessionsBucket)), sessionRetentionLimit)
	}))

	assert.Equal(t, sessionRetentionLimit, countSessions(t, m))

	// The survivors must be the newest 100, exactly as a full sort would choose.
	for i := total - sessionRetentionLimit; i < total; i++ {
		_, err := m.GetSessionByID(fmt.Sprintf("bulk-%04d", i))
		assert.NoError(t, err, "bulk-%04d is among the newest 100 and must survive", i)
	}
	for i := 0; i < 5; i++ {
		_, err := m.GetSessionByID(fmt.Sprintf("bulk-%04d", i))
		assert.Error(t, err, "bulk-%04d is among the oldest and must be evicted", i)
	}
}
