package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 124 FR-025: an upstream event about one user's credential is readable
// only by that user and by administrators, and the predicate applies before
// counting and paging.
func TestActivityEventViewer_OwnedEventsAreForTheirOwner(t *testing.T) {
	owned := &ActivityRecord{Type: ActivityTypeUpstreamEvent, ServerName: "github", UserID: "alice"}
	shared := &ActivityRecord{Type: ActivityTypeUpstreamEvent, ServerName: "github"}
	call := &ActivityRecord{Type: ActivityTypeToolCall, ServerName: "github", UserID: "alice"}

	var admin *ActivityEventViewer
	alice := &ActivityEventViewer{UserID: "alice"}
	bob := &ActivityEventViewer{UserID: "bob"}
	token := &ActivityEventViewer{} // an agent token with no owning user

	for _, r := range []*ActivityRecord{owned, shared, call} {
		assert.True(t, admin.MaySee(r), "an administrator sees everything")
		assert.True(t, alice.MaySee(r))
	}
	assert.False(t, bob.MaySee(owned), "another user's credential event is withheld")
	assert.True(t, bob.MaySee(shared), "a shared event is not")
	assert.True(t, bob.MaySee(call), "the predicate only concerns upstream events")
	assert.False(t, token.MaySee(owned), "a caller that is no user sees no owned event")
	assert.True(t, token.MaySee(shared))
}

func TestActivityEventViewer_AppliesBeforeCountAndPage(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	base := time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC)
	for i, owner := range []string{"alice", "bob", "", "alice"} {
		require.NoError(t, m.SaveActivity(&ActivityRecord{
			Type: ActivityTypeUpstreamEvent, ServerName: "github", Status: ActivityStatusError,
			UserID: owner, Timestamp: base.Add(time.Duration(i) * time.Second),
		}))
	}

	all, total, err := m.ListActivities(ActivityFilter{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, all, 4)
	assert.Equal(t, 4, total)

	page, total, err := m.ListActivities(ActivityFilter{Limit: 1, EventViewer: &ActivityEventViewer{UserID: "bob"}})
	require.NoError(t, err)
	assert.Equal(t, 2, total, "the total counts only what the viewer may read: bob's own and the shared one")
	require.Len(t, page, 1)
	assert.NotEqual(t, "alice", page[0].UserID)
}
