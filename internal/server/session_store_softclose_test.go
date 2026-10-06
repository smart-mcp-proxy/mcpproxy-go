package server

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// #1205: an unregister followed by a re-register on the same id (no new
// initialize) must keep client attribution and the cached work session.
func TestSessionStore_SoftCloseKeepsAttributionAcrossReRegister(t *testing.T) {
	mgr, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	store := NewSessionStore(zap.NewNop())
	store.SetStorageManager(mgr)
	store.SetSession("s1", "claude-code", "1.2", false, false, nil)

	ws := store.EnsurePersisted("s1", func(*SessionInfo) string { return "ws-1" })
	require.Equal(t, "ws-1", ws)

	store.RemoveSession("s1")
	rec, err := mgr.GetSessionByID("s1")
	require.NoError(t, err)
	assert.Equal(t, "closed", rec.Status)
	assert.Zero(t, store.Count(), "soft-closed session is not live")

	// Attribution survives the unregister.
	require.NotNil(t, store.GetSession("s1"))
	assert.Equal(t, "claude-code", store.GetSession("s1").ClientName)
	assert.Equal(t, "ws-1", store.WorkSessionID("s1"))

	// Next activity revives it: same work session, no duplicate record, row active.
	store.UpdateActivity("s1")
	assert.Equal(t, 1, store.Count())
	assert.Equal(t, "ws-1", store.EnsurePersisted("s1", func(*SessionInfo) string { return "other" }))
	rec, err = mgr.GetSessionByID("s1")
	require.NoError(t, err)
	assert.Equal(t, "active", rec.Status)
	assert.Nil(t, rec.EndTime)
	assert.Equal(t, "claude-code", rec.ClientName)
}

func TestSessionStore_ReopenOnRegisterHook(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	store.SetSession("s1", "codex", "1", false, false, nil)
	store.SetActiveProfile("s1", "work")
	store.RemoveSession("s1")
	assert.Equal(t, "work", store.GetActiveProfile("s1"), "set_profile selection survives a soft close")

	store.Reopen("s1")
	assert.Equal(t, 1, store.Count())
	assert.Equal(t, "codex", store.GetSession("s1").ClientName)
	store.Reopen("unknown") // no-op
}

func TestSessionStore_ClosedEntriesEvictedAfterTTL(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	now := time.Now()
	store.now = func() time.Time { return now }

	store.SetSession("s1", "a", "1", false, false, nil)
	store.SetActiveProfile("s1", "p")
	store.RemoveSession("s1")

	now = now.Add(closedSessionTTL - time.Minute)
	store.SetSession("s2", "b", "1", false, false, nil) // triggers sweep
	assert.NotNil(t, store.GetSession("s1"), "kept before the TTL")

	now = now.Add(2 * time.Minute)
	store.SetSession("s3", "c", "1", false, false, nil) // triggers sweep
	assert.Nil(t, store.GetSession("s1"), "evicted after the TTL")
	assert.Empty(t, store.GetActiveProfile("s1"))
	assert.NotNil(t, store.GetSession("s2"))
}

func TestSessionStore_RemoveRacesUpdateActivity(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	store.SetSession("s1", "claude-code", "1", false, false, nil)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			store.RemoveSession("s1")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			store.UpdateActivity("s1")
			store.UpdateSessionStats("s1", 1)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			store.Reopen("s1")
			_ = store.GetSession("s1")
			_ = store.Count()
		}
	}()
	wg.Wait()

	require.NotNil(t, store.GetSession("s1"))
	assert.Equal(t, "claude-code", store.GetSession("s1").ClientName)
}
