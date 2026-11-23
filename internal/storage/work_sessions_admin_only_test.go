package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

// Review round 14 (C1): an administrator-only row type (Spec 125 FR-019a)
// never joins a work session. A session door answers scoped callers from the
// summary and the row index, so a row in either would show through its times,
// its row count or a position on it, even with the row itself withheld.
func TestWorkSession_AdminOnlyRowsDoNotJoinASession(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	require.NoError(t, m.SaveActivity(wsRow("ws-1", 0, nil)))
	before, err := m.GetWorkSessionSummary("ws-1")
	require.NoError(t, err)
	require.NotNil(t, before)

	for i, typ := range []ActivityType{ActivityTypeSkillAccess, ActivityTypeSkillQuarantineChange} {
		require.NoError(t, m.SaveActivity(wsRow("ws-1", time.Duration(i+1)*time.Minute, func(r *ActivityRecord) {
			r.Type, r.Status = typ, ActivityStatusError
		})))
		// A session made only of such rows is never created.
		require.NoError(t, m.SaveActivity(wsRow("ws-skill-only", time.Duration(i+1)*time.Minute, func(r *ActivityRecord) {
			r.Type = typ
		})))
	}

	after, err := m.GetWorkSessionSummary("ws-1")
	require.NoError(t, err)
	assert.Equal(t, before.LastActivityAt, after.LastActivityAt, "an admin-only row does not move the session")

	only, err := m.GetWorkSessionSummary("ws-skill-only")
	require.NoError(t, err)
	assert.Nil(t, only, "no session of admin-only rows alone")

	indexed := 0
	require.NoError(t, m.db.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(WorkSessionRowsBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, _ []byte) error { indexed++; return nil })
	}))
	assert.Equal(t, 1, indexed, "only the tool call is indexed")

	// The stored row names no session either: retention decides "in a
	// session" from the row's own work_session_id, and the activity filter
	// work_session_id= would name a session whose timeline does not have it
	// (review round 15).
	rows, _, err := m.ListActivities(ActivityFilter{Types: []string{string(ActivityTypeSkillAccess), string(ActivityTypeSkillQuarantineChange)}, Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 4)
	for _, r := range rows {
		assert.Empty(t, r.WorkSessionID, "%s row keeps no work session id", r.Type)
	}
}
