package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A session that started long ago but is still active must survive a small
// page. Storage walks newest-first by START time and truncates to `limit`, so
// filtering on the client after truncation would drop it entirely — the tray's
// "Clients" section would then say "No connected clients" while a client is
// actively calling tools.
func TestGetRecentSessions_StatusFilterAppliedBeforeTruncation(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	base := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)

	// Oldest session is the active one.
	require.NoError(t, manager.CreateSession(&SessionRecord{
		ID:           "session-old-active",
		ClientName:   "claude-code",
		Status:       "active",
		StartTime:    base,
		LastActivity: base.Add(90 * time.Minute),
	}))

	// Four newer sessions, all closed.
	for i := 1; i <= 4; i++ {
		require.NoError(t, manager.CreateSession(&SessionRecord{
			ID:           "session-closed-" + string(rune('a'+i-1)),
			ClientName:   "cursor",
			Status:       "closed",
			StartTime:    base.Add(time.Duration(i) * time.Minute),
			LastActivity: base.Add(time.Duration(i) * time.Minute),
		}))
	}

	// Spec 090 reordered this: the unfiltered page is last_activity desc, so the
	// old-but-busy session now leads it. Before that change the page was
	// start-time order and this session was the one the page missed — which is
	// why the status filter had to compensate.
	t.Run("unfiltered page of 2 leads with the most recently active session", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(2, "")
		require.NoError(t, err)
		assert.Equal(t, 5, total, "unfiltered total is the whole bucket")
		require.Len(t, sessions, 2)
		assert.Equal(t, "session-old-active", sessions[0].ID)
		assert.Equal(t, "session-closed-d", sessions[1].ID)
	})

	t.Run("status=active returns the old active session despite the page size", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(2, "active")
		require.NoError(t, err)
		assert.Equal(t, 1, total, "total counts matching records, not the bucket")
		require.Len(t, sessions, 1)
		assert.Equal(t, "session-old-active", sessions[0].ID)
	})

	// The filtered walk must still honour `limit`. With more matches than the
	// page size, dropping the truncation guard would return all four — so this
	// is the case that actually pins it. Keys are "{startUnixNano}_{id}" and the
	// cursor runs Last()->Prev(), so the page is the two NEWEST closed sessions.
	t.Run("limit truncates the filtered result while total counts every match", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(2, "closed")
		require.NoError(t, err)
		assert.Equal(t, 4, total, "total counts every match, not just the returned page")
		require.Len(t, sessions, 2, "limit must truncate the filtered walk")
		assert.Equal(t, "session-closed-d", sessions[0].ID)
		assert.Equal(t, "session-closed-c", sessions[1].ID)
	})

	t.Run("status=closed returns only closed sessions", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(10, "closed")
		require.NoError(t, err)
		assert.Equal(t, 4, total)
		require.Len(t, sessions, 4)
		for _, s := range sessions {
			assert.Equal(t, "closed", s.Status)
		}
	})
}

// Spec 090 (T024, contracts §3): the page must be the most RECENTLY ACTIVE
// sessions, not the most recently STARTED ones.
//
// Bucket keys are "{startUnixNano}_{id}", so a cursor walk is start-time order.
// A client that connected this morning and is still calling tools sits at the
// bottom of that walk, and every reconnect from a chattier client pushes it
// further down until `limit` cuts it off — the tray then reports it as gone
// while it is the busiest client on the machine. Sorting by LastActivity has to
// happen over the whole retained set (capped at 100 by retention) BEFORE the
// truncation, and independently of status.
func TestGetRecentSessions_OrdersByLastActivityBeforeTruncation(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	base := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)

	// Started first, active most recently. Last in start order, first in
	// activity order.
	require.NoError(t, manager.CreateSession(&SessionRecord{
		ID:           "session-old-start-newest-activity",
		ClientName:   "claude-code",
		Status:       "active",
		StartTime:    base,
		LastActivity: base.Add(90 * time.Minute),
	}))

	// A closed session that was busy until recently — it must be able to
	// outrank a quiet active one, since the ordering ignores status.
	require.NoError(t, manager.CreateSession(&SessionRecord{
		ID:           "session-closed-recent-activity",
		ClientName:   "cursor",
		Status:       "closed",
		StartTime:    base.Add(1 * time.Minute),
		LastActivity: base.Add(60 * time.Minute),
	}))

	// Newest start, but silent since it connected.
	require.NoError(t, manager.CreateSession(&SessionRecord{
		ID:           "session-new-start-quiet",
		ClientName:   "vscode",
		Status:       "active",
		StartTime:    base.Add(30 * time.Minute),
		LastActivity: base.Add(30 * time.Minute),
	}))

	require.NoError(t, manager.CreateSession(&SessionRecord{
		ID:           "session-newest-start-quietest",
		ClientName:   "zed",
		Status:       "closed",
		StartTime:    base.Add(40 * time.Minute),
		LastActivity: base.Add(40 * time.Minute),
	}))

	t.Run("full page is ordered by last activity, newest first", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(100, "")
		require.NoError(t, err)
		assert.Equal(t, 4, total)
		require.Len(t, sessions, 4)

		got := make([]string, len(sessions))
		for i, s := range sessions {
			got[i] = s.ID
		}
		assert.Equal(t, []string{
			"session-old-start-newest-activity", // base+90m
			"session-closed-recent-activity",    // base+60m
			"session-newest-start-quietest",     // base+40m
			"session-new-start-quiet",           // base+30m
		}, got, "order is last_activity desc, regardless of start time or status")
	})

	t.Run("truncation keeps the recently active, not the recently started", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(2, "")
		require.NoError(t, err)
		assert.Equal(t, 4, total, "total still counts the whole bucket")
		require.Len(t, sessions, 2)
		assert.Equal(t, "session-old-start-newest-activity", sessions[0].ID,
			"the oldest-started session leads the page because it is the busiest")
		assert.Equal(t, "session-closed-recent-activity", sessions[1].ID)
	})

	t.Run("the status filter runs over the activity-sorted set", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(1, "active")
		require.NoError(t, err)
		assert.Equal(t, 2, total, "total counts every match, not just the returned page")
		require.Len(t, sessions, 1)
		assert.Equal(t, "session-old-start-newest-activity", sessions[0].ID)
	})

	t.Run("a zero last activity falls to the back rather than winning", func(t *testing.T) {
		require.NoError(t, manager.CreateSession(&SessionRecord{
			ID:        "session-never-active",
			Status:    "closed",
			StartTime: base.Add(50 * time.Minute),
			// LastActivity deliberately left zero.
		}))

		sessions, _, err := manager.GetRecentSessions(100, "")
		require.NoError(t, err)
		require.Len(t, sessions, 5)
		assert.Equal(t, "session-never-active", sessions[len(sessions)-1].ID)
	})
}
