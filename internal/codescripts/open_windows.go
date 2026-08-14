//go:build windows

package codescripts

import "os"

// openScriptFile opens a stored script for reading. Windows has no O_NOFOLLOW,
// so the symlink/reparse-point rejection is BEST-EFFORT: the path is Lstat'ed
// first and the descriptor re-verified by the caller after the open. The
// residual window is narrow and creating a symlink on Windows requires
// elevation (or developer mode) in the first place; the confinement boundary
// itself is the name validator, which does not depend on this check.
func openScriptFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errNonRegular
	}
	return os.Open(path)
}
