package scanner

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// plantRedirect replaces <dataDir>/tpa-lkg with a symlink to a directory
// outside it, seeded with files an unanchored reconcile would delete.
func plantRedirect(t *testing.T) (dataDir, outside string, seeded []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	dataDir = t.TempDir()
	outside = t.TempDir()
	orphan := digestHex([]byte("someone else's recovery bundle"))
	for _, name := range []string{orphan + ".json", lkgTempPrefix + "victim"} {
		require.NoError(t, os.WriteFile(filepath.Join(outside, name), []byte("precious"), 0o600))
		seeded = append(seeded, name)
	}
	require.NoError(t, os.Symlink(outside, lkgDirPath(dataDir)))
	return dataDir, outside, seeded
}

func requireUntouched(t *testing.T, outside string, seeded []string) {
	t.Helper()
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	require.ElementsMatch(t, seeded, got, "files outside the LKG directory must be neither deleted nor added to")
	for _, name := range seeded {
		b, err := os.ReadFile(filepath.Join(outside, name))
		require.NoError(t, err)
		require.Equal(t, "precious", string(b))
	}
}

// The data directory is attacker-writable (SC-012): tpa-lkg replaced by a
// symlink must not redirect the startup reconcile's deletes.
func TestLKGReconcile_RefusesASymlinkedDirectory(t *testing.T) {
	dataDir, outside, seeded := plantRedirect(t)
	removed, err := lkgReconcile(dataDir, nil)
	require.Error(t, err)
	require.Empty(t, removed)
	requireUntouched(t, outside, seeded)
}

// ...nor the writes and renames of an activation.
func TestLKGWrite_RefusesASymlinkedDirectory(t *testing.T) {
	dataDir, outside, seeded := plantRedirect(t)
	data := []byte(`{"bundle_version":"0.1.0"}`)
	require.Error(t, lkgWrite(dataDir, digestHex(data), data, nil, false))
	requireUntouched(t, outside, seeded)
}

func TestLKG_RefusesANonDirectory(t *testing.T) {
	dataDir := t.TempDir()
	require.NoError(t, os.WriteFile(lkgDirPath(dataDir), []byte("not a directory"), 0o600))
	data := []byte(`{"bundle_version":"0.1.0"}`)
	require.Error(t, lkgWrite(dataDir, digestHex(data), data, nil, false))
	_, err := lkgReconcile(dataDir, nil)
	require.Error(t, err)
}

func TestLKGReconcile_AbsentDirectoryIsNotAnError(t *testing.T) {
	removed, err := lkgReconcile(t.TempDir(), nil)
	require.NoError(t, err)
	require.Empty(t, removed)
}

// A fresh directory (and an already-real one) keeps working and leaves no
// temp debris behind.
func TestLKGWrite_FreshDirectoryStillWorks(t *testing.T) {
	dataDir := t.TempDir()
	data := []byte(`{"bundle_version":"0.1.0"}`)
	digest := digestHex(data)
	sidecar := []byte("sidecar")
	require.NoError(t, lkgWrite(dataDir, digest, data, sidecar, true))
	require.NoError(t, lkgWrite(dataDir, digest, data, sidecar, true))
	entries, err := os.ReadDir(lkgDirPath(dataDir))
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.ElementsMatch(t, []string{digest + ".json", lkgSidecarName(digest, digestHex(sidecar))}, names)
	info, err := os.Lstat(lkgDirPath(dataDir))
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

// FR-011a: the data directory's entry for tpa-lkg is made durable on EVERY
// activation. A parent fsync that failed right after the directory was created
// must not be skipped on the retry just because the directory now exists.
func TestLKGWrite_FailedParentSyncIsRetriedBeforeSuccess(t *testing.T) {
	dataDir := t.TempDir()
	var parentSyncs int
	failFirst := true
	prev := lkgSyncDirFn
	lkgSyncDirFn = func(dir string, root *os.Root) error {
		if dir == dataDir {
			parentSyncs++
			if failFirst {
				failFirst = false
				return errors.New("injected parent fsync failure")
			}
		}
		return prev(dir, root)
	}
	t.Cleanup(func() { lkgSyncDirFn = prev })

	data := []byte(`{"bundle_version":"0.1.0"}`)
	digest := digestHex(data)
	require.Error(t, lkgWrite(dataDir, digest, data, nil, false), "a failed parent fsync refuses the activation")
	require.Equal(t, 1, parentSyncs)

	require.NoError(t, lkgWrite(dataDir, digest, data, nil, false))
	require.Equal(t, 2, parentSyncs, "the retry must sync the parent again before reporting success")
}
