package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errUpdateInProgress reports that another mcpproxy process holds the
// self-update lock for the same target binary.
var errUpdateInProgress = errors.New("another mcpproxy update is already updating this binary")

// errUpdateTargetIsArtifact reports that the binary we were asked to replace is
// named like one of the files an update creates — see canonicalUpdateTarget.
var errUpdateTargetIsArtifact = errors.New("an update of this binary is already in progress")

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
	// Keyed on the CANONICAL binary, never on whatever name the caller happens
	// to hold. On Linux os.Executable() reads /proc/self/exe, which names the
	// running inode — so a process whose binary was renamed to <name>.old by a
	// concurrent update resolves its own path to <name>.old and would otherwise
	// take out a second, entirely separate "exclusive" lock. Both processes
	// have to contend on one file or the lock guarantees nothing.
	path := canonicalUpdateTarget(target) + updateLockSuffix
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

// canonicalUpdateTarget maps any of the files an update creates next to a
// binary back to the binary itself: mcpproxy.old, mcpproxy.updating,
// mcpproxy.update-lock and the staged .mcpproxy.new-<pid> all canonicalize to
// mcpproxy.
//
// Pure path arithmetic — no /proc, no stat — so it behaves identically on every
// platform even though only Linux can produce the aliasing it defends against.
// Applied repeatedly, because a swap of an already-aliased path would have
// produced names like mcpproxy.old.old.
//
// Used for the LOCK KEY, where mapping an unrelated user file named foo.old
// onto foo's lock costs nothing worse than two updates taking turns. Deciding
// which file to REPLACE is a different question with a different answer — see
// refuseAliasedUpdateTarget.
func canonicalUpdateTarget(target string) string {
	base := filepath.Base(target)
	canonical := base
	for range 4 { // bounded: each pass must shorten base, so this always settles
		stripped, ok := stripUpdateArtifactSuffix(canonical)
		if !ok {
			break
		}
		canonical = stripped
	}
	if canonical == base {
		// Nothing to canonicalize. Returned untouched rather than round-tripped
		// through filepath.Join, which would also Clean it — quietly rewriting
		// a path we were given no reason to rewrite.
		return target
	}
	return filepath.Join(filepath.Dir(target), canonical)
}

// stripUpdateArtifactSuffix removes one layer of update-artifact naming from a
// base name, reporting whether it removed anything.
func stripUpdateArtifactSuffix(base string) (string, bool) {
	for _, suffix := range []string{backupSuffix, swapSentinelSuffix, updateLockSuffix} {
		if len(base) > len(suffix) && strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix), true
		}
	}
	// The staged binary: ".<name>.new-<pid>". It is never executed, so it
	// cannot alias the way .old does, but a name we generate is a name we
	// should recognise.
	if strings.HasPrefix(base, ".") {
		if idx := strings.LastIndex(base, ".new-"); idx > 1 {
			if digits := base[idx+len(".new-"):]; digits != "" && isAllDigits(digits) {
				return base[1:idx], true
			}
		}
	}
	return base, false
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// refuseAliasedUpdateTarget rejects a target that is named like one of our own
// update artifacts, instead of quietly canonicalizing it.
//
// Stripping the suffix and updating the canonical binary would be wrong at
// least as often as it was right. Two things produce a target called
// mcpproxy.old:
//
//   - a concurrent update that has just renamed the binary aside, and this
//     process (on Linux) resolved /proc/self/exe afterwards — here the caller
//     must not touch anything;
//   - a user who kept a copy under that name and ran it — here rewriting
//     mcpproxy instead of the file they invoked is silently updating the wrong
//     binary.
//
// Nothing on disk reliably separates the two, and one of the answers is
// destructive, so neither gets guessed at.
func refuseAliasedUpdateTarget(target string) error {
	base := filepath.Base(target)
	if _, aliased := stripUpdateArtifactSuffix(base); !aliased {
		return nil
	}
	return fmt.Errorf("%w: %s is named like the working files mcpproxy creates while replacing a "+
		"binary, so updating it could overwrite another update's only backup.\n"+
		"If an update is running, wait for it to finish. If this is a copy you renamed yourself, "+
		"give it a name that does not end in %q, %q or %q and run the update again",
		errUpdateTargetIsArtifact, target, backupSuffix, swapSentinelSuffix, updateLockSuffix)
}
