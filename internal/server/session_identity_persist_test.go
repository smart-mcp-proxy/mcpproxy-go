package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func newPersistStore(t *testing.T) (*SessionStore, *storage.Manager) {
	t.Helper()
	mgr, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	store := NewSessionStore(zap.NewNop())
	store.SetStorageManager(mgr)
	return store, mgr
}

// FR-033: a persisted session row carries the credential identity and the
// LATEST profile resolution, so /sessions can filter on them.
func TestSessionPersist_CarriesIdentityAndLatestProfile(t *testing.T) {
	store, mgr := newPersistStore(t)
	store.SetSession("s1", "Cursor", "1.0", false, false, nil)
	store.SetSessionIdentity("s1", "client-cursor", "cursor")
	store.UpdateSessionProfile("s1", "work-readonly", "pin")

	store.EnsurePersisted("s1", nil)

	rec, err := mgr.GetSessionByID("s1")
	require.NoError(t, err)
	assert.Equal(t, "client-cursor", rec.TokenName)
	assert.Equal(t, "cursor", rec.ClientID)
	assert.Equal(t, "work-readonly", rec.Profile)
	assert.Equal(t, "pin", rec.ProfileSource)

	// A later resolution writes through to the persisted row.
	store.UpdateSessionProfile("s1", "work-full", "binding")
	rec, err = mgr.GetSessionByID("s1")
	require.NoError(t, err)
	assert.Equal(t, "work-full", rec.Profile)
	assert.Equal(t, "binding", rec.ProfileSource)
}

// R8: one small write per resolution CHANGE, never one per call.
func TestUpdateSessionProfile_WritesThroughOnlyOnChange(t *testing.T) {
	store, _ := newPersistStore(t)
	store.SetSession("s1", "Cursor", "1.0", false, false, nil)
	store.EnsurePersisted("s1", nil)

	writes := 0
	store.profileWriter = func(id, profile, source string) { writes++ }

	store.UpdateSessionProfile("s1", "work-readonly", "pin")
	store.UpdateSessionProfile("s1", "work-readonly", "pin")
	store.UpdateSessionProfile("s1", "work-readonly", "pin")
	assert.Equal(t, 1, writes, "identical resolutions cause one write")

	store.UpdateSessionProfile("s1", "work-full", "session")
	assert.Equal(t, 2, writes)
}

// A resolution that lands before the session is persisted is copied into the
// row when EnsurePersisted creates it, and causes no write of its own.
func TestUpdateSessionProfile_BeforePersistIsCopiedAtPersist(t *testing.T) {
	store, mgr := newPersistStore(t)
	store.SetSession("s1", "Cursor", "1.0", false, false, nil)

	writes := 0
	store.profileWriter = func(id, profile, source string) { writes++ }
	store.UpdateSessionProfile("s1", "work-readonly", "url")
	assert.Equal(t, 0, writes, "not persisted yet: nothing to write through to")

	store.EnsurePersisted("s1", nil)
	rec, err := mgr.GetSessionByID("s1")
	require.NoError(t, err)
	assert.Equal(t, "work-readonly", rec.Profile)
	assert.Equal(t, "url", rec.ProfileSource)
}
