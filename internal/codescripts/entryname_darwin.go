//go:build darwin

package codescripts

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// entryName returns the name the filesystem actually stores for the directory
// entry at path, without following a symlink and without listing the
// directory. The default APFS/HFS+ volumes are case-insensitive but
// case-PRESERVING: a probe for `backdoor.js` opens `backdoor.JS`, and the
// on-disk spelling is what F_GETPATH on the descriptor reports.
//
// O_SYMLINK opens a symlink itself rather than its target (the no-follow
// counterpart to Lstat), so a link's own entry name is the one verified;
// O_NONBLOCK keeps a FIFO from parking the open, as in openScriptFile.
func entryName(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_SYMLINK|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return entryNameFromFd(f.Fd())
}

// openedEntryName is entryName's post-open counterpart (round 9 MUST-FIX):
// it proves the stored spelling of the descriptor that will actually be
// EXECUTED, not of a separate pre-open probe of the same path — a
// case-rename or replacement landing between the pre-open probe
// (storedSpellingsOf) and openScriptFile's own open would otherwise let the
// wrong spelling run, because a case-folding volume folds the subsequent
// open onto whatever now occupies the name. F_GETPATH on the EXECUTED file's
// own descriptor is the same call entryName makes on a descriptor it opened
// itself for the pre-open probe; here it runs on the descriptor
// openScriptFile is about to read from.
func openedEntryName(f *os.File) (string, error) {
	return entryNameFromFd(f.Fd())
}

// entryNameFromFd is the shared F_GETPATH call both entryName and
// openedEntryName resolve to a base name.
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
