//go:build !windows

package codescripts

import (
	"errors"
	"os"
	"syscall"
)

// openScriptFile opens a stored script for reading, rejecting a symlink at the
// final path component ATOMICALLY: O_NOFOLLOW makes the kernel refuse the open
// (ELOOP) instead of resolving the link, so there is no check-then-open window
// in which a regular file can be swapped for a link (FR-003).
func openScriptFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		// ELOOP is the no-follow rejection; EMLINK is what some BSD kernels
		// return for the same condition. Both mean "the final component is a
		// symlink", which for us is simply not a regular file.
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK) {
			return nil, errNonRegular
		}
		return nil, err
	}
	return f, nil
}
