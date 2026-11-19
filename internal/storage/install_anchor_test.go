package storage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// Spec 126 FR-033 (T003): the install anchor is written once, only when the
// data store held no prior data when this process opened it.

func openAnchorTestManager(t *testing.T, dir string) *Manager {
	t.Helper()
	m, err := NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	return m
}

// (a) A store opened on a new file records the anchor once EnsureInstallAnchor runs.
func TestInstallAnchor_NewFileRecordsAnchor(t *testing.T) {
	dir := t.TempDir()
	m := openAnchorTestManager(t, dir)
	defer m.Close()

	require.True(t, m.WasEmptyAtOpen(), "a store opened on an absent file was empty at open")
	_, ok, err := m.InstallAnchor()
	require.NoError(t, err)
	require.False(t, ok, "no anchor before EnsureInstallAnchor")

	now := time.Date(2026, 11, 18, 9, 30, 0, 0, time.FixedZone("UTC+2", 2*3600))
	got, ok, err := m.EnsureInstallAnchor(now)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, got.Equal(now))
	require.Equal(t, time.UTC, got.Location(), "the anchor is a UTC time")

	read, ok, err := m.InstallAnchor()
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, read.Equal(now))
}

// (b) A store opened on an existing file with data and no anchor stays
// without one (an upgrade from a build without the anchor).
func TestInstallAnchor_ExistingStoreWithoutAnchorStaysWithout(t *testing.T) {
	dir := t.TempDir()
	// An older build: it created the buckets and wrote data, but no anchor.
	m := openAnchorTestManager(t, dir)
	require.NoError(t, m.GetDB().Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(MetaBucket)).Put([]byte("some_older_key"), []byte("x"))
	}))
	require.NoError(t, m.Close())

	m2 := openAnchorTestManager(t, dir)
	defer m2.Close()
	require.False(t, m2.WasEmptyAtOpen(), "a store with buckets is not empty")
	_, ok, err := m2.EnsureInstallAnchor(time.Now())
	require.NoError(t, err)
	require.False(t, ok, "an existing store without an anchor must stay without one")
	_, ok, err = m2.InstallAnchor()
	require.NoError(t, err)
	require.False(t, ok)
}

// (c) An existing empty file (0 bytes, a valid bbolt file with no buckets, or
// one whose buckets hold no record) counts as new.
func TestInstallAnchor_EmptyFileCountsAsNew(t *testing.T) {
	t.Run("zero bytes", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.db"), nil, 0o600))
		m := openAnchorTestManager(t, dir)
		defer m.Close()
		require.True(t, m.WasEmptyAtOpen())
		_, ok, err := m.EnsureInstallAnchor(time.Now())
		require.NoError(t, err)
		require.True(t, ok)
	})
	t.Run("no buckets", func(t *testing.T) {
		dir := t.TempDir()
		raw, err := bbolt.Open(filepath.Join(dir, "config.db"), 0o600, nil)
		require.NoError(t, err)
		require.NoError(t, raw.Close())
		fi, err := os.Stat(filepath.Join(dir, "config.db"))
		require.NoError(t, err)
		require.Greater(t, fi.Size(), int64(0), "a bucketless bbolt file still has pages")

		m := openAnchorTestManager(t, dir)
		defer m.Close()
		require.True(t, m.WasEmptyAtOpen())
		_, ok, err := m.EnsureInstallAnchor(time.Now())
		require.NoError(t, err)
		require.True(t, ok)
	})
	t.Run("only the buckets and schema version a real open writes", func(t *testing.T) {
		// A standalone CLI command run before the first serve opens the store
		// through the real NewBoltDB: it creates every bucket and writes
		// meta/schema_version (initBuckets), nothing else.
		dir := t.TempDir()
		cli, err := NewBoltDB(dir, zap.NewNop().Sugar())
		require.NoError(t, err)
		require.True(t, cli.WasEmptyAtOpen())
		require.NoError(t, cli.Close())

		m := openAnchorTestManager(t, dir)
		defer m.Close()
		require.True(t, m.WasEmptyAtOpen(), "buckets without records, and meta/schema_version, still count as empty")
		_, ok, err := m.EnsureInstallAnchor(time.Now())
		require.NoError(t, err)
		require.True(t, ok)
	})
	t.Run("a record in a nested bucket is data", func(t *testing.T) {
		dir := t.TempDir()
		m := openAnchorTestManager(t, dir)
		require.NoError(t, m.GetDB().Update(func(tx *bbolt.Tx) error {
			nb, err := tx.Bucket([]byte(UpstreamsBucket)).CreateBucketIfNotExists([]byte("nested"))
			if err != nil {
				return err
			}
			return nb.Put([]byte("k"), []byte("v"))
		}))
		require.NoError(t, m.Close())
		m2 := openAnchorTestManager(t, dir)
		defer m2.Close()
		require.False(t, m2.WasEmptyAtOpen())
	})
}

