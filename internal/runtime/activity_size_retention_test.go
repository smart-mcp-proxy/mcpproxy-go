package runtime

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func seedRuntimeActivities(t *testing.T, store *storage.Manager, prefix string, n, payload int) {
	t.Helper()
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		require.NoError(t, store.SaveActivity(&storage.ActivityRecord{
			ID:        fmt.Sprintf("%s-%02d", prefix, i),
			Type:      storage.ActivityTypeToolCall,
			Status:    "success",
			Response:  strings.Repeat("x", payload),
			Timestamp: now.Add(time.Duration(i) * time.Second), // all recent; last is newest
		}))
	}
}

// The size cap runs inside the existing retention cleanup and removes oldest
// records until the log is within budget, leaving the age/count caps intact.
func TestActivityRetention_SizeCapRemovesOldest(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	// Only the size cap should act: keep default age/count caps (7d / 10000),
	// seed recent records well under those, and set a small size budget.
	svc.SetRetentionConfig(0, 0, 0, 30*1024) // 30KB; age/count left unchanged
	seedRuntimeActivities(t, store, "rt", 10, 10*1024)

	svc.runRetentionCleanup()

	newest, err := store.GetActivity("rt-09")
	require.NoError(t, err)
	assert.NotNil(t, newest, "newest record must survive the size cap")
	oldest, err := store.GetActivity("rt-00")
	require.NoError(t, err)
	assert.Nil(t, oldest, "oldest record should be pruned by the size cap")

	// Log is now within budget: a direct size prune deletes nothing more.
	again, err := store.PruneActivitiesToSize(30 * 1024)
	require.NoError(t, err)
	assert.Equal(t, 0, again, "log should already be within the size budget")
}

func TestActivityRetention_SizeCapDisabled(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.SetRetentionConfig(0, 0, 0, 0) // disable size cap (age/count stay default)
	seedRuntimeActivities(t, store, "d", 5, 10*1024)

	svc.runRetentionCleanup()

	for i := 0; i < 5; i++ {
		rec, err := store.GetActivity(fmt.Sprintf("d-%02d", i))
		require.NoError(t, err)
		assert.NotNilf(t, rec, "d-%02d must survive when the size cap is disabled", i)
	}
}
