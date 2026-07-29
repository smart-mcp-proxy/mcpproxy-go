package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// A session with no LastActivity (older records predate the field) must fall
// back to StartTime rather than sorting as "epoch zero" and being evicted first.
func TestEnforceSessionRetention_ZeroLastActivityFallsBackToStartTime(t *testing.T) {
	m := newRetentionTestManager(t)
	base := time.Now().Add(-24 * time.Hour)

	// Legacy active record: recent start, no LastActivity recorded.
	legacy := &SessionRecord{
		ID:        "legacy-active",
		StartTime: base.Add(10 * time.Hour),
		Status:    "active",
	}
	require.NoError(t, m.CreateSession(legacy))

	// Older active records with equally-old activity.
	for i := 0; i < sessionRetentionLimit+10; i++ {
		s := makeSession(fmt.Sprintf("old-active-%04d", i), base,
			time.Duration(i)*time.Minute, time.Duration(i)*time.Minute, "active")
		require.NoError(t, m.CreateSession(s))
	}

	got, err := m.GetSessionByID("legacy-active")
	require.NoError(t, err, "a zero LastActivity must fall back to StartTime, not sort as the year 1")
	assert.Equal(t, "active", got.Status)
}
