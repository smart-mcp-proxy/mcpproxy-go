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

	t.Run("unfiltered page of 2 misses the active session", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(2, "")
		require.NoError(t, err)
		assert.Equal(t, 5, total, "unfiltered total is the whole bucket")
		require.Len(t, sessions, 2)
		for _, s := range sessions {
			assert.Equal(t, "closed", s.Status)
		}
	})

	t.Run("status=active returns the old active session despite the page size", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(2, "active")
		require.NoError(t, err)
		assert.Equal(t, 1, total, "total counts matching records, not the bucket")
		require.Len(t, sessions, 1)
		assert.Equal(t, "session-old-active", sessions[0].ID)
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
