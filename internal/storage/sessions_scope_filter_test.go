package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedScopeSessions(t *testing.T, m *Manager) {
	t.Helper()
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	recs := []*SessionRecord{
		{ID: "s-old-cursor", Status: "closed", ClientID: "cursor", TokenName: "client-cursor", Profile: "work-readonly", ProfileSource: "pin", StartTime: base, LastActivity: base},
		{ID: "s-api", Status: "active", StartTime: base.Add(time.Minute), LastActivity: base.Add(time.Minute)},
		{ID: "s-zed", Status: "active", ClientID: "zed", TokenName: "client-zed", Profile: "work-full", ProfileSource: "binding", StartTime: base.Add(2 * time.Minute), LastActivity: base.Add(2 * time.Minute)},
		{ID: "s-cursor2", Status: "active", ClientID: "cursor", TokenName: "client-cursor", Profile: "work-readonly", ProfileSource: "pin", StartTime: base.Add(3 * time.Minute), LastActivity: base.Add(3 * time.Minute)},
	}
	for _, r := range recs {
		require.NoError(t, m.CreateSession(r))
	}
}

func TestGetRecentSessions_ScopeFiltersBeforeTruncation(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	seedScopeSessions(t, m)

	got, total, err := m.GetRecentSessionsFiltered(SessionFilter{Limit: 1, ClientID: "cursor"})
	require.NoError(t, err)
	assert.Equal(t, 2, total, "total is the filtered count, not the bucket")
	require.Len(t, got, 1)
	assert.Equal(t, "s-cursor2", got[0].ID)

	got, total, err = m.GetRecentSessionsFiltered(SessionFilter{Limit: 10, Profile: "work-full"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, got, 1)
	assert.Equal(t, "s-zed", got[0].ID)

	got, total, err = m.GetRecentSessionsFiltered(SessionFilter{Limit: 10, TokenName: "client-cursor", Status: "closed"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, got, 1)
	assert.Equal(t, "s-old-cursor", got[0].ID)
}

func TestGetRecentSessions_UnattributedSentinel(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	seedScopeSessions(t, m)

	for name, f := range map[string]SessionFilter{
		"client":  {Limit: 10, ClientID: ScopeFilterUnattributed},
		"profile": {Limit: 10, Profile: ScopeFilterUnattributed},
		"token":   {Limit: 10, TokenName: ScopeFilterUnattributed},
	} {
		got, total, err := m.GetRecentSessionsFiltered(f)
		require.NoError(t, err, name)
		assert.Equal(t, 1, total, name)
		require.Len(t, got, 1, name)
		assert.Equal(t, "s-api", got[0].ID, name)
	}
}

func TestSetSessionProfile_UpdatesPersistedRowOnly(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	seedScopeSessions(t, m)

	require.NoError(t, m.SetSessionProfile("s-zed", "work-readonly", "session"))
	got, err := m.GetSessionByID("s-zed")
	require.NoError(t, err)
	assert.Equal(t, "work-readonly", got.Profile)
	assert.Equal(t, "session", got.ProfileSource)
	assert.Equal(t, "zed", got.ClientID, "other identity fields untouched")

	// An id that is not persisted is a no-op, not an error the caller must handle.
	assert.NoError(t, m.SetSessionProfile("not-persisted", "x", "url"))
	got, err = m.GetSessionByID("not-persisted")
	assert.Error(t, err)
	assert.Nil(t, got)
}

func TestSessionRecord_LegacyRowDecodesWithEmptyIdentity(t *testing.T) {
	var s SessionRecord
	require.NoError(t, json.Unmarshal([]byte(`{"id":"x","status":"active","start_time":"2026-01-01T00:00:00Z","last_activity":"2026-01-01T00:00:00Z"}`), &s))
	assert.Empty(t, s.TokenName)
	assert.Empty(t, s.ClientID)
	assert.Empty(t, s.Profile)
	assert.Empty(t, s.ProfileSource)
	out, err := json.Marshal(&s)
	require.NoError(t, err)
	for _, k := range []string{"token_name", "client_id", "profile", "profile_source"} {
		assert.NotContains(t, string(out), "\""+k+"\"")
	}
}
