//go:build unix

package outputfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// withForeignOwner makes every file whose path satisfies isForeign appear
// to be owned by another local user (uid euid+1000), so the ownership checks
// can be exercised without root.
func withForeignOwner(t *testing.T, isForeign func(fi os.FileInfo) bool) {
	t.Helper()
	orig := fileOwner
	euid := currentEUID()
	fileOwner = func(fi os.FileInfo) (uint32, bool) {
		if isForeign(fi) {
			return euid + 1000, true
		}
		return orig(fi)
	}
	t.Cleanup(func() { fileOwner = orig })
}

// TestResolve_ForeignSymlinkAtRootRejected models another local user
// pre-creating a shared-directory root (e.g. /tmp/mcp-out) as a symlink to
// a directory of ours: the redirect must be refused.
func TestResolve_ForeignSymlinkAtRootRejected(t *testing.T) {
	shared := t.TempDir()
	victim := t.TempDir()
	root := filepath.Join(shared, "mcp-out")
	if err := os.Symlink(victim, root); err != nil {
		t.Fatal(err)
	}
	withForeignOwner(t, func(fi os.FileInfo) bool {
		return fi.Mode()&os.ModeSymlink != 0 && fi.Name() == "mcp-out"
	})
	_, err := Resolve([]string{root}, filepath.Join(victim, "x"), false)
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrRootUnavailable", err)
	}
}

// TestResolve_ForeignSymlinkInAncestorChainRejected: the foreign symlink is
// reached only by following an (allowed) own symlink first.
func TestResolve_ForeignSymlinkInAncestorChainRejected(t *testing.T) {
	base := t.TempDir()
	victim := t.TempDir()
	if err := os.Symlink(victim, filepath.Join(base, "evil")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "evil"), filepath.Join(base, "mine")); err != nil {
		t.Fatal(err)
	}
	withForeignOwner(t, func(fi os.FileInfo) bool {
		return fi.Mode()&os.ModeSymlink != 0 && fi.Name() == "evil"
	})
	root := filepath.Join(base, "mine")
	_, err := Resolve([]string{root}, filepath.Join(root, "x"), false)
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrRootUnavailable", err)
	}
}

func TestResolve_OwnSymlinkedRootStillAccepted(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	tg, err := Resolve([]string{link}, filepath.Join(link, "x"), false)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	_ = tg.Close()
}

// TestResolve_ForeignOwnedRootDirRejected: a real directory created at the
// root path by another user is not ours to write into.
func TestResolve_ForeignOwnedRootDirRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "mcp-out")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	withForeignOwner(t, func(fi os.FileInfo) bool { return fi.IsDir() && fi.Name() == "mcp-out" })
	_, err := Resolve([]string{root}, filepath.Join(root, "x"), false)
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrRootUnavailable", err)
	}
}

func TestResolve_WorldWritableRootRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "mcp-out")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve([]string{root}, filepath.Join(root, "x"), false)
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrRootUnavailable", err)
	}
}

func TestCheckSymlinkOwners_RootOwnedSymlinkAllowed(t *testing.T) {
	base := t.TempDir()
	real := t.TempDir()
	link := filepath.Join(base, "sys")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	orig := fileOwner
	fileOwner = func(fi os.FileInfo) (uint32, bool) {
		if fi.Mode()&os.ModeSymlink != 0 && fi.Name() == "sys" {
			return 0, true // e.g. macOS /tmp -> /private/tmp
		}
		return orig(fi)
	}
	t.Cleanup(func() { fileOwner = orig })
	if err := checkSymlinkOwners(filepath.Join(link, "out"), currentEUID()); err != nil {
		t.Fatalf("checkSymlinkOwners() = %v, want nil for a root-owned symlink", err)
	}
}
