package storage_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage/purgetest"
)

// The TPA bundle re-protection marker (Spec 101 FR-019, tasks B17) is the
// startup catch-up's only memory: it must survive a reopen, read as the zero
// value when absent, fail loudly on a corrupt value and ignore server purges.

const reprotectDigest = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

func newReprotectManager(t *testing.T) *storage.Manager {
	t.Helper()
	m, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestBundleReprotectState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	mgr, err := storage.NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, mgr.SaveBundleReprotectState(storage.BundleReprotectState{FullDigest: reprotectDigest, CompletedAt: now}))

	got, err := mgr.LoadBundleReprotectState()
	require.NoError(t, err)
	assert.Equal(t, reprotectDigest, got.FullDigest)
	assert.True(t, got.CompletedAt.Equal(now))

	require.NoError(t, mgr.Close())
	reopened, err := storage.NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	got, err = reopened.LoadBundleReprotectState()
	require.NoError(t, err)
	assert.Equal(t, reprotectDigest, got.FullDigest, "the marker survives a reopen")
}

func TestBundleReprotectState_AbsentIsZeroValueAndWritesNothing(t *testing.T) {
	m := newReprotectManager(t)

	got, err := m.LoadBundleReprotectState()
	require.NoError(t, err)
	assert.Equal(t, storage.BundleReprotectState{}, got)

	// A read never creates the key (the load is a View transaction).
	require.NoError(t, m.GetDB().View(func(tx *bbolt.Tx) error {
		if meta := tx.Bucket([]byte(storage.MetaBucket)); meta != nil {
			assert.Nil(t, meta.Get([]byte(storage.BundleReprotectDoneKey)), "a read must not create the marker")
		}
		return nil
	}))
}

func TestBundleReprotectState_CorruptValueIsAnErrorNotAPanic(t *testing.T) {
	m := newReprotectManager(t)
	require.NoError(t, m.GetDB().Update(func(tx *bbolt.Tx) error {
		meta, err := tx.CreateBucketIfNotExists([]byte(storage.MetaBucket))
		require.NoError(t, err)
		return meta.Put([]byte(storage.BundleReprotectDoneKey), []byte("{not json"))
	}))

	assert.NotPanics(t, func() {
		_, err := m.LoadBundleReprotectState()
		assert.Error(t, err, "an unreadable marker must be an error so the caller can decide, never a silent zero")
	})
}

func TestBundleReprotectState_NotTouchedByServerPurge(t *testing.T) {
	m := newReprotectManager(t)
	seedRows(t, m, 1, "acme")
	purgetest.Seed(t, m, "acme")
	require.NoError(t, m.SaveBundleReprotectState(storage.BundleReprotectState{FullDigest: reprotectDigest, CompletedAt: time.Now().UTC()}))

	require.NoError(t, m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: 2, Removes: []string{"acme"}}))
	purgetest.AssertPurged(t, m, "acme", "ApplyServerCommit.Removes")

	got, err := m.LoadBundleReprotectState()
	require.NoError(t, err)
	assert.Equal(t, reprotectDigest, got.FullDigest, "the marker is installation-wide state; removing a server leaves it alone")
}
