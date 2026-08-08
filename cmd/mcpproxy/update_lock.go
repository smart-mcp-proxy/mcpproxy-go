package main

import (
	"errors"
	"fmt"
	"os"
)

// errUpdateInProgress reports that another mcpproxy process holds the
// self-update lock for the same target binary.
var errUpdateInProgress = errors.New("another mcpproxy update is already updating this binary")

// updateLockSuffix names the advisory lock file that serializes the whole
// recover-and-swap sequence per target. The file is deliberately NEVER
// unlinked: removing a held lock file lets a third process create-and-lock a
// fresh inode while a second still holds the old one, which is two "exclusive"
// holders. A leftover zero-byte <target>.update-lock is the documented cost.
const updateLockSuffix = ".update-lock"

// acquireUpdateLock takes an exclusive, non-blocking, OS-level advisory lock
// scoped to the target binary. It guards recoverInterruptedSwap AND the swap
// itself: without it, a concurrent `mcpproxy update` can read a half-finished
// sibling's sentinel/backup state, misclassify it, and delete the only
// known-good backup. The kernel releases the lock if the process dies, so a
// crash can never wedge future updates.
func acquireUpdateLock(target string) (release func(), err error) {
	path := target + updateLockSuffix
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- derived from the caller-resolved target path
	if err != nil {
		return nil, fmt.Errorf("open update lock %s: %w", path, err)
	}
	if lockErr := lockFileExclusiveNB(f); lockErr != nil {
		_ = f.Close()
		if errors.Is(lockErr, errWouldBlock) {
			return nil, fmt.Errorf("%w (lock: %s)", errUpdateInProgress, path)
		}
		return nil, fmt.Errorf("lock %s: %w", path, lockErr)
	}
	return func() { _ = f.Close() }, nil // closing the fd releases the lock
}
