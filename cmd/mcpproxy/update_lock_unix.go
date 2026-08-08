//go:build !windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// errWouldBlock is what lockFileExclusiveNB returns when another process
// already holds the lock.
var errWouldBlock = unix.EWOULDBLOCK

// lockFileExclusiveNB takes a non-blocking exclusive flock on f. flock locks
// belong to the open file description, so they conflict even between two
// opens in the same process, and the kernel drops them when the process exits.
func lockFileExclusiveNB(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EAGAIN) {
		return errWouldBlock
	}
	return err
}
