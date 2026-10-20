package scanner

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// An LKG file that already exists sits in the data directory, which the
// threat model lets the attacker write. lkgWrite's idempotence check must read
// it through the descriptor-first, bounded reader — never os.ReadFile, which
// would read an arbitrarily large planted file into memory, or block on a FIFO
// swapped in after the type check (Sol 6.1 review, round 1).
func TestLKGWrite_ExistingFileIsReadThroughTheGuardedReader(t *testing.T) {
	dataDir := t.TempDir()
	data := []byte(`{"bundle_version":"0.1.0"}`)
	digest := digestHex(data)
	require.NoError(t, lkgWrite(dataDir, digest, data, nil, false))

	final := filepath.Join(lkgDirPath(dataDir), digest+".json")
	var mu sync.Mutex
	opened := 0
	prev := candidateOpenHook
	candidateOpenHook = func(p string) {
		if p == final {
			mu.Lock()
			opened++
			mu.Unlock()
		}
	}
	t.Cleanup(func() { candidateOpenHook = prev })

	require.NoError(t, lkgWrite(dataDir, digest, data, nil, false))
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, opened, "the existing LKG file must be compared through readCandidateFile")

	// A planted oversized file under the final name is replaced, not trusted.
	require.NoError(t, os.WriteFile(final, append(append([]byte{}, data...), make([]byte, 1<<20)...), 0o600))
	require.NoError(t, lkgWrite(dataDir, digest, data, nil, false))
	got, err := os.ReadFile(final)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

// The first activation creates tpa-lkg/; its entry in the data directory must
// be made durable too, or a power loss after the state commit can leave an
// Active record whose LKG directory never reached the disk (FR-011a).
func TestLKGWrite_FirstWriteSyncsTheParentDirectory(t *testing.T) {
	dataDir := t.TempDir()
	var synced []string
	prev := lkgSyncDirFn
	lkgSyncDirFn = func(dir string) error { synced = append(synced, dir); return prev(dir) }
	t.Cleanup(func() { lkgSyncDirFn = prev })

	data := []byte(`{"bundle_version":"0.1.0"}`)
	require.NoError(t, lkgWrite(dataDir, digestHex(data), data, nil, false))
	require.Contains(t, synced, lkgDirPath(dataDir))
	require.Contains(t, synced, dataDir, "creating tpa-lkg must fsync its parent")

	synced = nil
	other := []byte(`{"bundle_version":"0.1.1"}`)
	require.NoError(t, lkgWrite(dataDir, digestHex(other), other, nil, false))
	require.NotContains(t, synced, dataDir, "an existing tpa-lkg needs no parent fsync")
}
