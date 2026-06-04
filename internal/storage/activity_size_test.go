package storage

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveSizedActivities saves n records (id-00 oldest → id-(n-1) newest), each
// padded so its stored value is ~payload bytes.
func saveSizedActivities(t *testing.T, m *Manager, n, payload int) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		require.NoError(t, m.SaveActivity(&ActivityRecord{
			ID:        fmt.Sprintf("id-%02d", i),
			Type:      ActivityTypeToolCall,
			Status:    "success",
			Response:  strings.Repeat("x", payload),
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		}))
	}
}

// exists reports whether an activity record with the given id is present.
// GetActivity returns (nil, nil) — not an error — when a record is absent.
func exists(t *testing.T, m *Manager, id string) bool {
	t.Helper()
	rec, err := m.GetActivity(id)
	require.NoError(t, err)
	return rec != nil
}

func TestPruneActivitiesToSize_RemovesOldestUntilUnderBudget(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// 10 records × ~10KB ≈ 100KB total.
	saveSizedActivities(t, m, 10, 10*1024)
	budget := int64(45 * 1024) // only the newest few fit

	deleted, err := m.PruneActivitiesToSize(budget)
	require.NoError(t, err)
	assert.Greater(t, deleted, 0)

	// Oldest pruned, newest retained.
	assert.False(t, exists(t, m, "id-00"), "oldest record should be pruned")
	assert.True(t, exists(t, m, "id-09"), "newest record must be retained")

	// Idempotent: a second pass deletes nothing more.
	again, err := m.PruneActivitiesToSize(budget)
	require.NoError(t, err)
	assert.Equal(t, 0, again, "second pass should be a no-op")
}

func TestPruneActivitiesToSize_AlreadyUnderBudget_NoOp(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	saveSizedActivities(t, m, 5, 1024) // ~5KB total

	deleted, err := m.PruneActivitiesToSize(10 * 1024 * 1024) // 10MB budget
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
	for i := 0; i < 5; i++ {
		assert.True(t, exists(t, m, fmt.Sprintf("id-%02d", i)))
	}
}

func TestPruneActivitiesToSize_KeepsNewestEvenIfOverBudget(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	// 5 records × 10KB; budget smaller than a single record.
	saveSizedActivities(t, m, 5, 10*1024)

	deleted, err := m.PruneActivitiesToSize(1024) // 1KB < one record
	require.NoError(t, err)
	assert.Equal(t, 4, deleted, "all but the newest should be deleted")

	// Only the newest survives — the log is never emptied.
	assert.True(t, exists(t, m, "id-04"), "newest must survive")
	for i := 0; i < 4; i++ {
		assert.Falsef(t, exists(t, m, fmt.Sprintf("id-%02d", i)), "id-%02d should be pruned", i)
	}
}

func TestPruneActivitiesToSize_DisabledWhenZeroOrNegative(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	saveSizedActivities(t, m, 4, 10*1024)

	for _, budget := range []int64{0, -1} {
		deleted, err := m.PruneActivitiesToSize(budget)
		require.NoError(t, err)
		assert.Equalf(t, 0, deleted, "budget %d disables size pruning", budget)
	}
	for i := 0; i < 4; i++ {
		assert.True(t, exists(t, m, fmt.Sprintf("id-%02d", i)))
	}
}
