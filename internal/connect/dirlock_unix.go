//go:build !windows

package connect

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// acquireDirLock takes an exclusive advisory lock (flock) on the directory dir,
// for every process that does the same, and returns its release. It creates no
// file: the lock is on the directory's own inode. flock locks belong to the open
// file description, so two handles opened separately, even in one process, do
// exclude each other.
//
// It retries without blocking until wait has passed and then fails with a
// "locked by another" error, before the caller has written anything. A directory
// that cannot be opened, or a filesystem that cannot lock (NFS without lockd,
// ENOLCK, ENOTSUP), is not contention: the lock is then skipped (no-op release)
// and the in-process mutex and the owner token remain the only guards.
func acquireDirLock(dir string, wait time.Duration) (func(), error) {
	f, err := os.Open(dir)
	if err != nil {
		return func() {}, nil
	}
	deadline := time.Now().Add(wait)
	delay := 5 * time.Millisecond
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() {
				_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = f.Close()
			return func() {}, nil // this filesystem cannot lock: not contention
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("%s is locked by another MCPProxy process writing client configs; try again", dir)
		}
		time.Sleep(delay)
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}
