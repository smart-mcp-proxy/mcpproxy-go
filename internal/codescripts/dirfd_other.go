//go:build !darwin && !windows

package codescripts

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// This file is the round 11 MUST-FIX for the Linux/BSD directory-path ABA
// hole: every earlier round's scoped resolution re-resolved scriptsDir BY
// PATH at each step of one request — once to read the directory's
// generation, once per candidate to probe it, once more after the open to
// recheck the generation — and openScriptFile's own no-follow open resolved
// the path a FOURTH time to obtain the descriptor that is actually read.
// Four independent path resolutions leave a window between each pair of
// them: retarget a replaceable symlink, an ancestor directory, or a bind
// mount to a different directory B between two of those steps and back to
// the original A before the next one, and whichever step happens to run
// while the path points at A agrees with everything the index vouches for
// while the step that runs during the B window reads or opens B's content
// instead — st_dev (round 9) rules out a substitution visible DURING one
// snapshot, never an alternation across several snapshots taken at
// different moments.
//
// The fix binds the whole scoped resolution to ONE retained directory
// descriptor per request (storedSpellingsOf, storednames_other.go): the
// scripts directory is opened by path exactly ONCE (openScopedDir); its
// generation is read from THAT descriptor (fstatDirGeneration — an fstat,
// never another Lstat of the path); a candidate is probed relative to the
// SAME descriptor (fstatatEntry, AT_SYMLINK_NOFOLLOW — the no-follow
// counterpart of Lstat, but resolved against the retained fd rather than a
// fresh join of the path); the winning candidate is OPENED relative to the
// SAME descriptor (openatEntry) — so the directory entry Fstatat already
// probed is the exact one Openat opens, never a second, independent lookup
// of the name that a retargeted symlink could have answered differently;
// and the post-open recheck reads the generation from the SAME descriptor
// once more. No path is resolved twice, so nothing about the sequence can
// observe two different directories. The request's own cost stays O(1):
// one open, two fstats (one before the lookup, one after the open), one
// fstatat, one openat.
//
// The rebuild goroutine (storednames_other.go, off the request path) opens
// its own descriptor the same way and lists through it (listScopedDir), so
// the listing and the generation the index records for it come from the
// identical open — never a second resolution of the path either.
//
// Every primitive below is a variable so the package's tests can install a
// real symlink retarget between two of a request's own calls (the actual
// window the fix closes is between separate Go statements the caller makes,
// not inside a single syscall) and, for the narrower races a real retarget
// cannot land deterministically, hook the exact call the race would need to
// win.
var (
	openScopedDir      = defaultOpenScopedDir
	fstatDirGeneration = defaultFstatDirGeneration
	fstatatEntry       = defaultFstatatEntry
	openatEntry        = defaultOpenatEntry
	listScopedDir      = defaultListScopedDir
)

// defaultOpenScopedDir opens scriptsDir once. O_DIRECTORY refuses a
// non-directory at the path outright (a symlink resolving to a plain file
// would otherwise silently "open" as if it were an empty directory);
// O_CLOEXEC keeps the descriptor from leaking into a child process spawned
// while a request holds it.
func defaultOpenScopedDir(path string) (int, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return fd, nil
}

// defaultFstatDirGeneration reads a directory's generation stamp from an
// already-open descriptor — fstat, never a path lookup — so it can be
// called again, after the candidate open, without re-resolving scriptsDir.
// Same tuple as dirGenerationOf (modTime, changeTime, size, ino, dev; round
// 9 MUST-FIX folded dev into it), read from golang.org/x/sys/unix.Stat_t
// directly rather than through fs.FileInfo: the field names (Dev, Ino, Mtim,
// Ctim, Size) are uniform across every platform this file builds for, unlike
// the standard syscall.Stat_t, whose ctime field is spelled differently on
// the BSDs (see dirgeneration_ctim.go / dirgeneration_ctimespec.go, which
// remain the path-based reader the package's tests and the administrator's
// bookkeeping use).
func defaultFstatDirGeneration(fd int) (dirGeneration, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return dirGeneration{}, err
	}
	return dirGeneration{
		modTime:    time.Unix(st.Mtim.Unix()),
		changeTime: time.Unix(st.Ctim.Unix()),
		size:       st.Size,
		ino:        st.Ino,
		dev:        uint64(st.Dev),
	}, nil
}

// defaultFstatatEntry probes name relative to dirfd, AT_SYMLINK_NOFOLLOW —
// the candidate's own existence check, bound to the SAME descriptor the
// generation was just read from rather than a fresh Lstat of the joined
// path (which is exactly the second, independent path resolution the round
// 11 MUST-FIX removes).
func defaultFstatatEntry(dirfd int, name string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return &os.PathError{Op: "fstatat", Path: name, Err: err}
	}
	return nil
}

// defaultOpenatEntry opens name relative to dirfd, refusing a symlink
// atomically (O_NOFOLLOW — the same ELOOP/EMLINK-to-errNonRegular mapping
// open_unix.go's openScriptFile applies for the administrator) and never
// parking on a FIFO (O_NONBLOCK, for the identical reason documented
// there). This is the entry Fstatat already probed, opened relative to the
// SAME descriptor — never a second, independent lookup of the name.
func defaultOpenatEntry(dirfd int, name string) (*os.File, error) {
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EMLINK) {
			return nil, errNonRegular
		}
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

// defaultListScopedDir lists dirfd's entries through a DUP of the
// descriptor: os.File.Close on the dup releases only the copy, leaving the
// caller's own dirfd — and its read position — untouched. The rebuild
// goroutine calls this on the same descriptor its generation came from
// (storednames_other.go), so the listing and the generation the index
// records for it describe the identical open, never a second resolution of
// the path.
func defaultListScopedDir(dirfd int, path string) ([]string, error) {
	dupFd, err := unix.Dup(dirfd)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dupFd), path)
	defer func() { _ = f.Close() }()
	return f.Readdirnames(-1)
}
