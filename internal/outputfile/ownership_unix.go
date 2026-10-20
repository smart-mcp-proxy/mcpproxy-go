//go:build unix

package outputfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Test seams: the real effective uid and the owner of a FileInfo. Tests
// override them to model files owned by another local user without root.
var (
	currentEUID = func() uint32 { return uint32(os.Geteuid()) }
	fileOwner   = func(fi os.FileInfo) (uint32, bool) {
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return 0, false
		}
		return st.Uid, true
	}
)

// maxSymlinkHops bounds the manual symlink walk (Linux's own limit is 40).
const maxSymlinkHops = 40

// checkRootOwnership closes the "another local user pre-creates the root"
// redirect: with a root such as /tmp/mcp-out, any local user can create
// /tmp/mcp-out as a symlink to a directory of OUR user (e.g. ~/.ssh) before
// mcpproxy first creates it, and every save would then land there.
//
// It walks configuredRoot component by component, following symlinks
// manually, and refuses any symlink owned by a uid other than root or our
// effective uid (macOS's root-owned /tmp -> /private/tmp stays allowed).
// The resolved root directory itself must be owned by our effective uid and
// must not be world-writable.
func checkRootOwnership(configuredRoot, resolvedRoot string) error {
	euid := currentEUID()
	if err := checkSymlinkOwners(filepath.Clean(configuredRoot), euid); err != nil {
		return err
	}
	fi, err := os.Lstat(resolvedRoot)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("root %q is not a directory", resolvedRoot)
	}
	uid, ok := fileOwner(fi)
	if !ok {
		return fmt.Errorf("cannot determine owner of root %q", resolvedRoot)
	}
	if uid != euid {
		return fmt.Errorf("root %q is owned by uid %d, not by the mcpproxy user (uid %d)", resolvedRoot, uid, euid)
	}
	if fi.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("root %q is world-writable", resolvedRoot)
	}
	return nil
}

// checkSymlinkOwners resolves path one component at a time and fails on the
// first symlink owned by neither root nor euid. Components that do not exist
// end the walk (nothing left to follow).
func checkSymlinkOwners(path string, euid uint32) error {
	pending := splitComponents(path)
	cur := string(os.PathSeparator)
	hops := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, part)
		fi, err := os.Lstat(next)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		hops++
		if hops > maxSymlinkHops {
			return fmt.Errorf("too many symlinks resolving %q", path)
		}
		uid, ok := fileOwner(fi)
		if !ok {
			return fmt.Errorf("cannot determine owner of symlink %q", next)
		}
		if uid != 0 && uid != euid {
			return fmt.Errorf("symlink %q on the root's path is owned by uid %d (only root or the mcpproxy user may own symlinks there)", next, uid)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return err
		}
		if filepath.IsAbs(target) {
			cur = string(os.PathSeparator)
		}
		pending = append(splitComponents(target), pending...)
	}
	return nil
}

func splitComponents(p string) []string {
	return strings.Split(p, string(os.PathSeparator))
}
