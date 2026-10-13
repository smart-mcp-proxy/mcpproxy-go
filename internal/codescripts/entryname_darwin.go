//go:build darwin

package codescripts

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// Round 13 MUST-FIX (unify darwin onto the fd-bound Linux/BSD design):
// darwin now answers a scoped request from the same directory-generation
// index and the same retained-descriptor primitives every other Unix
// platform uses (dirfd_other.go, storednames_other.go — this file's build
// tag joined `unix` this round). x/sys/unix.Stat_t already normalizes the
// ctime field name across darwin and the BSDs (Mtim/Ctim, not the standard
// library syscall.Stat_t's Mtimespec/Ctimespec — see dirfd_other.go's own
// comment), so no darwin-specific generation reader is needed.
//
// What darwin keeps that Linux/BSD do not is F_GETPATH: a single-entry
// platform call that reports the exact on-disk spelling of an already-open
// descriptor. openat's own identity binding (dirfd_other.go) already proves
// the opened entry is a child of the retained directory descriptor — a
// retargeted symlink or ancestor cannot make it otherwise — so this file
// wires F_GETPATH in as an ADDITIONAL, belt-and-suspenders spelling proof
// registered on extraVerifyOpened (storednames_other.go): after the shared
// generation recheck passes, compare the opened descriptor's own reported
// basename (the parent is already bound by openat, so only the basename is
// worth comparing) to the name that was requested.
func init() {
	extraVerifyOpened = func(f *os.File, want string) error {
		stored, err := openedEntryName(f)
		if err != nil || stored != want {
			return errSpellingUnproven
		}
		return nil
	}
}

// openedEntryName reports the on-disk spelling of the directory entry an
// already-open descriptor was opened from (F_GETPATH), truncated to its base
// name — the proof this file registers on extraVerifyOpened, run on the
// descriptor that will actually be EXECUTED (openatEntry's result), not a
// separate pre-open probe of the same path.
func openedEntryName(f *os.File) (string, error) {
	return entryNameFromFd(f.Fd())
}

// entryNameFromFd is the shared F_GETPATH call.
func entryNameFromFd(fd uintptr) (string, error) {
	var buf [1024]byte // MAXPATHLEN
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETPATH, uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return "", errno
	}
	n := bytes.IndexByte(buf[:], 0)
	if n < 0 {
		n = len(buf)
	}
	return filepath.Base(string(buf[:n])), nil
}
