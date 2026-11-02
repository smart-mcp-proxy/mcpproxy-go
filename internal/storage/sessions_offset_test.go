package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRecentSessionsFiltered_Offset(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	seedScopeSessions(t, m) // newest first: s-cursor2, s-zed, s-api, s-old-cursor

	got, total, err := m.GetRecentSessionsFiltered(SessionFilter{Limit: 2, Offset: 1})
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	assert.Equal(t, []string{"s-zed", "s-api"}, sessionIDs(got))

	got, total, err = m.GetRecentSessionsFiltered(SessionFilter{Limit: 10, Offset: 1, ClientID: "cursor"})
	require.NoError(t, err)
	assert.Equal(t, 2, total, "total stays the filtered count")
	assert.Equal(t, []string{"s-old-cursor"}, sessionIDs(got))

	got, total, err = m.GetRecentSessionsFiltered(SessionFilter{Limit: 10, Offset: 9})
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	assert.Empty(t, got)
}