// Only EnsureInstallAnchor writes the anchor. An upgraded store with no
// onboarding record goes through the onboarding missing-record branch on every
// MCP initialize, connect and mark; none of that may turn it into "fresh".
func TestInstallAnchor_UpgradedStoreStaysWithoutAnchorAfterOnboardingWrites(t *testing.T) {
	dir := t.TempDir()
	old := openAnchorTestManager(t, dir)
	require.NoError(t, old.GetDB().Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(UpstreamsBucket)).Put([]byte("github"), []byte(`{"name":"github"}`))
	}))
	require.NoError(t, old.Close())

	m := openAnchorTestManager(t, dir)
	defer m.Close()
	require.False(t, m.WasEmptyAtOpen())
	_, ok, err := m.EnsureInstallAnchor(time.Now()) // startup
	require.NoError(t, err)
	require.False(t, ok)

	st, err := m.GetOnboardingState() // no record yet
	require.NoError(t, err)
	_ = st
	// MCP initialize, then a client connect: both reach the missing-record
	// branch of UpdateOnboardingState.
	for i := 0; i < 2; i++ {
		require.NoError(t, m.UpdateOnboardingState(func(s *OnboardingState) error {
			s.Engaged = true
			return nil
		}))
	}
	_, ok, err = m.EnsureInstallAnchor(time.Now())
	require.NoError(t, err)
	require.False(t, ok, "onboarding writes must never create an anchor")
	_, ok, err = m.InstallAnchor()
	require.NoError(t, err)
	require.False(t, ok)
}

// The anchor lives in its own bucket, outside the OnboardingState record that
// older binaries round-trip whole.
func TestInstallAnchor_StoredInItsOwnBucket(t *testing.T) {
	dir := t.TempDir()
	m := openAnchorTestManager(t, dir)
	defer m.Close()
	_, ok, err := m.EnsureInstallAnchor(time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, m.GetDB().View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(InstallAnchorBucket))
		require.NotNil(t, b)
		require.NotNil(t, b.Get([]byte(InstallAnchorKey)))
		require.Nil(t, tx.Bucket([]byte(OnboardingBucket)).Get([]byte(OnboardingStateKey)), "no onboarding record is written")
		return nil
	}))
}

// (d) A second start never changes the anchor, and neither does a second call.
func TestInstallAnchor_NeverRewritten(t *testing.T) {
	dir := t.TempDir()
	first := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	m := openAnchorTestManager(t, dir)
	_, ok, err := m.EnsureInstallAnchor(first)
	require.NoError(t, err)
	require.True(t, ok)
	again, ok, err := m.EnsureInstallAnchor(first.Add(48 * time.Hour))
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, again.Equal(first), "a second call in the same process keeps the first anchor")
	require.NoError(t, m.Close())

	m2 := openAnchorTestManager(t, dir)
	defer m2.Close()
	require.False(t, m2.WasEmptyAtOpen())
	got, ok, err := m2.EnsureInstallAnchor(first.Add(240 * time.Hour))
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, got.Equal(first), "a later start keeps the anchor")
}

// (e) Concurrent callers produce one anchor (run under -race).
func TestInstallAnchor_ConcurrentCallersProduceOneAnchor(t *testing.T) {
	dir := t.TempDir()
	m := openAnchorTestManager(t, dir)
	defer m.Close()

	base := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	const n = 16
	results := make([]time.Time, n)
	oks := make([]bool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], oks[i], errs[i] = m.EnsureInstallAnchor(base.Add(time.Duration(i) * time.Minute))
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		require.True(t, oks[i])
		require.True(t, results[i].Equal(results[0]), "every caller sees the one anchor")
	}
	stored, ok, err := m.InstallAnchor()
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, stored.Equal(results[0]))
}

// A corrupt anchor value is reported as an error, never as a fresh anchor, and
// EnsureInstallAnchor does not overwrite it.
func TestInstallAnchor_CorruptValueIsNotRewritten(t *testing.T) {
	dir := t.TempDir()
	m := openAnchorTestManager(t, dir)
	defer m.Close()
	require.NoError(t, m.GetDB().Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(InstallAnchorBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(InstallAnchorKey), []byte("not-a-time"))
	}))
	_, _, err := m.EnsureInstallAnchor(time.Now())
	require.Error(t, err)
	_, _, err = m.InstallAnchor()
	require.Error(t, err)
	require.NoError(t, m.GetDB().View(func(tx *bbolt.Tx) error {
		require.Equal(t, "not-a-time", string(tx.Bucket([]byte(InstallAnchorBucket)).Get([]byte(InstallAnchorKey))))
		return nil
	}))
}

// A store populated by another process while this one waits for the lock is
// not a new install: emptiness is decided from the locked database, never
// from a stat taken before bbolt.Open (review finding, Spec 126 FR-033).
func TestNewBoltDB_StorePopulatedBeforeLockIsNotEmpty(t *testing.T) {
	dir := t.TempDir()
	testHookBeforeOpen = func(dbPath string) {
		other, err := bbolt.Open(dbPath, 0600, nil)
		if err != nil {
			t.Fatalf("other process open: %v", err)
		}
		if err := other.Update(func(tx *bbolt.Tx) error {
			b, err := tx.CreateBucketIfNotExists([]byte("upstreams"))
			if err != nil {
				return err
			}
			return b.Put([]byte("github"), []byte(`{"name":"github"}`))
		}); err != nil {
			t.Fatalf("other process write: %v", err)
		}
		if err := other.Close(); err != nil {
			t.Fatalf("other process close: %v", err)
		}
	}
	t.Cleanup(func() { testHookBeforeOpen = nil })

	db, err := NewBoltDB(dir, zap.NewNop().Sugar())
	if err != nil {
		t.Fatalf("NewBoltDB: %v", err)
	}
	defer db.Close()
	if db.WasEmptyAtOpen() {
		t.Fatal("a store populated before the lock was taken must not read as empty")
	}
	if _, wrote, err := db.EnsureInstallAnchor(time.Now()); err != nil || wrote {
		t.Fatalf("EnsureInstallAnchor on an existing store: wrote=%v err=%v, want no anchor", wrote, err)
	}
}
