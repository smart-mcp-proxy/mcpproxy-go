//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// errWouldBlock is what lockFileExclusiveNB returns when another process
// already holds the lock.
var errWouldBlock = errors.New("update lock held by another process")

// lockFileExclusiveNB takes a non-blocking exclusive LockFileEx on the first
// byte of f. Windows releases the region lock when the handle is closed or
// the process exits.
func lockFileExclusiveNB(f *os.File) error {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errWouldBlock
	}
	return err
}
