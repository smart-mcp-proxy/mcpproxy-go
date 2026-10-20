package scanner

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// A rollback names its target path in an admin request body. The path is
// confined to the directory of the configured bundle (security.tpa_bundle_path):
// anything else is a usage error, refused BEFORE the file is opened, so the
// rollback door cannot be used to probe the existence, type or digest of
// arbitrary files the proxy can read.
func TestRollback_PathIsConfinedToTheBundleDirectory(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	a, _, aPath := twoReleases(t, f)

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "previous-release.json")
	f.putAt(outside, a)

	var opened atomic.Int32
	prev := candidateOpenHook
	candidateOpenHook = func(p string) {
		if filepath.Dir(p) == outsideDir || filepath.Base(p) == "escape.json" {
			opened.Add(1)
		}
	}
	t.Cleanup(func() { candidateOpenHook = prev })

	for name, p := range map[string]string{
		"other directory": outside,
		"dot-dot escape":  filepath.Join(f.drop, "..", filepath.Base(outsideDir), "previous-release.json"),
		"relative path":   "previous-release.json",
		"the directory":   f.drop,
		"sibling prefix":  f.drop + "-x/escape.json",
	} {
		_, err := RollbackBundle(BundleRollbackRequest{Path: p, Digest: digestOf(a.Bundle)}, f.log)
		require.Error(t, err, name)
		require.True(t, errors.Is(err, ErrBundleRollbackUsage), "%s: want a usage error, got %v", name, err)
	}
	require.Zero(t, opened.Load(), "a path outside the bundle directory must never be opened")

	// Inside the directory still works.
	_, err := RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)}, f.log)
	require.NoError(t, err)
	_ = os.Remove(outside)
}

// With no bundle path configured there is no directory to confine to, so a
// rollback by path is refused; the embedded default stays reachable.
func TestRollback_PathNeedsAConfiguredBundlePath(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.sec.TPABundlePath = ""
	ConfigureBundleFromConfig(f.sec, f.log)
	_, err := RollbackBundle(BundleRollbackRequest{Path: filepath.Join(f.drop, "x.json"), Digest: digestOf([]byte("x"))}, f.log)
	require.True(t, errors.Is(err, ErrBundleRollbackUsage), "got %v", err)
}
