//go:build windows

package connect

import "time"

// acquireDirLock is a no-op on Windows: LockFileEx does not lock a directory
// handle cleanly, so there is no cross-process directory lock there. The
// in-process mutex (lockConfig) and the owner token in the undo state remain the
// guards; the undo state's fail-closed rules cover what they leave.
func acquireDirLock(dir string, wait time.Duration) (func(), error) {
	return func() {}, nil
}
