//go:build !unix && !windows

package scanner

import (
	"os"
)

// openCandidateDescriptor is the fallback for targets with neither O_NOFOLLOW
// nor reparse-point handles (plan9, js/wasm). It cannot be atomic, so it
// narrows the window as far as it can: Lstat refuses a symlink up front, the
// file is opened, and the type is then re-checked on the descriptor. These
// targets are not shipped; the stub exists so the package builds everywhere.
func openCandidateDescriptor(path string) (*os.File, int64, error) {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, 0, notRegularError("not a regular file: " + fi.Mode().String())
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, 0, notRegularError("not a regular file: " + fi.Mode().String())
	}
	return f, fi.Size(), nil
}
