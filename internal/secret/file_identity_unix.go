//go:build unix

package secret

import (
	"os"
	"syscall"
)

// fileIdentity is a file's device and inode, captured eagerly so it keeps
// naming the same file after the path is renamed over.
type fileIdentity struct {
	dev, ino uint64
}

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

// identityOfPath stats path (following symlinks) and returns its identity.
func identityOfPath(path string) (fileIdentity, bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return fileIdentity{}, false
	}
	return identityFromInfo(fi)
}

// identityOfHandle returns the identity of an opened file from its fstat info.
func identityOfHandle(_ *os.File, info os.FileInfo) (fileIdentity, bool) {
	return identityFromInfo(info)
}
