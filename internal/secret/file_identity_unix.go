//go:build unix

package secret

import (
	"errors"
	"os"
	"syscall"
)

// fileIdentity is a file's device and inode, captured eagerly so it keeps
// naming the same file after the path is renamed over.
type fileIdentity struct {
	dev, ino uint64
}

var errIdentityNotRegular = errors.New("not a regular file")

func identityFromInfo(info os.FileInfo) (fileIdentity, bool) {
	if info == nil {
		return fileIdentity{}, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return fileIdentity{}, false
	}
	return fileIdentity{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true //nolint:unconvert // widths differ by platform
}

// identityOfPathErr stats path (following symlinks) and returns its identity.
// A missing file reports an error matching fs.ErrNotExist.
func identityOfPathErr(path string) (fileIdentity, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileIdentity{}, err
	}
	if !fi.Mode().IsRegular() {
		return fileIdentity{}, errIdentityNotRegular
	}
	id, ok := identityFromInfo(fi)
	if !ok {
		return fileIdentity{}, errors.New("file identity unavailable")
	}
	return id, nil
}

func identityOfPath(path string) (fileIdentity, bool) {
	id, err := identityOfPathErr(path)
	return id, err == nil
}

// identityOfHandle returns the identity of an opened file from its fstat info.
func identityOfHandle(_ *os.File, info os.FileInfo) (fileIdentity, bool) {
	return identityFromInfo(info)
}
