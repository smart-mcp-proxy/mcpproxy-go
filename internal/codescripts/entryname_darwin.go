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

	var buf [1024]byte // MAXPATHLEN
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_GETPATH, uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return "", errno
	}
	n := bytes.IndexByte(buf[:], 0)
	if n < 0 {
		n = len(buf)
	}
	return filepath.Base(string(buf[:n])), nil
}
