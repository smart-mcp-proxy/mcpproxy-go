//go:build darwin

package main

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// singleInstanceLock keeps the locked file open (and out of the GC's reach,
// whose finalizer would close it) for the life of the process.
var singleInstanceLock *os.File //nolint:unused // written only: holding the reference is the point

// acquireSingleInstance takes an exclusive flock on ~/.mcpproxy/<id>.lock and
// reports whether this process is the only one holding it. The kernel drops the
// lock when the process exits, however it exits. The macOS app
// (native/macos, InstancePaths.trayLockURL) locks the same file, so a Go tray
// and the Swift tray exclude each other too.
func acquireSingleInstance(id string) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	dir := filepath.Join(home, ".mcpproxy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return false, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return false, nil
		}
		return false, err
	}
	singleInstanceLock = f
	return true, nil
}
