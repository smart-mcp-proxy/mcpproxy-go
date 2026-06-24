//go:build linux

package sandbox

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Filesystem access-right groups. Read-only entries get read+execute; the
// read-write group adds every mutating right. The full set is masked down to
// what the running kernel's Landlock ABI actually understands (see
// handledAccessFS) — handing a ruleset bits from a newer ABI yields EINVAL.
const (
	accessFSRead = unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR

	accessFSWrite = unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM |
		unix.LANDLOCK_ACCESS_FS_REFER | // ABI 2
		unix.LANDLOCK_ACCESS_FS_TRUNCATE | // ABI 3
		unix.LANDLOCK_ACCESS_FS_IOCTL_DEV // ABI 5

	accessFSReadWrite = accessFSRead | accessFSWrite
)

// handledAccessFS returns the set of filesystem access rights the ruleset
// should "handle" (i.e. deny by default unless granted), masked to the bits the
// given Landlock ABI version supports. This is the standard best-effort pattern
// so the same binary degrades cleanly from a 6.10 kernel down to 5.13.
func handledAccessFS(abi int) uint64 {
	h := uint64(accessFSReadWrite)
	if abi < 5 {
		h &^= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	if abi < 3 {
		h &^= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi < 2 {
		h &^= unix.LANDLOCK_ACCESS_FS_REFER
	}
	return h
}

// Apply confines the current process per spec. On success the calling process
// — and every process it subsequently execs — can only touch the filesystem
// subtrees in the allowlist, under the supplied rlimits. The restriction is
// irreversible for the lifetime of the process, which is why the intended
// caller is a short-lived re-exec wrapper that calls Apply and immediately
// execs the untrusted command.
func Apply(spec Spec) (Report, error) {
	var rep Report

	// Resource limits first — cheap, and independent of Landlock availability.
	for i := range spec.Rlimits {
		rl := spec.Rlimits[i]
		lim := unix.Rlimit{Cur: rl.Cur, Max: rl.Max}
		if err := unix.Setrlimit(rl.Resource, &lim); err != nil {
			return rep, fmt.Errorf("sandbox: setrlimit(%d): %w", rl.Resource, err)
		}
		rep.RlimitsSet++
	}

	if !spec.wantsLandlock() {
		rep.LandlockNote = "no filesystem allowlist requested; rlimits only"
		return rep, nil
	}

	abi, err := landlockABI()
	if err != nil || abi < 1 {
		rep.LandlockABI = -1
		rep.LandlockNote = fmt.Sprintf("Landlock unavailable on this kernel (%v)", err)
		if spec.BestEffort {
			return rep, nil
		}
		return rep, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}

	handled := handledAccessFS(abi)
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	rulesetFD, err := landlockCreateRuleset(&attr)
	if err != nil {
		return rep, fmt.Errorf("sandbox: landlock_create_ruleset: %w", err)
	}
	defer unix.Close(rulesetFD)

	var missing []string
	addAll := func(paths []string, access uint64) error {
		access &= handled
		for _, p := range paths {
			ok, addErr := addPathRule(rulesetFD, p, access)
			if addErr != nil {
				return fmt.Errorf("sandbox: add rule for %q: %w", p, addErr)
			}
			if !ok {
				missing = append(missing, p)
			}
		}
		return nil
	}
	if err := addAll(spec.ReadOnlyPaths, accessFSRead); err != nil {
		return rep, err
	}
	if err := addAll(spec.ReadWritePaths, accessFSReadWrite); err != nil {
		return rep, err
	}

	// Landlock requires no_new_privs so a confined process cannot regain
	// privileges via a SUID binary and escape the ruleset.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return rep, fmt.Errorf("sandbox: prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}
	rep.NoNewPrivs = true

	if err := landlockRestrictSelf(rulesetFD); err != nil {
		return rep, fmt.Errorf("sandbox: landlock_restrict_self: %w", err)
	}

	rep.LandlockABI = abi
	if len(missing) > 0 {
		rep.LandlockNote = "skipped missing paths: " + strings.Join(missing, ", ")
	}
	return rep, nil
}

// addPathRule grants access to the subtree beneath path. Returns (false, nil)
// when the path does not exist so the caller can record it as skipped rather
// than fail the whole confinement.
func addPathRule(rulesetFD int, path string, access uint64) (bool, error) {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		if err == unix.ENOENT {
			return false, nil
		}
		return false, err
	}
	defer unix.Close(fd)

	beneath := unix.LandlockPathBeneathAttr{
		Allowed_access: access,
		Parent_fd:      int32(fd),
	}
	if err := landlockAddPathBeneath(rulesetFD, &beneath); err != nil {
		return false, err
	}
	return true, nil
}

// --- raw syscall wrappers (x/sys/unix v0.46 ships the numbers/types but not
// high-level helpers for these three syscalls) -----------------------------

// landlockABI queries the kernel's supported Landlock ABI version via
// landlock_create_ruleset(NULL, 0, LANDLOCK_CREATE_RULESET_VERSION).
func landlockABI() (int, error) {
	r, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, uintptr(unix.LANDLOCK_CREATE_RULESET_VERSION))
	if e != 0 {
		return 0, e
	}
	return int(r), nil
}

func landlockCreateRuleset(attr *unix.LandlockRulesetAttr) (int, error) {
	r, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(attr)), unsafe.Sizeof(*attr), 0)
	if e != 0 {
		return 0, e
	}
	return int(r), nil
}

func landlockAddPathBeneath(rulesetFD int, attr *unix.LandlockPathBeneathAttr) error {
	_, _, e := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFD), uintptr(unix.LANDLOCK_RULE_PATH_BENEATH),
		uintptr(unsafe.Pointer(attr)), 0, 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

func landlockRestrictSelf(rulesetFD int) error {
	_, _, e := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFD), 0, 0)
	if e != 0 {
		return e
	}
	return nil
}
